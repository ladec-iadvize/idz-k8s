package kube

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/iadvize/idz-k8s/internal/model"
)

// OwnerRef is one step up the ownership chain (US9): the controller that owns
// an object, e.g. a Pod's ReplicaSet, a ReplicaSet's Deployment.
type OwnerRef struct {
	Group   string
	Version string
	Kind    string
	Name    string
}

// Owner extracts the first ownerReference of an object. ok=false when the
// object has no owner (top of the chain).
func Owner(raw map[string]interface{}) (OwnerRef, bool) {
	refs, found, _ := unstructured.NestedSlice(raw, "metadata", "ownerReferences")
	if !found || len(refs) == 0 {
		return OwnerRef{}, false
	}
	rm, ok := refs[0].(map[string]interface{})
	if !ok {
		return OwnerRef{}, false
	}
	apiVersion, _ := rm["apiVersion"].(string)
	kind, _ := rm["kind"].(string)
	name, _ := rm["name"].(string)
	if kind == "" || name == "" {
		return OwnerRef{}, false
	}
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return OwnerRef{}, false
	}
	return OwnerRef{Group: gv.Group, Version: gv.Version, Kind: kind, Name: name}, true
}

var (
	endpointsGVR      = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "endpoints"}
	endpointSlicesGVR = schema.GroupVersionResource{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}
)

// serviceNameLabel links an EndpointSlice to its Service.
const serviceNameLabel = "kubernetes.io/service-name"

// EndpointsSummary reports a Service's backends (US9 broken-link detection):
// ready and not-ready counts. It reads discovery.k8s.io/v1 EndpointSlices
// (v1 Endpoints is deprecated since K8s 1.33) and falls back to the legacy
// Endpoints object on clusters/RBAC where slices are unavailable.
func (c *Client) EndpointsSummary(ctx context.Context, namespace, service string) (ready, notReady int, err error) {
	if c == nil || c.Dynamic == nil {
		return 0, 0, fmt.Errorf("kubernetes client not initialized")
	}
	ul, err := c.Dynamic.Resource(endpointSlicesGVR).Namespace(namespace).List(ctx, metav1.ListOptions{
		LabelSelector: serviceNameLabel + "=" + service,
	})
	if err == nil && len(ul.Items) > 0 {
		for i := range ul.Items {
			r, nr := countSlice(&ul.Items[i])
			ready += r
			notReady += nr
		}
		return ready, notReady, nil
	}
	// Fallback: legacy v1 Endpoints.
	u, gerr := c.Dynamic.Resource(endpointsGVR).Namespace(namespace).Get(ctx, service, metav1.GetOptions{})
	if gerr != nil {
		return 0, 0, gerr
	}
	ready, notReady = countLegacyEndpoints(u)
	return ready, notReady, nil
}

// countSlice counts ready/not-ready endpoints of one EndpointSlice. Per the
// API, an absent conditions.ready means "ready".
func countSlice(u *unstructured.Unstructured) (ready, notReady int) {
	eps, _, _ := unstructured.NestedSlice(u.Object, "endpoints")
	for _, e := range eps {
		em, ok := e.(map[string]interface{})
		if !ok {
			continue
		}
		if r, found, _ := unstructured.NestedBool(em, "conditions", "ready"); found && !r {
			notReady++
			continue
		}
		ready++
	}
	return ready, notReady
}

func countLegacyEndpoints(u *unstructured.Unstructured) (ready, notReady int) {
	subsets, _, _ := unstructured.NestedSlice(u.Object, "subsets")
	for _, s := range subsets {
		sm, ok := s.(map[string]interface{})
		if !ok {
			continue
		}
		if addrs, ok := sm["addresses"].([]interface{}); ok {
			ready += len(addrs)
		}
		if nr, ok := sm["notReadyAddresses"].([]interface{}); ok {
			notReady += len(nr)
		}
	}
	return ready, notReady
}

// EndpointsByService lists backends for every service in a namespace (empty →
// all) with a single LIST, keyed by "ns/name". Prefers EndpointSlices (the
// non-deprecated API), aggregating slices per service via their
// kubernetes.io/service-name label; falls back to legacy Endpoints.
func (c *Client) EndpointsByService(ctx context.Context, namespace string) (map[string][2]int, error) {
	if c == nil || c.Dynamic == nil {
		return nil, fmt.Errorf("kubernetes client not initialized")
	}
	if out, err := c.slicesByService(ctx, namespace); err == nil && len(out) > 0 {
		return out, nil
	}
	// Fallback: legacy v1 Endpoints.
	apiNS, _ := namespaceScope(namespace)
	ul, err := c.listGVR(ctx, endpointsGVR, apiNS, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string][2]int, len(ul.Items))
	for i := range ul.Items {
		u := &ul.Items[i]
		ready, notReady := countLegacyEndpoints(u)
		out[u.GetNamespace()+"/"+u.GetName()] = [2]int{ready, notReady}
	}
	return out, nil
}

func (c *Client) slicesByService(ctx context.Context, namespace string) (map[string][2]int, error) {
	apiNS, _ := namespaceScope(namespace)
	ul, err := c.listGVR(ctx, endpointSlicesGVR, apiNS, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	out := make(map[string][2]int, len(ul.Items))
	for i := range ul.Items {
		u := &ul.Items[i]
		svc := u.GetLabels()[serviceNameLabel]
		if svc == "" {
			continue
		}
		ready, notReady := countSlice(u)
		key := u.GetNamespace() + "/" + svc
		cur := out[key]
		out[key] = [2]int{cur[0] + ready, cur[1] + notReady}
	}
	return out, nil
}

// ServiceStatus derives a Service's display status from its backends:
// ready endpoints → Ok, none while a selector exists → Error (broken link),
// selector-less services (ExternalName, manual endpoints) → neutral.
func ServiceStatus(raw map[string]interface{}, eps map[string][2]int, namespace, name string) model.StatusSummary {
	if _, hasSelector := PodSelector(raw); !hasSelector {
		// No selector: nothing is supposed to be "ready" (ExternalName etc.).
		return model.StatusSummary{Level: model.HealthOk, Reason: "external"}
	}
	counts := eps[namespace+"/"+name]
	ready, notReady := counts[0], counts[1]
	switch {
	case ready > 0:
		return model.StatusSummary{Level: model.HealthOk, Reason: fmt.Sprintf("%d eps", ready)}
	case notReady > 0:
		return model.StatusSummary{Level: model.HealthWarning, Reason: fmt.Sprintf("0/%d eps", ready+notReady)}
	default:
		return model.StatusSummary{Level: model.HealthError, Reason: "no eps"}
	}
}

// ResolveMarkedPods expands a set of marked resources into the pods they
// cover (read-only): a marked Pod is itself; a marked workload/Service
// contributes the pods its label selector owns. Keys are "ns/name".
func ResolveMarkedPods(ctx context.Context, c *Client, marked []model.ResourceObject) (map[string]bool, error) {
	allowed := map[string]bool{}
	podType := model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}
	for _, o := range marked {
		if o.Type.Kind == "Pod" || o.Type.Resource == "pods" {
			allowed[o.Namespace+"/"+o.Name] = true
			continue
		}
		sel, ok := PodSelector(o.Raw)
		if !ok {
			continue
		}
		pods, err := c.ListSelected(ctx, podType, o.Namespace, sel)
		if err != nil {
			return nil, err
		}
		for _, p := range pods {
			allowed[p.Namespace+"/"+p.Name] = true
		}
	}
	return allowed, nil
}
