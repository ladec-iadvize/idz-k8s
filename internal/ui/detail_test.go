package ui

import (
	"strings"
	"testing"

	"github.com/iadvize/idz-k8s/internal/config"
	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/model"
)

func TestDetailHidesManagedFields(t *testing.T) {
	pods := model.ResourceType{Version: "v1", Resource: "pods", Kind: "Pod", Namespaced: true}
	m := New(&kube.Client{Namespace: "demo"}, config.Defaults(), "", WithInitialType(pods))
	m.objects = []model.ResourceObject{{
		Namespace: "demo", Name: "web-1", Status: model.StatusSummary{Level: model.HealthOk},
		Raw: map[string]interface{}{
			"apiVersion": "apps/v1",
			"kind":       "Deployment",
			"metadata": map[string]interface{}{
				"name":      "web-1",
				"namespace": "demo",
				"annotations": map[string]interface{}{
					"app.iadvize.io/track":                             "main",
					"kubectl.kubernetes.io/last-applied-configuration": "{\"huge\":\"blob\"}",
				},
				"managedFields": []interface{}{
					map[string]interface{}{"manager": "helm", "fieldsV1": map[string]interface{}{"f:spec": map[string]interface{}{}}},
				},
			},
			"spec": map[string]interface{}{"replicas": int64(3)},
		},
	}}
	m.width, m.height = 100, 20
	m.layout()
	m.applyRows()
	m.openDetail()

	content := m.detail.View()
	if strings.Contains(content, "managedFields") {
		t.Error("detail must not show managedFields")
	}
	if strings.Contains(content, "f:spec") {
		t.Error("detail must not show the f: field-management tree")
	}
	if strings.Contains(content, "last-applied-configuration") {
		t.Error("detail must not show the verbose last-applied annotation")
	}
	// Useful content is still present.
	if !strings.Contains(content, "app.iadvize.io/track") {
		t.Error("real annotations should still be shown")
	}
}
