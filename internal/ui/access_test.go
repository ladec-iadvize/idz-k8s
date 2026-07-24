package ui

import (
	"errors"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func accessModel(t *testing.T) Model {
	t.Helper()
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "",
		WithInitialType(model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}))
	m.width, m.height = 110, 30
	m.layout()
	return m
}

// TestRenderAccessReport: rules, incomplete flag, and unlistable types are
// all visible; the no-access state is explicit.
func TestRenderAccessReport(t *testing.T) {
	m := accessModel(t)
	m.screen = screenAccess
	m.renderAccess(model.AccessReport{
		Namespace:  "demo",
		Incomplete: true,
		Rules: []model.AccessRule{
			{Verbs: []string{"get", "list", "watch"}, Groups: []string{""}, Resources: []string{"pods", "pods/log"}},
		},
		Unlistable: []string{"batch/v1/cronjobs"},
	})
	content := m.access.View()
	for _, want := range []string{"Access (RBAC)", "ns demo", "INCOMPLETE", "get,list,watch", "pods, pods/log", "batch/v1/cronjobs"} {
		if !strings.Contains(content, want) {
			t.Fatalf("access view missing %q:\n%s", want, content)
		}
	}
	m.renderAccess(model.AccessReport{Namespace: "demo"})
	if !strings.Contains(m.access.View(), "no read access") {
		t.Fatal("empty rule set must be explicit")
	}
}

// TestForbiddenListIsAccessNotDisconnection (FR-032): a 403 on list names the
// type and points to the access view — no reconnect banner.
func TestForbiddenListIsAccessNotDisconnection(t *testing.T) {
	m := accessModel(t)
	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("RBAC denied"))
	mi, _ := m.Update(objectsMsg{err: forbidden})
	m = asModel(t, mi)
	if m.disconnected {
		t.Fatal("a 403 is not a lost connection")
	}
	if !strings.Contains(m.errMsg, "forbidden") || !strings.Contains(m.errMsg, "v1/pods") || !strings.Contains(m.errMsg, "access view") {
		t.Fatalf("errMsg=%q", m.errMsg)
	}
	// A real outage still shows the reconnect banner.
	mi, _ = m.Update(objectsMsg{err: errors.New("connection refused")})
	m = asModel(t, mi)
	if !m.disconnected {
		t.Fatal("network errors must keep the retry behavior")
	}
}
