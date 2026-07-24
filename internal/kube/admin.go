package kube

// Cluster administration operations (v3): edit, scale, delete, restart,
// cordon, suspend. Every mutation here is triggered from the UI's actions
// palette behind an explicit confirmation step — no mutation ever runs
// implicitly (FR-012 v3).

import (
	"context"
	"fmt"
	"time"

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

// ApplyYAML parses an edited YAML document and updates the object it
// describes. The document must keep its metadata.name (and resourceVersion,
// so a concurrent change surfaces as a conflict instead of being clobbered).
func (c *Client) ApplyYAML(ctx context.Context, t model.ResourceType, doc []byte) error {
	jsonDoc, err := yaml.YAMLToJSON(doc)
	if err != nil {
		return fmt.Errorf("parsing edited YAML: %w", err)
	}
	// UnmarshalJSON goes through UnstructuredJSONScheme, which keeps integers
	// as int64 (a plain yaml.Unmarshal would degrade them to float64).
	obj := &unstructured.Unstructured{}
	if err := obj.UnmarshalJSON(jsonDoc); err != nil {
		return fmt.Errorf("parsing edited YAML: %w", err)
	}
	if obj.GetName() == "" {
		return fmt.Errorf("edited YAML has no metadata.name")
	}
	if _, err := c.resourceFor(t, obj.GetNamespace()).Update(ctx, obj, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
		return fmt.Errorf("updating %s/%s: %w", t.Kind, obj.GetName(), err)
	}
	return nil
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
