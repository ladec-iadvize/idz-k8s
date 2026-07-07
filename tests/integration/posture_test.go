package integration

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"strings"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

// badPod builds a pod violating several posture rules at once.
func badPod(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{
			"containers": []interface{}{map[string]interface{}{
				"name":  "app",
				"image": "nginx:latest",
				"securityContext": map[string]interface{}{
					"privileged": true,
				},
			}},
		},
		"status": map[string]interface{}{"phase": "Running"},
	}}
}

// goodPod builds a pod passing every container rule.
func goodPod(namespace, name string) *unstructured.Unstructured {
	return &unstructured.Unstructured{Object: map[string]interface{}{
		"apiVersion": "v1",
		"kind":       "Pod",
		"metadata":   map[string]interface{}{"name": name, "namespace": namespace},
		"spec": map[string]interface{}{
			"securityContext": map[string]interface{}{"runAsNonRoot": true},
			"containers": []interface{}{map[string]interface{}{
				"name":  "app",
				"image": "nginx:1.27.1",
				"resources": map[string]interface{}{
					"requests": map[string]interface{}{"cpu": "100m", "memory": "128Mi"},
					"limits":   map[string]interface{}{"cpu": "200m", "memory": "256Mi"},
				},
				"livenessProbe":  map[string]interface{}{"httpGet": map[string]interface{}{"path": "/"}},
				"readinessProbe": map[string]interface{}{"httpGet": map[string]interface{}{"path": "/"}},
			}},
		},
		"status": map[string]interface{}{"phase": "Running"},
	}}
}

func selfSignedCertB64(t *testing.T, notAfter time.Time) string {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tpl := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "api.example.com"},
		NotBefore:    notAfter.Add(-365 * 24 * time.Hour),
		NotAfter:     notAfter,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	return base64.StdEncoding.EncodeToString(pemBytes)
}

func findRule(rows []model.PostureFinding, rule string) []model.PostureFinding {
	var out []model.PostureFinding
	for _, f := range rows {
		if f.Rule == rule {
			out = append(out, f)
		}
	}
	return out
}

// TestPostureRules: each FR-030 rule fires on a violating pod and stays
// silent on a compliant one.
func TestPostureRules(t *testing.T) {
	now := time.Date(2026, 7, 7, 12, 0, 0, 0, time.UTC)
	client, _ := NewFakeClient("",
		badPod("demo", "bad"),
		goodPod("clean", "good"),
		NewNetworkPolicy("clean", "default-deny"),
		NewTLSSecret("demo", "cert-soon", selfSignedCertB64(t, now.Add(10*24*time.Hour))),
		NewTLSSecret("demo", "cert-expired", selfSignedCertB64(t, now.Add(-24*time.Hour))),
		NewTLSSecret("clean", "cert-fine", selfSignedCertB64(t, now.Add(365*24*time.Hour))),
	)
	rows, err := client.Posture(context.Background(), "", now)
	if err != nil {
		t.Fatal(err)
	}

	for rule, wantOnBad := range map[string]string{
		kube.RuleNoResources: "no cpu request",
		kube.RulePrivileged:  "privileged: true",
		kube.RuleRoot:        "runAsNonRoot",
		kube.RuleNoProbes:    "liveness",
		kube.RuleLatestImage: "nginx:latest",
	} {
		fs := findRule(rows, rule)
		if len(fs) != 1 || fs[0].Name != "bad" || fs[0].Container != "app" {
			t.Errorf("%s: want exactly one finding on bad/app, got %+v", rule, fs)
			continue
		}
		if wantOnBad != "" && !contains(fs[0].Detail, wantOnBad) {
			t.Errorf("%s: detail %q should mention %q", rule, fs[0].Detail, wantOnBad)
		}
	}
	if fs := findRule(rows, kube.RulePrivileged); len(fs) == 1 && fs[0].Severity != model.HealthError {
		t.Error("privileged must be an error-level finding")
	}

	// NetworkPolicy: demo uncovered, clean covered.
	nps := findRule(rows, kube.RuleNoNetpol)
	if len(nps) != 1 || nps[0].Namespace != "demo" {
		t.Errorf("netpol rule: want exactly demo flagged, got %+v", nps)
	}

	// TLS: expired (error) + expiring soon (warning); the 1-year cert silent.
	tls := findRule(rows, kube.RuleTLSExpiry)
	if len(tls) != 2 {
		t.Fatalf("tls rule: want 2 findings, got %+v", tls)
	}
	byName := map[string]model.PostureFinding{}
	for _, f := range tls {
		byName[f.Name] = f
	}
	if byName["cert-expired"].Severity != model.HealthError || !contains(byName["cert-expired"].Detail, "EXPIRED") {
		t.Errorf("expired cert: %+v", byName["cert-expired"])
	}
	if byName["cert-soon"].Severity != model.HealthWarning || !contains(byName["cert-soon"].Detail, "10 day") {
		t.Errorf("expiring cert: %+v", byName["cert-soon"])
	}
}

// TestPostureScopeAndFinishedPods: finished pods are skipped and the
// namespace scope (incl. globs) applies.
func TestPostureScopeAndFinishedPods(t *testing.T) {
	done := badPod("demo", "done")
	_ = unstructured.SetNestedField(done.Object, "Succeeded", "status", "phase")
	client, _ := NewFakeClient("",
		done,
		badPod("staging-a", "bad-a"),
		badPod("prod", "bad-p"),
	)
	rows, err := client.Posture(context.Background(), "staging-*", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range rows {
		if f.Namespace != "staging-a" {
			t.Errorf("finding leaked outside the pattern scope: %+v", f)
		}
	}
	if len(findRule(rows, kube.RulePrivileged)) != 1 {
		t.Errorf("expected exactly the staging-a privileged finding, got %+v", rows)
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
