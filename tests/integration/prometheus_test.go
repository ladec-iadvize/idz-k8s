package integration

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/iadvize/idz-k8s/internal/metrics"
	"github.com/iadvize/idz-k8s/internal/model"
)

// stubPrometheus returns a test server speaking the Prometheus HTTP API with
// canned vector/matrix responses (research D10, task T027).
func stubPrometheus(t *testing.T) *httptest.Server {
	t.Helper()
	const vector = `{"status":"success","data":{"resultType":"vector","result":[
		{"metric":{"namespace":"demo","pod":"web-1"},"value":[1700000000,"0.5"]},
		{"metric":{"namespace":"demo","pod":"web-2"},"value":[1700000000,"0.3"]}]}}`
	const matrix = `{"status":"success","data":{"resultType":"matrix","result":[
		{"metric":{"namespace":"demo","pod":"web-1"},"values":[[1700000000,"0.4"],[1700000060,"0.5"],[1700000120,"0.6"]]}]}}`
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/query", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(vector))
	})
	mux.HandleFunc("/api/v1/query_range", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(matrix))
	})
	return httptest.NewServer(mux)
}

func TestPrometheusInstantAndRange(t *testing.T) {
	srv := stubPrometheus(t)
	defer srv.Close()
	c, err := metrics.NewClient(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Enabled() {
		t.Fatal("client should be enabled with a URL")
	}
	if !c.Available(context.Background()) {
		t.Fatal("stub should report available")
	}

	v, ok := c.InstantScalar(context.Background(), "whatever")
	if !ok || v != 0.5 {
		t.Fatalf("instant query = %v ok=%v, want 0.5 true", v, ok)
	}

	series := c.Range(context.Background(), "whatever", time.Hour, time.Minute)
	if len(series) != 3 {
		t.Fatalf("range should return 3 samples, got %d", len(series))
	}
	if series[2].Value != 0.6 {
		t.Fatalf("last sample = %v, want 0.6", series[2].Value)
	}
}

func TestPrometheusTopN(t *testing.T) {
	srv := stubPrometheus(t)
	defer srv.Close()
	c, _ := metrics.NewClient(srv.URL)
	rows := c.TopN(context.Background(), metrics.ScopeNowByPod("", model.MetricCPU), model.MetricCPU)
	if len(rows) != 2 {
		t.Fatalf("expected 2 top rows, got %d", len(rows))
	}
	if rows[0].Namespace != "demo" || !strings.HasPrefix(rows[0].Name, "web-") {
		t.Fatalf("unexpected top row: %+v", rows[0])
	}
}

func TestPrometheusDisabledReportsUnavailable(t *testing.T) {
	c, err := metrics.NewClient("") // no endpoint
	if err != nil {
		t.Fatal(err)
	}
	if c.Enabled() {
		t.Fatal("empty URL must yield a disabled client")
	}
	if c.Available(context.Background()) {
		t.Fatal("disabled client must be unavailable")
	}
	if _, ok := c.InstantScalar(context.Background(), "x"); ok {
		t.Fatal("disabled client instant query must be not-ok")
	}
	if s := c.Range(context.Background(), "x", time.Hour, time.Minute); s != nil {
		t.Fatal("disabled client range must be nil")
	}
}
