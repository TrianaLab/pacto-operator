package prober

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProbe_Healthy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/health")

	if !result.Reachable {
		t.Fatalf("expected reachable, got error: %s", result.Error)
	}
	if result.StatusCode != 200 {
		t.Errorf("expected status 200, got %d", result.StatusCode)
	}
	if !result.ContentPresent {
		t.Error("expected content present")
	}
	if result.LatencyMs < 0 {
		t.Error("expected non-negative latency")
	}
}

func TestProbe_ConnectionRefused(t *testing.T) {
	p := New(1 * time.Second)
	result := p.Probe(context.Background(), "http://127.0.0.1:1/nonexistent")

	if result.Reachable {
		t.Fatal("expected unreachable")
	}
	if result.Error == "" {
		t.Error("expected error message")
	}
	if result.StatusCode != 0 {
		t.Errorf("expected status 0, got %d", result.StatusCode)
	}
}

func TestProbe_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/metrics")

	if !result.Reachable {
		t.Fatalf("expected reachable, got error: %s", result.Error)
	}
	if result.ContentPresent {
		t.Error("expected empty content")
	}
}

func TestProbe_PrometheusLike(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("# HELP http_requests_total Total requests\n# TYPE http_requests_total counter\nhttp_requests_total 42\n"))
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/metrics")

	if !result.PrometheusLike {
		t.Error("expected PrometheusLike=true for response with # HELP and # TYPE")
	}
}

func TestProbe_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("error"))
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/health")

	if !result.Reachable {
		t.Fatal("expected reachable (server responded)")
	}
	if result.StatusCode != 500 {
		t.Errorf("expected status 500, got %d", result.StatusCode)
	}
}

func TestProbe_Redirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/health-check", http.StatusMovedPermanently)
	}))
	defer srv.Close()

	p := New(2 * time.Second)
	result := p.Probe(context.Background(), srv.URL+"/health")

	if !result.Reachable {
		t.Fatal("expected reachable")
	}
	if result.StatusCode != 301 {
		t.Errorf("expected status 301, got %d", result.StatusCode)
	}
}

func TestBuildURL(t *testing.T) {
	tests := []struct {
		svc, ns  string
		port     int32
		path     string
		expected string
	}{
		{"api", "default", 8080, "/health", "http://api.default.svc:8080/health"},
		{"api", "prod", 9090, "/metrics", "http://api.prod.svc:9090/metrics"},
		{"api", "default", 8080, "health", "http://api.default.svc:8080/health"},
	}
	for _, tt := range tests {
		got := BuildURL(tt.svc, tt.ns, tt.port, tt.path)
		if got != tt.expected {
			t.Errorf("BuildURL(%q, %q, %d, %q) = %q, want %q", tt.svc, tt.ns, tt.port, tt.path, got, tt.expected)
		}
	}
}
