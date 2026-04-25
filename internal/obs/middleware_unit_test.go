package obs

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// Unit tests for the small helpers inside http_middleware.go.
// metrics_test.go covers HTTPMetrics end-to-end via httptest;
// this file exercises the helpers directly so regressions show
// up at the function level rather than as a metric-label
// surprise three layers deep.

// ─── normalizeMethod ──────────────────────────────────────────

func TestNormalizeMethod(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"GET", "GET"},
		{"get", "GET"},
		{"Post", "POST"},
		{"PATCH", "PATCH"},
		{"propfind", "propfind"}, // unknown verb passes through as-is
		{"PROPFIND", "PROPFIND"}, // already-upper unknown verb same
		{"", ""},
	}
	for _, tc := range cases {
		if got := normalizeMethod(tc.in); got != tc.want {
			t.Errorf("normalizeMethod(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─── routeFromPattern ─────────────────────────────────────────

func TestRouteFromPattern(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"GET /v1/price", "/v1/price"},
		{"POST /v1/price/batch", "/v1/price/batch"},
		{"/v1/healthz", "/v1/healthz"}, // pattern without method prefix
		{"", "unmatched"},              // empty → sentinel
		{"GET /", "/"},                 // root path
	}
	for _, tc := range cases {
		if got := routeFromPattern(tc.in); got != tc.want {
			t.Errorf("routeFromPattern(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// ─── statusRecorder.Flush ─────────────────────────────────────

// flushableRecorder is an httptest.ResponseRecorder with a Flush
// counter — proves statusRecorder.Flush proxies to the underlying
// writer when it implements http.Flusher.
type flushableRecorder struct {
	*httptest.ResponseRecorder
	flushed int
}

func (f *flushableRecorder) Flush() { f.flushed++ }

func TestStatusRecorder_Flush_proxiesWhenInnerSupportsFlusher(t *testing.T) {
	inner := &flushableRecorder{ResponseRecorder: httptest.NewRecorder()}
	r := &statusRecorder{ResponseWriter: inner}
	r.Flush()
	if inner.flushed != 1 {
		t.Errorf("inner.flushed = %d, want 1", inner.flushed)
	}
}

// nonFlusher is a writer that deliberately does NOT implement
// http.Flusher. statusRecorder.Flush must be a no-op rather than
// panicking — SSE endpoints sometimes get wrapped by middleware
// that strips the Flusher interface.
type nonFlusher struct {
	header http.Header
}

func (n *nonFlusher) Header() http.Header { return n.header }
func (n *nonFlusher) Write(b []byte) (int, error) {
	return len(b), nil
}
func (n *nonFlusher) WriteHeader(int) {}

func TestStatusRecorder_Flush_noopWhenInnerDoesntImplementFlusher(t *testing.T) {
	r := &statusRecorder{ResponseWriter: &nonFlusher{header: http.Header{}}}
	// Must not panic.
	r.Flush()
}
