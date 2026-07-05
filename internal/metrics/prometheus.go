// Package metrics is the read-only Prometheus access layer — the single metrics
// source for instantaneous usage and the rolling last-1-hour history (research
// D5). When no endpoint is configured or Prometheus is unreachable, queries
// report "unavailable" instead of fabricating values (FR-021).
package metrics

import (
	"context"
	"fmt"
	"strings"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	promv1 "github.com/prometheus/client_golang/api/prometheus/v1"
	prommodel "github.com/prometheus/common/model"
	"k8s.io/client-go/rest"

	"github.com/iadvize/idz-k8s/internal/model"
)

// TrendWindow is the rolling history window for trend charts (clarified: 1h).
const TrendWindow = time.Hour

// Client wraps the Prometheus HTTP API. A Client with enabled=false (no URL)
// makes every query report unavailable without erroring.
type Client struct {
	api     promv1.API
	enabled bool
}

// NewClient builds a Prometheus client for an explicit endpoint URL. An empty
// URL yields a disabled client — the app then auto-discovers the cluster's
// Prometheus (see NewViaProxy) instead. All queries report unavailable while
// disabled.
func NewClient(url string) (*Client, error) {
	if url == "" {
		return &Client{enabled: false}, nil
	}
	c, err := promapi.NewClient(promapi.Config{Address: url})
	if err != nil {
		return &Client{enabled: false}, err
	}
	return &Client{api: promv1.NewAPI(c), enabled: true}, nil
}

// ProxyAddress builds the API-server proxy URL for an in-cluster Prometheus
// service: <apiserver>/api/v1/namespaces/<ns>/services/http:<svc>:<port>/proxy.
// The Prometheus client appends its own /api/v1/query… below this.
func ProxyAddress(apiHost, namespace, service string, port int) string {
	return fmt.Sprintf("%s/api/v1/namespaces/%s/services/http:%s:%d/proxy",
		strings.TrimSuffix(apiHost, "/"), namespace, service, port)
}

// NewViaProxy builds a Prometheus client that reaches an in-cluster Prometheus
// through the Kubernetes API server proxy, reusing the kubeconfig credentials
// (works with EKS exec auth). No port-forward needed; each context's API server
// proxies to its own Prometheus, so this links dev/prod/shared autonomously.
func NewViaProxy(cfg *rest.Config, namespace, service string, port int) (*Client, error) {
	if cfg == nil {
		return &Client{enabled: false}, fmt.Errorf("nil rest config")
	}
	transport, err := rest.TransportFor(cfg)
	if err != nil {
		return &Client{enabled: false}, err
	}
	c, err := promapi.NewClient(promapi.Config{
		Address:      ProxyAddress(cfg.Host, namespace, service, port),
		RoundTripper: transport,
	})
	if err != nil {
		return &Client{enabled: false}, err
	}
	return &Client{api: promv1.NewAPI(c), enabled: true}, nil
}

// Enabled reports whether a Prometheus endpoint is configured.
func (c *Client) Enabled() bool { return c != nil && c.enabled }

// Available probes Prometheus with a cheap query. False on any error.
func (c *Client) Available(ctx context.Context) bool {
	if !c.Enabled() {
		return false
	}
	_, _, err := c.api.Query(ctx, "vector(1)", time.Now())
	return err == nil
}

// InstantScalar runs an instant query and returns the first sample's value.
// ok is false when disabled, on error, or when the result is empty.
func (c *Client) InstantScalar(ctx context.Context, query string) (value float64, ok bool) {
	if !c.Enabled() {
		return 0, false
	}
	res, _, err := c.api.Query(ctx, query, time.Now())
	if err != nil {
		return 0, false
	}
	vec, isVec := res.(prommodel.Vector)
	if !isVec || len(vec) == 0 {
		return 0, false
	}
	return float64(vec[0].Value), true
}

// Range runs a range query over the last `window` at the given step and returns
// the first series as model samples. Empty on disabled/error/no-data.
func (c *Client) Range(ctx context.Context, query string, window, step time.Duration) []model.MetricSample {
	if !c.Enabled() {
		return nil
	}
	end := time.Now()
	r := promv1.Range{Start: end.Add(-window), End: end, Step: step}
	res, _, err := c.api.QueryRange(ctx, query, r)
	if err != nil {
		return nil
	}
	mtx, isMtx := res.(prommodel.Matrix)
	if !isMtx || len(mtx) == 0 {
		return nil
	}
	stream := mtx[0]
	out := make([]model.MetricSample, 0, len(stream.Values))
	for _, p := range stream.Values {
		out = append(out, model.MetricSample{T: p.Timestamp.Time(), Value: float64(p.Value)})
	}
	return out
}

// TopN runs an instant query expected to return a vector labelled by namespace
// and pod, and maps it to TopConsumer rows.
func (c *Client) TopN(ctx context.Context, query string, kind model.MetricKind) []model.TopConsumer {
	if !c.Enabled() {
		return nil
	}
	res, _, err := c.api.Query(ctx, query, time.Now())
	if err != nil {
		return nil
	}
	vec, isVec := res.(prommodel.Vector)
	if !isVec {
		return nil
	}
	out := make([]model.TopConsumer, 0, len(vec))
	for _, s := range vec {
		out = append(out, model.TopConsumer{
			Namespace: string(s.Metric["namespace"]),
			Name:      string(s.Metric["pod"]),
			Kind:      kind,
			Value:     float64(s.Value),
		})
	}
	return out
}
