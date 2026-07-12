// Package integration provides a shared, cluster-free test harness: fake
// client-go clients (dynamic + typed + discovery) so the read-only kube layer
// can be exercised without a live cluster (research.md D10, task T017).
package integration

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// PodsType is the built-in Pod resource type used across tests.
var PodsType = model.ResourceType{Version: "v1", Kind: "Pod", Resource: "pods", Namespaced: true}

// SecretsType is the built-in Secret resource type used across tests.
var SecretsType = model.ResourceType{Version: "v1", Kind: "Secret", Resource: "secrets", Namespaced: true}

var podsGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

// NewPod builds an unstructured Pod with a status phase.
func NewPod(namespace, name, phase string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata": map[string]interface{}{
			"name":      name,
			"namespace": namespace,
		},
		"status": map[string]interface{}{
			"phase": phase,
		},
	}}
}

// NewSecret builds an unstructured Secret with base64-ish data values.
func NewSecret(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"data":       map[string]interface{}{"password": "c3VwZXItc2VjcmV0"},
	}}
}

// NewNode builds an unstructured (cluster-scoped) Node with a Ready condition.
func NewNode(name string, ready bool) *unstructured.Unstructured {
	status := "True"
	if !ready {
		status = "False"
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Node",
		"metadata":   map[string]any{"name": name},
		"status": map[string]any{"conditions": []any{
			map[string]any{"type": "Ready", "status": status},
		}},
	}}
}

// NewPodOnNode builds a pod scheduled on a given node (empty node → unscheduled).
func NewPodOnNode(namespace, name, node, phase string) *unstructured.Unstructured {
	p := NewPod(namespace, name, phase)
	if node != "" {
		p.Object["spec"] = map[string]any{"nodeName": node}
	}
	return p
}

// NewNetworkPolicy builds a minimal NetworkPolicy (posture rule fixture).
func NewNetworkPolicy(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"spec":       map[string]interface{}{"podSelector": map[string]interface{}{}},
	}}
}

// NewTLSSecret builds a kubernetes.io/tls secret holding the given PEM cert.
func NewTLSSecret(namespace, name, crtB64 string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Secret",
		"type":       "kubernetes.io/tls",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"data":       map[string]interface{}{"tls.crt": crtB64, "tls.key": ""},
	}}
}

// NewNamespace builds an unstructured (cluster-scoped) Namespace object.
func NewNamespace(name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "v1",
		"kind":       "Namespace",
		"metadata":   map[string]any{"name": name},
	}}
}

// NewFakeClient returns a read-only kube.Client backed by fake clients holding
// the given objects, plus the underlying fake dynamic client for action
// inspection (zero-mutation assertion).
func NewFakeClient(namespace string, objs ...*unstructured.Unstructured) (*kube.Client, *dynamicfake.FakeDynamicClient) {
	scheme := runtime.NewScheme()
	// Every kind the app has dedicated columns or drill flows for must be
	// registered here — the fake dynamic client refuses to LIST an unknown
	// kind, walling off any integration test on that type.
	listKinds := map[schema.GroupVersionResource]string{
		podsGVR: "PodList",
		{Group: "", Version: "v1", Resource: "secrets"}:                             "SecretList",
		{Group: "", Version: "v1", Resource: "namespaces"}:                          "NamespaceList",
		{Group: "", Version: "v1", Resource: "nodes"}:                               "NodeList",
		{Group: "", Version: "v1", Resource: "events"}:                              "EventList",
		{Group: "", Version: "v1", Resource: "endpoints"}:                           "EndpointsList",
		{Group: "", Version: "v1", Resource: "services"}:                            "ServiceList",
		{Group: "", Version: "v1", Resource: "configmaps"}:                          "ConfigMapList",
		{Group: "", Version: "v1", Resource: "persistentvolumeclaims"}:              "PersistentVolumeClaimList",
		{Group: "", Version: "v1", Resource: "persistentvolumes"}:                   "PersistentVolumeList",
		{Group: "apps", Version: "v1", Resource: "deployments"}:                     "DeploymentList",
		{Group: "apps", Version: "v1", Resource: "statefulsets"}:                    "StatefulSetList",
		{Group: "apps", Version: "v1", Resource: "replicasets"}:                     "ReplicaSetList",
		{Group: "apps", Version: "v1", Resource: "daemonsets"}:                      "DaemonSetList",
		{Group: "batch", Version: "v1", Resource: "jobs"}:                           "JobList",
		{Group: "batch", Version: "v1", Resource: "cronjobs"}:                       "CronJobList",
		{Group: "autoscaling", Version: "v2", Resource: "horizontalpodautoscalers"}: "HorizontalPodAutoscalerList",
		{Group: "networking.k8s.io", Version: "v1", Resource: "ingresses"}:          "IngressList",
		{Group: "discovery.k8s.io", Version: "v1", Resource: "endpointslices"}:      "EndpointSliceList",
		{Group: "networking.k8s.io", Version: "v1", Resource: "networkpolicies"}:    "NetworkPolicyList",
	}
	runtimeObjs := make([]runtime.Object, 0, len(objs))
	for _, o := range objs {
		runtimeObjs = append(runtimeObjs, o)
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, listKinds, runtimeObjs...)
	cs := k8sfake.NewSimpleClientset()

	// Seed discovery so ResourceTypes() finds pods/secrets (incl. verbs).
	if fd, ok := cs.Discovery().(*fakediscovery.FakeDiscovery); ok {
		fd.Resources = []*metav1.APIResourceList{
			{GroupVersion: "v1", APIResources: []metav1.APIResource{
				{Name: "pods", Kind: "Pod", Namespaced: true, Verbs: []string{"get", "list", "watch"}, ShortNames: []string{"po"}},
				{Name: "secrets", Kind: "Secret", Namespaced: true, Verbs: []string{"get", "list", "watch"}},
			}},
		}
	}

	client := &kube.Client{
		Dynamic:   dyn,
		Discovery: cs.Discovery(),
		Clientset: cs,
		Namespace: namespace,
	}
	return client, dyn
}

// VerbsFromActions extracts the verb of each recorded fake action.
func VerbsFromActions(actions []k8stesting.Action) []string {
	verbs := make([]string, 0, len(actions))
	for _, a := range actions {
		verbs = append(verbs, a.GetVerb())
	}
	return verbs
}

// DiscoveryList is a convenience for building discovery input for
// kube.ParseResourceTypes in tests.
func DiscoveryList(groupVersion string, resources ...metav1.APIResource) *metav1.APIResourceList {
	return &metav1.APIResourceList{GroupVersion: groupVersion, APIResources: resources}
}
