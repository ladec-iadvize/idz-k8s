package ui

import (
	"path/filepath"
	"testing"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestPersistSavesLastSelections(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	m := New(&kube.Client{Namespace: "team-a"}, config.Defaults(), "",
		WithConfigPath(path),
		WithInitialType(model.ResourceType{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment"}),
	)
	m.persist()

	got, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.LastNamespace != "team-a" {
		t.Errorf("LastNamespace=%q want team-a", got.LastNamespace)
	}
	if got.LastType != "apps/v1/deployments" {
		t.Errorf("LastType=%q want apps/v1/deployments", got.LastType)
	}
}

func TestFindTypeByKey(t *testing.T) {
	types := []model.ResourceType{
		{Version: "v1", Resource: "pods", Kind: "Pod"},
		{Group: "apps", Version: "v1", Resource: "deployments", Kind: "Deployment"},
	}
	if _, ok := findTypeByKey(types, ""); ok {
		t.Error("empty key must not match")
	}
	if _, ok := findTypeByKey(types, "batch/v1/jobs"); ok {
		t.Error("unknown key must not match")
	}
	got, ok := findTypeByKey(types, "apps/v1/deployments")
	if !ok || got.Resource != "deployments" {
		t.Errorf("expected deployments, got %+v ok=%v", got, ok)
	}
}
