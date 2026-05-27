package bitstamp

import (
	"errors"
	"net/http"
	"testing"
	"time"
)

// F-0029: Bitstamp shares the same backoff drift as Binance — see
// the equivalent test in the binance package for full context.
func TestNewStreamer_DefaultInitialBackoffIs5s(t *testing.T) {
	s := NewStreamer(nil)
	if got, want := s.InitialBackoff, 5*time.Second; got != want {
		t.Errorf("InitialBackoff = %v, want %v (F-0029)", got, want)
	}
	if got, want := s.MaxBackoff, 60*time.Second; got != want {
		t.Errorf("MaxBackoff = %v, want %v", got, want)
	}
}

// TestClassifyDisconnect — covers the additional server_requested
// reason that bitstamp uses for the bts:request_reconnect path.
func TestClassifyDisconnect(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, "other"},
		{"server_requested", ErrRequestedReconnect, "server_requested"},
		{"reset", errors.New("read: ...: read: connection reset by peer"), "reset"},
		{"broken_pipe", errors.New("write: broken pipe"), "broken_pipe"},
		{"timeout", errors.New("read: i/o timeout"), "timeout"},
		{"dial", errors.New("dial: lookup ws.bitstamp.net: no such host"), "dial"},
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
		t.Fatal("Transport.DialContext is nil")
	}
}
