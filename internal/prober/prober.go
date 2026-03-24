/*
Copyright 2026.

Licensed under the MIT License.
See LICENSE file in the project root for full license text.
*/

// Package prober performs HTTP endpoint probing for health and metrics validation.
package prober

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTimeout = 5 * time.Second
	maxBodyRead    = 4096 // only read first 4KB for content checks
)

// Result describes the outcome of probing a single HTTP endpoint.
type Result struct {
	URL        string
	Reachable  bool
	StatusCode int32
	LatencyMs  int64
	Error      string

	// ContentPresent indicates whether the response body was non-empty.
	ContentPresent bool
	// PrometheusLike indicates whether the response body contains Prometheus-style markers.
	PrometheusLike bool
}

// Prober performs HTTP endpoint checks.
type Prober struct {
	client *http.Client
}

// New creates a Prober with the given timeout.
func New(timeout time.Duration) *Prober {
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Prober{
		client: &http.Client{
			Timeout: timeout,
			// Do not follow redirects — report them as-is
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}
}

// Probe performs an HTTP GET to the given URL and reports the result.
func (p *Prober) Probe(ctx context.Context, url string) Result {
	result := Result{URL: url}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		result.Error = fmt.Sprintf("invalid URL: %v", err)
		return result
	}

	start := time.Now()
	resp, err := p.client.Do(req)
	result.LatencyMs = time.Since(start).Milliseconds()

	if err != nil {
		result.Error = sanitizeError(err)
		return result
	}
	defer func() { _ = resp.Body.Close() }()

	result.Reachable = true
	result.StatusCode = int32(resp.StatusCode)

	// Read a small prefix of the body for content checks
	body, _ := io.ReadAll(io.LimitReader(resp.Body, maxBodyRead))
	result.ContentPresent = len(body) > 0

	if result.ContentPresent {
		bodyStr := string(body)
		result.PrometheusLike = strings.Contains(bodyStr, "# HELP") || strings.Contains(bodyStr, "# TYPE")
	}

	return result
}

// sanitizeError extracts a clean error message, stripping URL repetition from net/http errors.
func sanitizeError(err error) string {
	msg := err.Error()
	// net/http errors often include the full URL which is redundant since we store it separately
	if idx := strings.LastIndex(msg, ": "); idx > 0 {
		suffix := msg[idx+2:]
		if len(suffix) > 0 {
			return suffix
		}
	}
	return msg
}

// BuildURL constructs an in-cluster HTTP URL for a Kubernetes Service endpoint.
func BuildURL(serviceName, namespace string, port int32, path string) string {
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	return fmt.Sprintf("http://%s.%s.svc:%d%s", serviceName, namespace, port, path)
}
