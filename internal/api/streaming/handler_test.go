package streaming_test

import (
	"bufio"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/api/streaming"
)

// readSSEFrames blocks until `want` complete frames have been read
// from the response, or `timeout` elapses. A "frame" here is one
// non-comment SSE event terminated by a blank line. Returns the
// raw frame text (without the trailing blank line) so tests can
// assert on `id:`/`event:`/`data:` directly.
func readSSEFrames(t *testing.T, body io.Reader, want int, timeout time.Duration) []string {
	t.Helper()
	br := bufio.NewReader(body)
	frames := make([]string, 0, want)
	deadline := time.Now().Add(timeout)
	var current strings.Builder
	for time.Now().Before(deadline) && len(frames) < want {
		// Use SetReadDeadline-equivalent: rely on the test killing
		// the underlying connection at timeout. ReadString here is
		// blocking but the httptest.Server cleanup handles teardown.
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		// SSE comment line — keepalive or :connected — skipped.
		if strings.HasPrefix(line, ":") {
			continue
		}
		if line == "\n" {
			if current.Len() > 0 {
				frames = append(frames, current.String())
				current.Reset()
			}
			continue
		}
		current.WriteString(line)
	}
	return frames
}

// TestStream_DeliversLiveEvent — start a stream handler, publish
// one event, observe it on the wire as an SSE frame.
func TestStream_DeliversLiveEvent(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streaming.Stream(w, r, hub, []string{"topic"}, streaming.StreamOptions{
			HeartbeatInterval: 5 * time.Second,
		})
	}))
	defer srv.Close()

	// Subscribe via HTTP first (so the connection is established
	// and ready for live events), then publish.
	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Accept", "text/event-stream")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	if got := resp.Header.Get("Content-Type"); got != "text/event-stream" {
		t.Errorf("Content-Type = %q, want text/event-stream", got)
	}

	// Allow the handler's Subscribe to land before publishing.
	// Without this, hub.Publish can race with the subscribe-then-
	// listen path and the event slips through to the buffer alone.
	time.Sleep(50 * time.Millisecond)

	hub.Publish("topic", "price_update", []byte(`{"p":"0.12"}`))

	frames := readSSEFrames(t, resp.Body, 1, 2*time.Second)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1", len(frames))
	}
	frame := frames[0]
	if !strings.Contains(frame, "id: ") {
		t.Errorf("frame missing id: %q", frame)
	}
	if !strings.Contains(frame, "event: price_update\n") {
		t.Errorf("frame missing event line: %q", frame)
	}
	if !strings.Contains(frame, `data: {"p":"0.12"}`) {
		t.Errorf("frame missing data: %q", frame)
	}
}

// TestStream_HeartbeatFires — with a fast heartbeat interval and no
// events, the handler still writes keepalive comments so the
// connection stays alive across reverse proxies.
func TestStream_HeartbeatFires(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streaming.Stream(w, r, hub, []string{"topic"}, streaming.StreamOptions{
			HeartbeatInterval: 100 * time.Millisecond,
		})
	}))
	defer srv.Close()

	resp, err := srv.Client().Get(srv.URL)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	br := bufio.NewReader(resp.Body)
	keepaliveCount := 0
	deadline := time.Now().Add(750 * time.Millisecond)
	for time.Now().Before(deadline) {
		line, err := br.ReadString('\n')
		if err != nil {
			break
		}
		if strings.HasPrefix(line, ":keepalive") {
			keepaliveCount++
			if keepaliveCount >= 3 {
				return // got the heartbeats we expected
			}
		}
	}
	if keepaliveCount < 3 {
		t.Errorf("heartbeats observed in 750ms = %d, want at least 3 at 100ms cadence", keepaliveCount)
	}
}

// TestStream_LastEventIDHeaderResume — set Last-Event-ID on the
// request and observe replay of buffered events with greater IDs.
func TestStream_LastEventIDHeaderResume(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streaming.Stream(w, r, hub, []string{"topic"}, streaming.StreamOptions{
			HeartbeatInterval: 5 * time.Second,
		})
	}))
	defer srv.Close()

	id1 := hub.Publish("topic", "x", []byte("first"))
	hub.Publish("topic", "x", []byte("second"))
	hub.Publish("topic", "x", []byte("third"))

	req, _ := http.NewRequest(http.MethodGet, srv.URL, nil)
	req.Header.Set("Last-Event-ID", id1) // resume after first
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp.Body, 2, 2*time.Second)
	if len(frames) != 2 {
		t.Fatalf("got %d frames, want 2 (replay of second + third)", len(frames))
	}
	if !strings.Contains(frames[0], "data: second") {
		t.Errorf("first replayed frame = %q, want second", frames[0])
	}
	if !strings.Contains(frames[1], "data: third") {
		t.Errorf("second replayed frame = %q, want third", frames[1])
	}
}

// TestStream_LastEventIDQueryFallback — clients that can't set the
// Last-Event-ID header (some browser EventSource bootstraps) can
// pass it as ?last_event_id= instead.
func TestStream_LastEventIDQueryFallback(t *testing.T) {
	hub := streaming.NewHub(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streaming.Stream(w, r, hub, []string{"topic"}, streaming.StreamOptions{
			HeartbeatInterval: 5 * time.Second,
		})
	}))
	defer srv.Close()

	id1 := hub.Publish("topic", "x", []byte("first"))
	hub.Publish("topic", "x", []byte("second"))

	resp, err := srv.Client().Get(srv.URL + "?last_event_id=" + id1)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}
	defer resp.Body.Close()

	frames := readSSEFrames(t, resp.Body, 1, 2*time.Second)
	if len(frames) != 1 {
		t.Fatalf("got %d frames, want 1 (replay of second)", len(frames))
	}
	if !strings.Contains(frames[0], "data: second") {
		t.Errorf("frame = %q, want second", frames[0])
	}
}

// TestStream_ContextCancelEndsStream — when the request context
// cancels (client disconnect), Stream returns promptly. Probed by
// closing the request and asserting the server-side handler
// completes.
func TestStream_ContextCancelEndsStream(t *testing.T) {
	hub := streaming.NewHub(0)
	done := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		streaming.Stream(w, r, hub, []string{"topic"}, streaming.StreamOptions{
			HeartbeatInterval: 5 * time.Second,
		})
		close(done)
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL, nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("GET stream: %v", err)
	}

	// Cancel the client request → server-side ctx fires → handler
	// returns. Must complete inside a generous window.
	cancel()
	_ = resp.Body.Close()

	select {
	case <-done:
		// handler returned
	case <-time.After(2 * time.Second):
		t.Error("Stream did not return after client disconnect")
	}
}
