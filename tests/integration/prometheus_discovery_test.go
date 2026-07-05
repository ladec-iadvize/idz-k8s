package integration

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	k8sfake "k8s.io/client-go/kubernetes/fake"

	"github.com/iadvize/idz-k8s/internal/kube"
	"github.com/iadvize/idz-k8s/internal/metrics"
)

func svc(ns, name string, labels map[string]string, ports ...int32) *corev1.Service {
	sp := make([]corev1.ServicePort, 0, len(ports))
	for _, p := range ports {
		sp = append(sp, corev1.ServicePort{Port: p})
	}
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name, Labels: labels},
		Spec:       corev1.ServiceSpec{Ports: sp},
	}
}

func TestProxyAddress(t *testing.T) {
	got := metrics.ProxyAddress("https://api.eks.example:443/", "monitoring", "prometheus-server", 9090)
	want := "https://api.eks.example:443/api/v1/namespaces/monitoring/services/http:prometheus-server:9090/proxy"
	if got != want {
		t.Fatalf("ProxyAddress:\n got  %q\n want %q", got, want)
	}
}

func TestDiscoverPrometheusPicksBestCandidate(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(
		svc("monitoring", "alertmanager", map[string]string{"app.kubernetes.io/name": "alertmanager"}, 9093),
		svc("monitoring", "prometheus-server", map[string]string{"app.kubernetes.io/name": "prometheus"}, 9090),
		svc("monitoring", "node-exporter", map[string]string{"app.kubernetes.io/name": "node-exporter"}, 9100),
		svc("default", "my-app", map[string]string{"app": "web"}, 80),
	)
	client := &kube.Client{Clientset: cs}

	ref, ok, err := client.DiscoverPrometheus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ok {
		t.Fatal("should have found a Prometheus service")
	}
	if ref.Namespace != "monitoring" || ref.Name != "prometheus-server" || ref.Port != 9090 {
		t.Fatalf("unexpected ref: %+v", ref)
	}
}

func TestDiscoverPrometheusNoneFound(t *testing.T) {
	cs := k8sfake.NewSimpleClientset(
		svc("default", "web", map[string]string{"app": "web"}, 80),
		svc("monitoring", "alertmanager", map[string]string{"app.kubernetes.io/name": "alertmanager"}, 9093),
	)
	client := &kube.Client{Clientset: cs}

	_, ok, err := client.DiscoverPrometheus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ok {
		t.Fatal("no Prometheus should be found among unrelated services")
	}
}
