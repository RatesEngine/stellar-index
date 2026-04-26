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
// 499) vs. a genuine internal error (write 500). The three
// branches this test pins are:
//
//   - err is context.Canceled        → true
//   - err is context.DeadlineExceeded → true (errors.Is chain)
//   - err is something else, but the request's own context is
//     done                            → true (downstream wrapped)
//   - none of the above                → false
//
// Without these tests, a regression that dropped the errors.Is
// chain or the context-done check would silently turn 499s into
// 500s — pollute the api-5xx alert.

func TestClientAborted_directContextCanceled(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	if !clientAborted(r, context.Canceled) {
		t.Error("clientAborted(context.Canceled) = false, want true")
	}
}

func TestClientAborted_directDeadlineExceeded(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	if !clientAborted(r, context.DeadlineExceeded) {
		t.Error("clientAborted(context.DeadlineExceeded) = false, want true")
	}
}

func TestClientAborted_wrappedContextCanceled(t *testing.T) {
	wrapped := fmt.Errorf("storage: %w", context.Canceled)
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	if !clientAborted(r, wrapped) {
		t.Error("clientAborted(wrapped Canceled) = false, want true")
	}
}

func TestClientAborted_unrelatedError_falseWhenCtxAlive(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	if clientAborted(r, errors.New("disk full")) {
		t.Error("clientAborted(unrelated err, alive ctx) = true, want false")
	}
}

func TestClientAborted_unrelatedError_butCtxDoneViaRequest(t *testing.T) {
	// The request's own context has been cancelled — even an
	// unrelated reader error counts as client-aborted because the
	// downstream may have wrapped the cancel in a custom error
	// type that doesn't satisfy errors.Is(context.Canceled).
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)
	if !clientAborted(r, errors.New("downstream wrapped")) {
		t.Error("clientAborted(any err, cancelled ctx) = false, want true")
	}
}

func TestClientAborted_nilErrorButCtxDone(t *testing.T) {
	// Edge case: err nil, but ctx is done. Some readers return
	// (nil, nil) on cancellation rather than the ctx error — the
	// fallback ctx.Err() check catches them.
	r := httptest.NewRequest(http.MethodGet, "/v1/price", nil)
	ctx, cancel := context.WithCancel(r.Context())
	cancel()
	r = r.WithContext(ctx)
	if !clientAborted(r, nil) {
		t.Error("clientAborted(nil err, cancelled ctx) = false, want true")
	}
}
