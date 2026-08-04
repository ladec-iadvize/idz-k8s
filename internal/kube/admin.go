package kube

// Cluster administration operations (v3): edit, scale, delete, restart,
// cordon, suspend. Every mutation here is triggered from the UI's actions
// palette behind an explicit confirmation step — no mutation ever runs
// implicitly (FR-012 v3).

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	jsonpatch "github.com/evanphx/json-patch"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/iadvize/idz-k8s/internal/model"
)

// fieldManager identifies this tool in managedFields for every write.
const fieldManager = "idz-k8s"

// resourceFor returns the dynamic interface for a type, namespace-scoped when
// the type is namespaced and a namespace is given.
func (c *Client) resourceFor(t model.ResourceType, namespace string) dynamic.ResourceInterface {
	ri := c.Dynamic.Resource(gvr(t))
	if t.Namespaced && namespace != "" {
		return ri.Namespace(namespace)
	}
	return ri
}

// ObjectYAML fetches the live object and renders it as editable YAML.
// managedFields are stripped (pure server bookkeeping, kubectl edit does the
// same); everything else — status included — stays visible.
func (c *Client) ObjectYAML(ctx context.Context, t model.ResourceType, namespace, name string) (string, error) {
	obj, err := c.resourceFor(t, namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting %s/%s: %w", t.Kind, name, err)
	}
	unstructured.RemoveNestedField(obj.Object, "metadata", "managedFields")
	data, err := yaml.Marshal(obj.Object)
	if err != nil {
		return "", fmt.Errorf("rendering %s/%s: %w", t.Kind, name, err)
	}
	return string(data), nil
}

// ApplyEditedYAML applies an edit as a MERGE PATCH of what the operator
// actually changed (original → edited), the way kubectl edit does — never a
// whole-object Update. That matters twice (owner report 2026-08-04):
//   - an Update carries the resourceVersion captured when the editor opened,
//     so any churn during the edit session (a Deployment's status conditions,
//     an HPA scaling it) makes the save fail with a 409 conflict;
//   - a patch touches only the edited fields, so concurrent changes to OTHER
//     fields survive instead of being clobbered.
//
// changed=false means the documents are equivalent: nothing is sent.
func (c *Client) ApplyEditedYAML(ctx context.Context, t model.ResourceType, original, edited []byte) (changed bool, err error) {
	origJSON, err := yaml.YAMLToJSON(original)
	if err != nil {
		return false, fmt.Errorf("reading the original YAML: %w", err)
	}
	editedJSON, err := yaml.YAMLToJSON(edited)
	if err != nil {
		return false, fmt.Errorf("parsing edited YAML: %w", err)
	}
	// UnmarshalJSON goes through UnstructuredJSONScheme, which keeps integers
	// as int64 (a plain yaml.Unmarshal would degrade them to float64).
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(editedJSON); err != nil {
		return false, fmt.Errorf("parsing edited YAML: %w", err)
	}
	if obj.GetName() == "" {
		return false, fmt.Errorf("edited YAML has no metadata.name")
	}
	patch, err := jsonpatch.CreateMergePatch(origJSON, editedJSON)
	if err != nil {
		return false, fmt.Errorf("diffing the edit: %w", err)
	}
	if len(patch) == 0 || string(patch) == "{}" {
		return false, nil
	}
	if _, err := c.resourceFor(t, obj.GetNamespace()).Patch(ctx, obj.GetName(),
		types.MergePatchType, patch, metav1.PatchOptions{FieldManager: fieldManager}); err != nil {
		return false, fmt.Errorf("applying the edit to %s/%s: %w", t.Kind, obj.GetName(), err)
	}
	return true, nil
}

// MergePatchFields lists the top-level paths an edit touches — what the
// status line reports back so the operator sees WHAT was applied.
func MergePatchFields(original, edited []byte) []string {
	origJSON, err1 := yaml.YAMLToJSON(original)
	editedJSON, err2 := yaml.YAMLToJSON(edited)
	if err1 != nil || err2 != nil {
		return nil
	}
	patch, err := jsonpatch.CreateMergePatch(origJSON, editedJSON)
	if err != nil {
		return nil
	}
	var m map[string]any
	if json.Unmarshal(patch, &m) != nil {
		return nil
	}
	return topPaths(m, "")
}

// topPaths flattens a merge patch into dotted paths, stopping at the level
// where a whole subtree is replaced (2 levels deep is enough to be useful).
func topPaths(m map[string]any, prefix string) []string {
	var out []string
	for k, v := range m {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if sub, ok := v.(map[string]any); ok && prefix == "" && len(sub) > 0 {
			out = append(out, topPaths(sub, path)...)
			continue
		}
		out = append(out, path)
	}
	sort.Strings(out)
	return out
}

