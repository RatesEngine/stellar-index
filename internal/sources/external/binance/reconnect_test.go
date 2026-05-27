package binance

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// F-0029: NewStreamer's default backoff is 5 s — large enough to
// avoid hammering Binance on a venue-wide outage, small enough that
// the per-cycle data-loss window is ~5 s on a healthy connection.
// Pre-fix it was 1 s (defaults) but with no reset path, so in
// production the running value drifted to MaxBackoff (60 s) and
// stayed there. The defaults change + healthy-connection reset in
// run() are the actual fix; this test pins the defaults so a future
// drive-by edit can't silently regress them.
func TestNewStreamer_DefaultInitialBackoffIs5s(t *testing.T) {
	s := NewStreamer(nil)
	if got, want := s.InitialBackoff, 5*time.Second; got != want {
		t.Errorf("InitialBackoff = %v, want %v (F-0029)", got, want)
	}
	if got, want := s.MaxBackoff, 60*time.Second; got != want {
		t.Errorf("MaxBackoff = %v, want %v", got, want)
	}
}

// TestClassifyDisconnect_BoundedReasonLabels — keeps the metric's
// label cardinality bounded. Add to this table when adding a new
// reason; that's the operator contract.
func TestClassifyDisconnect_BoundedReasonLabels(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "other"},
		{"reset", errors.New("read: failed to read frame payload: read tcp 1.2.3.4:443: read: connection reset by peer"), "reset"},
		{"broken_pipe", errors.New("write: broken pipe"), "broken_pipe"},
		{"timeout", errors.New("read: i/o timeout"), "timeout"},
		{"dial", errors.New("dial: lookup stream.binance.com: no such host"), "dial"},
		{"other", errors.New("EOF"), "other"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := classifyDisconnect(tc.err); got != tc.want {
				t.Errorf("classifyDisconnect(%v) = %q, want %q", tc.err, got, tc.want)
			}
		})
	}
}

// TestKeepAliveHTTPClient_HasKeepaliveDialer — the *http.Client we
// hand to websocket.Dial must have a Transport with a custom
// DialContext that sets TCP keepalive. Without this, dead TCP
// connections take Linux's default (~2h) to be detected, surfacing
// as "connection reset by peer" reads instead of being preempted
// in the dialer. F-0029.
func TestKeepAliveHTTPClient_HasKeepaliveDialer(t *testing.T) {
	c := keepAliveHTTPClient()
	if c == nil {
		t.Fatal("keepAliveHTTPClient returned nil")
	}
	tr, ok := c.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport type = %T, want *http.Transport", c.Transport)
	}
	if tr.DialContext == nil {
		t.Fatal("Transport.DialContext is nil — would fall back to no-keepalive default")
	}
}
