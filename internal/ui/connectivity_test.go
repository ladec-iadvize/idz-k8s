package ui

import (
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// TestRenderConnectivityStates: restricted rules are listed with peers and
// ports; the unrestricted pod and the default-deny direction are explicit.
func TestRenderConnectivityStates(t *testing.T) {
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}))
	m.width, m.height = 110, 30
	m.layout()
	m.screen = screenConnectivity

	m.renderConnectivity(model.ConnectivityReport{
		Subject: "pod demo/back-1", Namespace: "demo",
		Policies:          []string{"allow-front", "deny-all-egress"},
		IngressRestricted: true,
		Ingress: []model.PolicyRule{{
			Policy: "allow-front",
			Peers:  []string{"pods app=front (same ns)"},
			Ports:  []string{"TCP/8080"},
		}},
		EgressRestricted: true, // declared but no rules = default deny
	})
	content := m.connectivity.View()
	for _, want := range []string{
		"pod demo/back-1", "allow-front, deny-all-egress",
		"INGRESS", "pods app=front (same ns)", "TCP/8080",
		"EGRESS", "default deny",
	} {
		if !strings.Contains(content, want) {
			t.Fatalf("connectivity view missing %q:\n%s", want, content)
		}
	}

	// Unrestricted pod: explicit, never empty (FR-031).
	m.renderConnectivity(model.ConnectivityReport{Subject: "pod demo/free", Namespace: "demo"})
	if !strings.Contains(m.connectivity.View(), "UNRESTRICTED") {
		t.Fatal("unrestricted state must be explicit")
	}
}

// TestOpenConnectivityOnWorkloadUsesTemplateLabels: a Deployment is evaluated
// on its pod template labels without needing a live pod.
func TestOpenConnectivityOnWorkloadUsesTemplateLabels(t *testing.T) {
	dep := model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(dep))
	m.width, m.height = 100, 24
	m.layout()
	m.objects = []model.ResourceObject{{
		Type: dep, Namespace: "demo", Name: "back",
		Raw: map[string]interface{}{
			"metadata": map[string]interface{}{"name": "back", "namespace": "demo"},
			"spec": map[string]interface{}{
				"template": map[string]interface{}{
					"metadata": map[string]interface{}{
						"labels": map[string]interface{}{"app": "back"},
					},
				},
			},
		},
	}}
	m.applyRows()
	mi, cmd := m.openConnectivity()
	m = asModel(t, mi)
	if m.screen != screenConnectivity || cmd == nil {
		t.Fatalf("connectivity did not open (screen=%d cmd=%v)", m.screen, cmd)
	}
	if !strings.Contains(m.connectivity.View(), "pods of Deployment/back") {
		t.Fatal("subject must reference the workload's pods")
	}

	// A kind without a pod template refuses with a hint.
	m.screen = screenList
	m.curType = model.ResourceType{Version: "v1", Resource: "configmaps", Kind: "ConfigMap", Namespaced: true}
	m.objects = []model.ResourceObject{{Namespace: "demo", Name: "cm", Raw: map[string]interface{}{}}}
	m.applyRows()
	mi, _ = m.openConnectivity()
	m = asModel(t, mi)
	if m.screen == screenConnectivity || m.statusMsg == "" {
		t.Fatalf("ConfigMap must be refused with a hint (screen=%d msg=%q)", m.screen, m.statusMsg)
	}
}
