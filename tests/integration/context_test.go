package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/iadvize/idz-k8s/internal/kube"
)

const twoContextKubeconfig = `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: c1
  cluster:
    server: https://127.0.0.1:6443
- name: c2
  cluster:
    server: https://127.0.0.1:6444
contexts:
- name: dev
  context:
    cluster: c1
    user: u1
    namespace: dev-ns
- name: prod
  context:
    cluster: c2
    user: u1
    namespace: prod-ns
users:
- name: u1
  user:
    token: fake-token
`

func writeKubeconfig(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "kubeconfig")
	if err := os.WriteFile(path, []byte(twoContextKubeconfig), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestContextsListsAllWithActive(t *testing.T) {
	path := writeKubeconfig(t)
	ctxs, err := kube.Contexts(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(ctxs) != 2 {
		t.Fatalf("expected 2 contexts, got %d", len(ctxs))
	}
	var activeName string
	for _, c := range ctxs {
		if c.Active {
			activeName = c.Name
		}
	}
	if activeName != "dev" {
		t.Fatalf("expected 'dev' to be active (current-context), got %q", activeName)
	}
}

func TestNewClientResolvesActiveContext(t *testing.T) {
	path := writeKubeconfig(t)

	// Default: current-context is "dev" with namespace "dev-ns".
	def, err := kube.NewClient(kube.Options{KubeconfigPath: path})
	if err != nil {
		t.Fatal(err)
	}
	if def.ActiveContext() != "dev" {
		t.Errorf("default active context should be dev, got %q", def.ActiveContext())
	}
	// Default scope is ALL namespaces (empty), not the context's namespace:
	// the overview tool starts wide by design.
	if def.Namespace != "" {
		t.Errorf("default namespace scope should be all (empty), got %q", def.Namespace)
	}

	// Switching to "prod" must be reflected in ActiveContext; an explicit
	// namespace is honored.
	prod, err := kube.NewClient(kube.Options{KubeconfigPath: path, Context: "prod", Namespace: "team-a"})
	if err != nil {
		t.Fatal(err)
	}
	if prod.ActiveContext() != "prod" {
		t.Errorf("switched active context should be prod, got %q", prod.ActiveContext())
	}
	if prod.Namespace != "team-a" {
		t.Errorf("explicit namespace should be honored, got %q", prod.Namespace)
	}
}
