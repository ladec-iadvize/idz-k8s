package integration

import (
	"context"
	"testing"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func netpol(namespace, name string, spec map[string]interface{}) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "networking.k8s.io/v1",
		"kind":       "NetworkPolicy",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"spec":       spec,
	}}
}

// TestConnectivityMatchingAndSummary (FR-031): selecting policies are listed
// with their allowed peers; non-matching ones are ignored.
func TestConnectivityMatchingAndSummary(t *testing.T) {
	client, _ := NewFakeClient("",
		netpol("demo", "allow-front", map[string]interface{}{
			"podSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{"app": "back"},
			},
			"ingress": []interface{}{map[string]interface{}{
				"from": []interface{}{map[string]interface{}{
					"podSelector": map[string]interface{}{
						"matchLabels": map[string]interface{}{"app": "front"},
					},
				}},
				"ports": []interface{}{map[string]interface{}{
					"protocol": "TCP", "port": int64(8080),
				}},
			}},
		}),
		netpol("demo", "other-app", map[string]interface{}{
			"podSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{"app": "unrelated"},
			},
		}),
		netpol("demo", "deny-all-egress", map[string]interface{}{
			"podSelector": map[string]interface{}{}, // selects every pod
			"policyTypes": []interface{}{"Egress"},
		}),
	)
	rep, err := client.Connectivity(context.Background(), "demo", "pod demo/back-1",
		map[string]string{"app": "back", "extra": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Policies) != 2 || rep.Policies[0] != "allow-front" || rep.Policies[1] != "deny-all-egress" {
		t.Fatalf("policies=%v", rep.Policies)
	}
	if !rep.IngressRestricted || len(rep.Ingress) != 1 {
		t.Fatalf("ingress: restricted=%v rules=%+v", rep.IngressRestricted, rep.Ingress)
	}
	r := rep.Ingress[0]
	if r.Policy != "allow-front" || len(r.Peers) != 1 || r.Peers[0] != "pods app=front (same ns)" {
		t.Fatalf("ingress rule=%+v", r)
	}
	if len(r.Ports) != 1 || r.Ports[0] != "TCP/8080" {
		t.Fatalf("ports=%v", r.Ports)
	}
	// Egress declared with zero rules = default deny.
	if !rep.EgressRestricted || len(rep.Egress) != 0 {
		t.Fatalf("egress: restricted=%v rules=%+v", rep.EgressRestricted, rep.Egress)
	}
}

// TestConnectivityUnrestricted: no selecting policy → explicit unrestricted.
func TestConnectivityUnrestricted(t *testing.T) {
	client, _ := NewFakeClient("",
		netpol("demo", "other", map[string]interface{}{
			"podSelector": map[string]interface{}{
				"matchLabels": map[string]interface{}{"app": "unrelated"},
			},
		}),
	)
	rep, err := client.Connectivity(context.Background(), "demo", "pod demo/web-1",
		map[string]string{"app": "web"})
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Policies) != 0 || rep.IngressRestricted || rep.EgressRestricted {
		t.Fatalf("expected fully unrestricted, got %+v", rep)
	}
}

// TestConnectivityMatchExpressions: In/Exists/NotIn/DoesNotExist operators.
func TestConnectivityMatchExpressions(t *testing.T) {
	client, _ := NewFakeClient("",
		netpol("demo", "expr", map[string]interface{}{
			"podSelector": map[string]interface{}{
				"matchExpressions": []interface{}{
					map[string]interface{}{"key": "tier", "operator": "In", "values": []interface{}{"web", "api"}},
					map[string]interface{}{"key": "app", "operator": "Exists"},
					map[string]interface{}{"key": "legacy", "operator": "DoesNotExist"},
				},
			},
		}),
	)
	match := map[string]string{"tier": "web", "app": "x"}
	rep, _ := client.Connectivity(context.Background(), "demo", "s", match)
	if len(rep.Policies) != 1 {
		t.Fatalf("expression selector should match, got %v", rep.Policies)
	}
	noMatch := map[string]string{"tier": "db", "app": "x"}
	rep, _ = client.Connectivity(context.Background(), "demo", "s", noMatch)
	if len(rep.Policies) != 0 {
		t.Fatalf("tier=db must not match In(web,api), got %v", rep.Policies)
	}
	legacy := map[string]string{"tier": "web", "app": "x", "legacy": "1"}
	rep, _ = client.Connectivity(context.Background(), "demo", "s", legacy)
	if len(rep.Policies) != 0 {
		t.Fatalf("legacy label must fail DoesNotExist, got %v", rep.Policies)
	}
}
