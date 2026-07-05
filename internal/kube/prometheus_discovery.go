package kube

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/rest"
)

// PrometheusRef locates a Prometheus service inside the cluster.
type PrometheusRef struct {
	Namespace string
	Name      string
	Port      int
}

// RESTConfig exposes the REST config so the metrics layer can reach in-cluster
// services through the API server proxy using the same credentials.
func (c *Client) RESTConfig() *rest.Config { return c.restConfig }

// DiscoverPrometheus finds the most likely Prometheus service in the cluster
// (read-only). Returns ok=false when none looks like Prometheus. This lets the
// tool link to each cluster's own Prometheus autonomously (dev/prod/shared)
// without a manual --prometheus-url.
func (c *Client) DiscoverPrometheus(ctx context.Context) (PrometheusRef, bool, error) {
	svcs, err := c.Clientset.CoreV1().Services(metav1.NamespaceAll).List(ctx, metav1.ListOptions{})
	if err != nil {
		return PrometheusRef{}, false, err
	}
	best := 0
	var ref PrometheusRef
	for i := range svcs.Items {
		s := &svcs.Items[i]
		score := prometheusScore(s.Name, s.Labels)
		if score <= 0 {
			continue
		}
		if score > best {
			best = score
			ref = PrometheusRef{Namespace: s.Namespace, Name: s.Name, Port: pickPrometheusPort(s.Spec.Ports)}
		}
	}
	if best == 0 {
		return PrometheusRef{}, false, nil
	}
	return ref, true, nil
}

// prometheusScore ranks a service as a Prometheus candidate by name/labels.
// Non-candidates (alertmanager, node-exporter, pushgateway, operator) are
// excluded with a negative score.
func prometheusScore(name string, labels map[string]string) int {
	lname := strings.ToLower(name)
	for _, bad := range []string{"alertmanager", "node-exporter", "pushgateway", "operator", "operated-alertmanager"} {
		if strings.Contains(lname, bad) {
			return -1
		}
	}
	score := 0
	switch labels["app.kubernetes.io/name"] {
	case "prometheus":
		score += 10
	}
	if labels["app"] == "prometheus" {
		score += 8
	}
	if strings.Contains(lname, "prometheus") {
		score += 5
	}
	switch lname {
	case "prometheus-server", "prometheus-operated", "prometheus-k8s", "kube-prometheus-stack-prometheus":
		score += 3
	}
	return score
}

// pickPrometheusPort selects the port to query: a 9090 port, or one named
// web/http-web/http, else the first port, defaulting to 9090.
func pickPrometheusPort(ports []corev1.ServicePort) int {
	for _, p := range ports {
		if p.Port == 9090 {
			return 9090
		}
	}
	for _, p := range ports {
		switch strings.ToLower(p.Name) {
		case "web", "http-web", "http":
			return int(p.Port)
		}
	}
	if len(ports) > 0 {
		return int(ports[0].Port)
	}
	return 9090
}
