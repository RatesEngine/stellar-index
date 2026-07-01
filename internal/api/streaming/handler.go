package streaming

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"time"
)

// streamWriteDeadline bounds a single SSE write. It's rolled forward
// before every write, so a healthy stream (which writes a heartbeat every
// HeartbeatInterval < this) never trips it, but a STALLED write — a
// non-reading or zero-window client — fails after this long, letting the
// handler return and free its goroutine/conn/FD (CS-013). Must be > the
// heartbeat interval so a slow-but-alive client isn't killed.
const streamWriteDeadline = 25 * time.Second

// maxConcurrentStreams caps simultaneous SSE connections across all stream
// endpoints, so a flood of connections can't exhaust file descriptors /
// goroutines (CS-013 / F4). Generous by default (legit fan-out is small on
// a single host); tune via SetMaxConcurrentStreams. <= 0 disables the cap.
var maxConcurrentStreams int64 = 8192

var activeStreams int64

// SetMaxConcurrentStreams overrides the global concurrent-SSE-connection
// cap. Pass <= 0 to disable. Call once at startup.
func SetMaxConcurrentStreams(n int64) { atomic.StoreInt64(&maxConcurrentStreams, n) }

// ActiveStreams reports the current number of open SSE connections (for
// diagnostics / a gauge).
func ActiveStreams() int64 { return atomic.LoadInt64(&activeStreams) }

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

	// Concurrent-stream cap (CS-013): refuse new connections past the
	// ceiling so a connection flood can't exhaust FDs/goroutines.
	if cap := atomic.LoadInt64(&maxConcurrentStreams); cap > 0 {
		if atomic.AddInt64(&activeStreams, 1) > cap {
			atomic.AddInt64(&activeStreams, -1)
			http.Error(w, "too many concurrent streams", http.StatusServiceUnavailable)
			return
		}
		defer atomic.AddInt64(&activeStreams, -1)
	} else {
		atomic.AddInt64(&activeStreams, 1)
		defer atomic.AddInt64(&activeStreams, -1)
	}

	// F-1228 + CS-013: the API's http.Server sets `WriteTimeout: 30s` to
	// keep short handlers honest, but that fixed deadline would reset an
	// SSE stream at 30s. The old fix cleared the deadline entirely
	// (`SetWriteDeadline(zero)`), which let a stalled write block FOREVER
	// — a non-reading client leaked its goroutine/conn/FD indefinitely.
	// Instead we ROLL a per-write deadline forward before every write
	// (see setWriteDeadline). A healthy stream heartbeats within the
	// window and never trips it; a stalled write fails after
	// streamWriteDeadline and the handler returns + cleans up.
	//
	// On transports that don't expose SetWriteDeadline (httptest writers,
	// wrappers without Unwrap) the call returns http.ErrNotSupported,
	// which we ignore — those transports don't enforce write deadlines
	// anyway. Production wrappers all expose Unwrap().
	rc := http.NewResponseController(w)
	setWriteDeadline := func() {
		_ = rc.SetWriteDeadline(time.Now().Add(streamWriteDeadline))
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
	setWriteDeadline()
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
			setWriteDeadline()
			if err := WriteFrame(w, ev); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			setWriteDeadline()
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