// ScaleWorkload sets spec.replicas on a scalable workload
// (Deployment/StatefulSet/ReplicaSet).
func (c *Client) ScaleWorkload(ctx context.Context, t model.ResourceType, namespace, name string, replicas int) error {
	patch := fmt.Sprintf(`{"spec":{"replicas":%d}}`, replicas)
	return c.mergePatch(ctx, t, namespace, name, patch)
}

// DeleteObject deletes one object (default propagation: dependents follow).
func (c *Client) DeleteObject(ctx context.Context, t model.ResourceType, namespace, name string) error {
	if err := c.resourceFor(t, namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("deleting %s/%s: %w", t.Kind, name, err)
	}
	return nil
}

// RolloutRestart triggers a rolling restart the same way kubectl does: a
// restartedAt annotation on the pod template.
func (c *Client) RolloutRestart(ctx context.Context, t model.ResourceType, namespace, name string, at time.Time) error {
	patch := fmt.Sprintf(
		`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`,
		at.UTC().Format(time.RFC3339))
	return c.mergePatch(ctx, t, namespace, name, patch)
}

// SetCordon marks a node (un)schedulable.
func (c *Client) SetCordon(ctx context.Context, t model.ResourceType, name string, cordon bool) error {
	return c.mergePatch(ctx, t, "", name, fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, cordon))
}

// SetSuspend suspends or resumes a CronJob.
func (c *Client) SetSuspend(ctx context.Context, t model.ResourceType, namespace, name string, suspend bool) error {
	return c.mergePatch(ctx, t, namespace, name, fmt.Sprintf(`{"spec":{"suspend":%t}}`, suspend))
}

func (c *Client) mergePatch(ctx context.Context, t model.ResourceType, namespace, name, patch string) error {
	_, err := c.resourceFor(t, namespace).Patch(ctx, name, types.MergePatchType,
		[]byte(patch), metav1.PatchOptions{FieldManager: fieldManager})
	if err != nil {
		return fmt.Errorf("patching %s/%s: %w", t.Kind, name, err)
	}
	return nil
}

// TriggerCronJob creates a Job from a CronJob's template right now — what
// `kubectl create job --from=cronjob/x` does (v3 admin, UI-confirmed).
// Returns the created job's name.
func (c *Client) TriggerCronJob(ctx context.Context, t model.ResourceType, namespace, name string, at time.Time) (string, error) {
	cj, err := c.resourceFor(t, namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("getting CronJob/%s: %w", name, err)
	}
	tplSpec, found, _ := unstructured.NestedMap(cj.Object, "spec", "jobTemplate", "spec")
	if !found {
		return "", fmt.Errorf("CronJob/%s has no job template", name)
	}
	// Job names are DNS labels (63 chars): keep room for the suffix.
	base := name
	if len(base) > 47 {
		base = base[:47]
	}
	jobName := fmt.Sprintf("%s-manual-%s", base, at.UTC().Format("150405"))
	job := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "batch/v1",
		"kind":       "Job",
		"metadata": map[string]any{
			"name":      jobName,
			"namespace": namespace,
			// The annotation kubectl sets for manual runs — makes the origin
			// auditable and keeps controllers from double-counting it.
			"annotations": map[string]any{"cronjob.kubernetes.io/instantiate": "manual"},
			// Owner reference with controller=false (owner report 2026-08-03):
			// kubectl leaves manual Jobs ownerless, so they never showed up
			// under their CronJob in the drill view. Claiming ownership WITHOUT
			// the controller flag is the sweet spot: the Job is listed under
			// its CronJob and garbage-collected with it, while the CronJob
			// controller — which only counts jobs it CONTROLS — still ignores
			// it, so concurrencyPolicy and the history limits are untouched.
			"ownerReferences": []any{map[string]any{
				"apiVersion":         cj.GetAPIVersion(),
				"kind":               cj.GetKind(),
				"name":               cj.GetName(),
				"uid":                string(cj.GetUID()),
				"controller":         false,
				"blockOwnerDeletion": false,
			}},
		},
		"spec": tplSpec,
	}}
	jobs := model.ResourceType{Group: "batch", Version: "v1", Kind: "Job", Resource: "jobs", Namespaced: true}
	if _, err := c.resourceFor(jobs, namespace).Create(ctx, job, metav1.CreateOptions{FieldManager: fieldManager}); err != nil {
		return "", fmt.Errorf("creating Job/%s: %w", jobName, err)
	}
	return jobName, nil
}

// FirstReadyPod resolves a selector to one ready pod — the port-forward
// target for workloads and services (kubectl-like resolution).
func (c *Client) FirstReadyPod(ctx context.Context, namespace, selector string) (string, error) {
	pods, err := c.ListSelected(ctx, model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}, namespace, selector)
	if err != nil {
		return "", err
	}
	for _, p := range pods {
		if r, d, ok := ReadyCount("Pod", p.Raw); ok && d > 0 && r == d {
			return p.Name, nil
		}
	}
	if len(pods) > 0 {
		return pods[0].Name, nil // no fully-ready pod: best effort
	}
	return "", fmt.Errorf("selector matches no pods")
}
