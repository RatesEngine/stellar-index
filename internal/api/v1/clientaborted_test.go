package v1

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

// clientAborted is the helper handlers use to decide whether a
// reader-returned error came from a cancelled client request
// (return without writing — let HTTPMetrics label the request
// 499) vs. a genuine internal error (write 500) or a server-side
// deadline (write 503).
//
// Decision rule: r.Context().Err() != nil is the ONLY signal that
// means "client gone." A bare context.DeadlineExceeded while the
// request context is still alive is a server-side WithTimeout
// guard firing (cold-path protection in #1082, #1099-#1105) and
// must NOT be treated as a client abort — the handler needs to
// flow through to its 503 timeout-response branch.
//
// The four cases this test pins:
//
//   - err is context.Canceled, request ctx alive → false
//     (server-side cancel, e.g. internal context.WithCancel; not
//     a client abort)
//   - err is context.DeadlineExceeded, request ctx alive → false
//     (server-side WithTimeout fired; handler should write 503)
//   - request ctx done (any err)                  → true
//   - none of the above                           → false
//
// Without these tests, a regression that re-added the errors.Is
// chain would silently swallow server-side timeouts and leave the
// client hanging without a response.

func TestClientAborted_directContextCanceled_aliveReqCtx(t *testing.T) {
	// A bare context.Canceled with the request context still alive
	// is a server-side cancel, not a client abort. The handler
	// should keep going (and either write 500 or, if it knows the
	// cancel was timer-driven, structure its own response path).
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	if clientAborted(r, context.Canceled) {
		t.Error("clientAborted(context.Canceled, alive ctx) = true, want false")
	}
}

func TestClientAborted_directDeadlineExceeded_aliveReqCtx(t *testing.T) {
	// THE bug fix: a bare context.DeadlineExceeded with the request
	// context still alive comes from one of our cold-path
	// context.WithTimeout(8s) guards (#1082, #1099-#1105). Returning
	// true here would short-circuit the handler before its 503
	// timeout-response branch fires — the client would get an empty
	// body instead of a structured problem+json.
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	if clientAborted(r, context.DeadlineExceeded) {
		t.Error("clientAborted(context.DeadlineExceeded, alive ctx) = true, want false (server-side deadline must flow to 503 path)")
	}
}

func TestClientAborted_wrappedContextCanceled_aliveReqCtx(t *testing.T) {
	// Wrapping doesn't change the analysis: if r.Context() is alive,
	// the cancel was server-internal, not a client abort.
	wrapped := fmt.Errorf("storage: %w", context.Canceled)
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	if clientAborted(r, wrapped) {
		t.Error("clientAborted(wrapped Canceled, alive ctx) = true, want false")
	}
}

func TestClientAborted_unrelatedError_falseWhenCtxAlive(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	if clientAborted(r, errors.New("disk full")) {
		t.Error("clientAborted(unrelated err, alive ctx) = true, want false")
	}
}

func TestClientAborted_unrelatedError_butCtxDoneViaRequest(t *testing.T) {
	// The request's own context has been cancelled — that's the
	// authoritative "client gone" signal regardless of what the
	// reader returned (downstream may have wrapped the cancel in a
	// custom error type that doesn't satisfy errors.Is).
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)
	if !clientAborted(r, errors.New("downstream wrapped")) {
		t.Error("clientAborted(any err, cancelled req ctx) = false, want true")
	}
}

func TestClientAborted_nilErrorButCtxDone(t *testing.T) {
	// Edge case: err nil, but req ctx is done. Some readers return
	// (nil, nil) on cancellation rather than the ctx error — the
	// req-ctx check catches them.
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)
	if !clientAborted(r, nil) {
		t.Error("clientAborted(nil err, cancelled req ctx) = false, want true")
	}
}

func TestClientAborted_canceledErr_butReqCtxDone_returnsTrue(t *testing.T) {
	// Race case: req ctx and the reader's err both indicate cancel.
	// The req-ctx check is sufficient, regardless of err.
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)
	if !clientAborted(r, context.Canceled) {
		t.Error("clientAborted(Canceled, cancelled req ctx) = false, want true")
	}
}
