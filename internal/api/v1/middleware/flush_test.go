package middleware_test

import (
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	mw "github.com/StellarAtlas/stellar-atlas/internal/api/v1/middleware"
)

// flushSpy is the test fixture: a ResponseWriter that also implements
// http.Flusher and records each Flush call. We hand it to the
// Logger middleware via httptest.NewRecorder() composition so we can
// observe whether the wrapper propagates Flush down to the underlying
// writer.
type flushSpy struct {
	http.ResponseWriter
	flushes int
}

func (f *flushSpy) Flush() { f.flushes++ }

// Logger's statusRecorder wraps the response writer and must
// preserve http.Flusher — without it, SSE endpoints (price stream,
// trades stream) silently buffer their output and break chunked
// streaming. This is the regression that motivated the explicit
// Flush method on the wrapper; pin it.
func TestLogger_PreservesFlush(t *testing.T) {
	logger := slog.New(slog.NewJSONHandler(&discardWriter{}, nil))

	var sawFlusher bool
	handler := mw.Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("inner handler did not see an http.Flusher — Logger broke the wrapping")
			return
		}
		sawFlusher = true
		f.Flush()
		f.Flush()
	}))

	spy := &flushSpy{ResponseWriter: httptest.NewRecorder()}
	req := httptest.NewRequest(http.MethodGet, "/v1/stream", nil)
	handler.ServeHTTP(spy, req)

	if !sawFlusher {
		t.Fatal("inner handler did not run / Flusher cast failed")
	}
	if spy.flushes != 2 {
		t.Errorf("flushSpy.flushes = %d, want 2 (each handler Flush should reach the wrapped writer)", spy.flushes)
	}
}

// discardWriter is a minimal io.Writer for slog. Avoids polluting
// test output with the structured log line.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }
