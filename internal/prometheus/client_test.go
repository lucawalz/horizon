package prometheus_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	promapi "github.com/prometheus/client_golang/api"
	v1 "github.com/prometheus/client_golang/api/prometheus/v1"

	"github.com/lucawalz/horizon/internal/prometheus"
)

func newTestClient(t *testing.T, handler http.Handler) *prometheus.Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	apiClient, err := promapi.NewClient(promapi.Config{Address: srv.URL})
	if err != nil {
		t.Fatal(err)
	}
	return prometheus.NewClientFromAPI(v1.NewAPI(apiClient))
}

func TestQueryInstant(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := map[string]interface{}{
			"status": "success",
			"data": map[string]interface{}{
				"resultType": "vector",
				"result": []map[string]interface{}{
					{
						"metric": map[string]string{"instance": "192.168.2.191:9100"},
						"value":  []interface{}{float64(time.Now().Unix()), "0.114"},
					},
				},
			},
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(resp)
	})
	c := newTestClient(t, handler)
	vec, err := c.QueryInstant(context.Background(), `1 - avg by (instance)(rate(node_cpu_seconds_total{mode="idle"}[5m]))`)
	if err != nil {
		t.Fatalf("QueryInstant() error: %v", err)
	}
	if len(vec) != 1 {
		t.Fatalf("expected 1 result, got %d", len(vec))
	}
	if float64(vec[0].Value) < 0 || float64(vec[0].Value) > 1 {
		t.Errorf("unexpected value %v, expected in [0,1]", vec[0].Value)
	}
}

func TestQueryInstantError(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "internal error", http.StatusInternalServerError)
	})
	c := newTestClient(t, handler)
	_, err := c.QueryInstant(context.Background(), "up")
	if err == nil {
		t.Fatal("expected error from 500 response, got nil")
	}
}
