package streaming

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"time"
)

// DefaultHeartbeatInterval is the cadence at which Stream emits
// SSE comment heartbeats (`:keepalive\n\n`) when no real events are
// flowing. 15 s matches the api-design.md note and is well under
// the typical 60 s reverse-proxy idle timeout — which is what we're
// trying to dodge by sending these.
const DefaultHeartbeatInterval = 15 * time.Second

// StreamOptions tunes [Stream] behaviour. Zero values use sensible
// defaults so most callers can pass `StreamOptions{}`.
type StreamOptions struct {
	// HeartbeatInterval is the no-event cadence for SSE comment
	// heartbeats. Zero = DefaultHeartbeatInterval. Tests may want a
	// faster value to keep wall-clock test time short.
	HeartbeatInterval time.Duration
}

// Stream wires an http.ResponseWriter into the Hub for the supplied
// topics. It:
//
//  1. Sets the SSE-mandated response headers and disables proxy buffering.
//  2. Reads `Last-Event-ID` from the request (header takes
//     precedence over the `?last_event_id=` query param fallback)
//     and replays buffered events with greater IDs.
//  3. Forwards live events from the Hub as SSE frames until the
//     request context cancels.
//  4. Emits comment-only heartbeat frames at HeartbeatInterval to
//     keep proxies from idling out the connection.
//
// Stream is the convenience constructor for Hub-driven endpoints
// (the closed-bucket /v1/price/stream). Per-connection-tick
// endpoints (/v1/price/tip/stream, /v1/observations/stream) bypass
// the Hub and feed events through [StreamFromChannel] directly.
func Stream(w http.ResponseWriter, r *http.Request, hub *Hub, topics []string, opts StreamOptions) {
	ch, cancel := hub.Subscribe(topics, LastEventIDFrom(r))
	defer cancel()
	StreamFromChannel(w, r, ch, opts)
}

// StreamFromChannel is the lower-level SSE writer: given any
// receive-only event channel, write headers, run the heartbeat-aware
// event loop, and return when the request context cancels or `ch`
// closes. Pair this with a per-connection producer goroutine to
// build endpoints whose events are computed on a tick rather than
// fanned out from a Hub.
//
// The caller is responsible for closing `ch` to signal "no more
// events"; closing terminates the stream cleanly.
func StreamFromChannel(w http.ResponseWriter, r *http.Request, ch <-chan Event, opts StreamOptions) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	// SSE headers per WHATWG. Setting these BEFORE WriteHeader so
	// the first frame goes out cleanly. X-Accel-Buffering disables
	// nginx response buffering; Connection: keep-alive is implicit
	// in HTTP/1.1 and harmless on HTTP/2.
	h := w.Header()
	h.Set("Content-Type", "text/event-stream")
	h.Set("Cache-Control", "no-cache")
	h.Set("Connection", "keep-alive")
	h.Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	heartbeat := opts.HeartbeatInterval
	if heartbeat <= 0 {
		heartbeat = DefaultHeartbeatInterval
	}

	ctx := r.Context()
	ticker := time.NewTicker(heartbeat)
	defer ticker.Stop()

	// Initial flush so the client sees the response start
	// immediately rather than waiting for the first event. Some
	// clients deadlock if the server hasn't written headers + flushed
	// before they time out.
	if _, err := fmt.Fprint(w, ":connected\n\n"); err != nil {
		return
	}
	flusher.Flush()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				// Channel closed → producer signalled done. Return
				// cleanly; client reconnects with Last-Event-ID for
				// resume.
				return
			}
			if err := WriteFrame(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := fmt.Fprint(w, ":keepalive\n\n"); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// LastEventIDFrom returns the resume cursor from the request:
// header `Last-Event-ID` per the WHATWG SSE spec, or
// `?last_event_id=` as a fallback for clients that can't set custom
// headers (notably the EventSource API in browsers — it auto-sends
// the header on reconnect, but the *initial* connection may need
// the query-param form for resume across page reloads).
//
// Exported so non-Hub endpoints can consult it themselves (e.g. to
// log resumption events or skip stale state on reconnect).
func LastEventIDFrom(r *http.Request) string {
	if v := r.Header.Get("Last-Event-ID"); v != "" {
		return v
	}
	return r.URL.Query().Get("last_event_id")
}

// WriteFrame emits one SSE frame to w:
//
//	id: <ID>
//	event: <Type>      (omitted when Type == "")
//	data: <line 1>
//	data: <line 2>     (one per \n in Data)
//	\n
//
// Each `data:` line ends with \n per the SSE spec; the trailing \n
// separates the frame from the next.
//
// WriteFrame does NOT flush the underlying writer; Stream and
// StreamFromChannel call Flush() after each successful WriteFrame.
// Direct callers (custom event loops) are responsible for flushing.
func WriteFrame(w http.ResponseWriter, ev Event) error {
	var b strings.Builder
	b.Grow(len(ev.Data) + 64)
	if ev.ID != "" {
		b.WriteString("id: ")
		b.WriteString(ev.ID)
		b.WriteByte('\n')
	}
	if ev.Type != "" {
		b.WriteString("event: ")
		b.WriteString(ev.Type)
		b.WriteByte('\n')
	}
	if len(ev.Data) == 0 {
		b.WriteString("data:\n")
	} else {
		for _, line := range strings.Split(string(ev.Data), "\n") {
			b.WriteString("data: ")
			b.WriteString(line)
			b.WriteByte('\n')
		}
	}
	b.WriteByte('\n')
	_, err := w.Write([]byte(b.String()))
	return err
}

// Compile-time assertion that ResponseWriters returned by
// httptest.NewRecorder satisfy http.Flusher (they don't, by
// default — that's why server tests use httptest.NewServer +
// http.Get instead of recorders for Stream).
var _ = func() context.Context { return context.Background() }()
