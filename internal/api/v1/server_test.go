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

	v1 "github.com/RatesEngine/rates-engine/internal/api/v1"
)

// stubCheck is a ReadyChecker that returns a configurable error.
type stubCheck struct {
	name string
	err  error
}

func (s *stubCheck) Ping(context.Context) error { return s.err }
func (s *stubCheck) Name() string               { return s.name }

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

	if resp.StatusCode != 200 {
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
	resp, _ := http.Get(ts.URL + "/v1/readyz")
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
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
	resp, _ := http.Get(ts.URL + "/v1/readyz")
	defer resp.Body.Close()

	if resp.StatusCode != 503 {
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

func TestVersion(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := http.Get(ts.URL + "/v1/version")
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
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
}

func TestUnknownRouteReturns404(t *testing.T) {
	ts := newTestServer(t)
	resp, _ := http.Get(ts.URL + "/v1/nonsense")
	defer resp.Body.Close()
	if resp.StatusCode != 404 {
		t.Errorf("status = %d, want 404", resp.StatusCode)
	}
}

func TestMethodMismatch(t *testing.T) {
	// /v1/healthz is GET-only; POST should be 405.
	ts := newTestServer(t)
	req, _ := http.NewRequest("POST", ts.URL+"/v1/healthz", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 405 {
		t.Errorf("status = %d, want 405 for POST /healthz", resp.StatusCode)
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
