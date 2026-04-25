package v1_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
)

// stubCheck is a ReadyChecker that returns a configurable error.
// An optional `sleep` models a slow dependency so tests can verify
// the readyz handler runs probes in parallel.
type stubCheck struct {
	name  string
	err   error
	sleep time.Duration
}

func (s *stubCheck) Ping(ctx context.Context) error {
	if s.sleep > 0 {
		select {
		case <-time.After(s.sleep):
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	return s.err
}
func (s *stubCheck) Name() string { return s.name }

func newTestServer(t *testing.T, checks ...v1.ReadyChecker) *httptest.Server {
	t.Helper()
	srv := v1.New(v1.Options{ReadyChecks: checks})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)
	return ts
}

func TestHealthz(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q", ct)
	}

	var env struct {
		Data struct {
			Status string `json:"status"`
			Uptime string `json:"uptime"`
		} `json:"data"`
		Flags map[string]bool `json:"flags"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		t.Fatal(err)
	}
	if env.Data.Status != "ok" {
		t.Errorf("status = %q", env.Data.Status)
	}
	if env.Data.Uptime == "" {
		t.Error("uptime should be non-empty")
	}
}

func TestReadyz_AllChecksPass(t *testing.T) {
	ts := newTestServer(t,
		&stubCheck{name: "postgres"},
		&stubCheck{name: "redis"},
	)
	resp, err := http.Get(ts.URL + "/v1/readyz")
	if err != nil {
		t.Fatalf("GET /v1/readyz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var env struct {
		Data struct {
			Status string `json:"status"`
			Checks []struct {
				Name string `json:"name"`
				OK   bool   `json:"ok"`
			} `json:"checks"`
		} `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Data.Status != "ok" {
		t.Errorf("status = %q", env.Data.Status)
	}
	if len(env.Data.Checks) != 2 {
		t.Fatalf("expected 2 checks, got %d", len(env.Data.Checks))
	}
	for _, c := range env.Data.Checks {
		if !c.OK {
			t.Errorf("check %s should be OK", c.Name)
		}
	}
}

func TestReadyz_OneFailure(t *testing.T) {
	ts := newTestServer(t,
		&stubCheck{name: "postgres"},
		&stubCheck{name: "redis", err: errors.New("connection refused")},
	)
	resp, err := http.Get(ts.URL + "/v1/readyz")
	if err != nil {
		t.Fatalf("GET /v1/readyz: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := readAll(resp)
	if !strings.Contains(body, `"status":"degraded"`) {
		t.Errorf("body should report degraded: %s", body)
	}
	if !strings.Contains(body, "connection refused") {
		t.Errorf("body should include failing-check error: %s", body)
	}
	// Stale flag must be set when degraded.
	if !strings.Contains(body, `"stale":true`) {
		t.Errorf("body should set stale flag: %s", body)
	}
}

func TestReadyz_ProbesRunInParallel(t *testing.T) {
	// Three 400ms-sleep checks. Serial execution would take ~1.2s
	// (over the 2s budget). Parallel should land well under 1s.
	// Generous cap (900ms) so we don't flake on CPU-stressed CI.
	const perProbeSleep = 400 * time.Millisecond
	const elapsedCap = 900 * time.Millisecond

	ts := newTestServer(t,
		&stubCheck{name: "a", sleep: perProbeSleep},
		&stubCheck{name: "b", sleep: perProbeSleep},
		&stubCheck{name: "c", sleep: perProbeSleep},
	)

	start := time.Now()
	resp, err := http.Get(ts.URL + "/v1/readyz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	elapsed := time.Since(start)

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if elapsed >= elapsedCap {
		t.Errorf("readyz took %v — expected < %v (serial execution regression)", elapsed, elapsedCap)
	}
}

func TestVersion(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/version")
	if err != nil {
		t.Fatalf("GET /v1/version: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d", resp.StatusCode)
	}
	var env struct {
		Data map[string]string `json:"data"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&env)
	if env.Data["version"] == "" {
		t.Error("version should be non-empty")
	}
	if env.Data["build_date"] == "" {
		t.Error("build_date should be non-empty")
	}
	// Commit + Dirty come from runtime/debug.BuildInfo. Under
	// `go test` they're typically "unknown" (no -buildvcs in the
	// test invocation); under `go build -buildvcs=true` they're
	// populated. Either way the keys must be present + non-empty.
	if env.Data["commit"] == "" {
		t.Error("commit should be non-empty (defaults to 'unknown' if VCS unavailable)")
	}
	if env.Data["dirty"] == "" {
		t.Error("dirty should be non-empty")
	}
	if env.Data["go_version"] == "" {
		t.Error("go_version should be non-empty (runtime.Version())")
	}
}

func TestUnknownRouteReturns404(t *testing.T) {
	ts := newTestServer(t)
	resp, err := http.Get(ts.URL + "/v1/nonsense")
	if err != nil {
		t.Fatalf("GET /v1/nonsense: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestMethodMismatch(t *testing.T) {
	// /v1/healthz is GET-only; POST should be 405.
	ts := newTestServer(t)
	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("status = %d, want 405 for POST /healthz", resp.StatusCode)
	}
}

func TestMiddlewareStackAppliedEndToEnd(t *testing.T) {
	// Hit a real endpoint and assert the full stack fired:
	// RequestID preserves client values + mints when absent.
	ts := newTestServer(t)

	// 1. Client-supplied X-Request-ID is preserved verbatim.
	req, err := http.NewRequest(http.MethodGet, ts.URL+"/v1/healthz", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request-ID", "test-trace-abc")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if got := resp.Header.Get("X-Request-ID"); got != "test-trace-abc" {
		t.Errorf("client-supplied X-Request-ID not preserved: %q", got)
	}

	// 2. Absent header → middleware mints 32-char hex ID.
	resp2, err := http.Get(ts.URL + "/v1/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp2.Body.Close()
	if id := resp2.Header.Get("X-Request-ID"); len(id) != 32 {
		t.Errorf("minted X-Request-ID len = %d, want 32 hex chars", len(id))
	}
}

func readAll(resp *http.Response) (string, error) {
	b, err := ioReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Thin shim so the test file doesn't import io directly — keeps
// the imports list short + greppable.
var ioReadAll = func(r interface{ Read([]byte) (int, error) }) ([]byte, error) {
	var buf []byte
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf = append(buf, tmp[:n]...)
		}
		if err != nil {
			if err.Error() == "EOF" {
				return buf, nil
			}
			return buf, fmt.Errorf("read: %w", err)
		}
	}
}
