package v1

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/api/streaming"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// tipStreamProducerQueueDepth is the capacity of the per-connection
// channel between the producer goroutine and the SSE writer. 4 is
// enough for the writer to fall a tick or two behind without the
// producer blocking, while small enough that a wedged writer is
// detected by the producer (next channel send blocks → ctx cancel
// signals teardown).
const tipStreamProducerQueueDepth = 4

// handlePriceTipStream serves GET /v1/price/tip/stream — the SSE
// counterpart to /v1/price/tip per ADR-0018 §"SSE stream wires onto
// the tip surface" and the Wk-7 plan row L3.7.
//
// Wire shape per connection:
//
//   - Headers: Content-Type: text/event-stream + Cache-Control:
//     no-cache + X-Accel-Buffering: no (set by the streaming.Stream
//     scaffolding).
//   - Initial event: a tip_update emitted as soon as the first
//     compute completes (so the client doesn't sit on a heartbeat-only
//     stream when data is already available).
//   - Recurring events: every window_seconds (default 5, clamp 1–60),
//     a fresh tip computation runs and a tip_update event fires when
//     it succeeds. Failures (transient hypertable error, no data) are
//     logged and silently skipped — the client sees heartbeats keep
//     the connection alive until data returns.
//   - Heartbeats: every streaming.DefaultHeartbeatInterval (15 s) when
//     no real event has flowed.
//
// Pre-stream errors (param validation, "no data ever" 404) are
// returned as standard problem+json with the right HTTP status —
// after the stream body starts there's no way to set status, so
// failures must be detected pre-flight.
func (s *Server) handlePriceTipStream(w http.ResponseWriter, r *http.Request) {
	if s.prices == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-unavailable",
			"Price serving not configured", http.StatusServiceUnavailable,
			"this deployment has no PriceReader wired — check binary configuration")
		return
	}

	// URL-discipline rule: tip URL never accepts closed-bucket params.
	if r.URL.Query().Get("granularity") != "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-tip-param",
			"granularity is not valid on /v1/price/tip/stream", http.StatusBadRequest,
			"granularity is a closed-bucket concept (ADR-0018); /v1/price/tip and /v1/price/tip/stream do not have granularities")
		return
	}

	asset, quote, ok := s.parseTipAssetQuote(w, r)
	if !ok {
		return
	}
	window, ok := parseTipWindowSeconds(w, r)
	if !ok {
		return
	}

	// First synchronous compute — gives us a chance to return 404
	// before switching the response into SSE mode (where it's too
	// late to set a non-200 status code).
	first, firstSources, err := s.computeTip(r.Context(), asset, quote, window)
	if errors.Is(err, ErrPriceNotFound) {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/price-not-found",
			"No price data for pair", http.StatusNotFound,
			"no trades or oracle observations for "+asset.String()+" / "+quote.String())
		return
	}
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if IsCacheUnavailable(err) {
			s.logger.Warn("computeTip cache unavailable (stream prelude)",
				"err", err, "asset", asset.String(), "quote", quote.String())
			writeCacheUnavailableProblem(w, r)
			return
		}
		s.logger.Error("computeTip failed (stream prelude)",
			"err", err, "asset", asset.String(), "quote", quote.String())
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	// We have a valid first event + an open response. Switch to SSE.
	var gen streaming.Generator
	ch := make(chan streaming.Event, tipStreamProducerQueueDepth)
	prodCtx, cancelProd := context.WithCancel(r.Context())
	defer cancelProd()

	go s.runTipStreamProducer(prodCtx, ch, &gen, asset, quote, window, first, firstSources)

	streaming.StreamFromChannel(w, r, ch, streaming.StreamOptions{})
}

// runTipStreamProducer is the per-connection compute + push loop.
// Emits the pre-computed initial event, then ticks every
// `windowSeconds` recomputing the tip price. Failures are silently
// skipped (heartbeats keep the connection alive) — the assumption
// is that transient unavailability resolves itself and the next
// tick will succeed.
//
// The function returns when ctx cancels (client disconnect, request
// teardown) and closes ch on the way out so [streaming.StreamFromChannel]
// returns cleanly.
func (s *Server) runTipStreamProducer(
	ctx context.Context,
	ch chan<- streaming.Event,
	gen *streaming.Generator,
	asset, quote canonical.Asset,
	windowSeconds int,
	first PriceSnapshot,
	firstSources []string,
) {
	defer close(ch)

	if firstEv, ok := tipStreamEvent(gen, first, firstSources); ok {
		select {
		case <-ctx.Done():
			return
		case ch <- firstEv:
		}
	}

	ticker := time.NewTicker(time.Duration(windowSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			snap, sources, err := s.computeTip(ctx, asset, quote, windowSeconds)
			if err != nil {
				if ctx.Err() == nil {
					s.logger.Warn("computeTip failed (stream tick) — skipping emit",
						"err", err, "asset", asset.String(), "quote", quote.String())
				}
				continue
			}
			ev, ok := tipStreamEvent(gen, snap, sources)
			if !ok {
				continue
			}
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}
}

// tipStreamEvent builds the SSE event payload for one tip emission.
// Returns (_, false) on JSON-marshal failure (which would mean a
// programming error in PriceSnapshot — caller skips emit so the
// stream stays alive).
func tipStreamEvent(gen *streaming.Generator, snap PriceSnapshot, sources []string) (streaming.Event, bool) {
	payload := tipStreamPayload{
		Data:    snap,
		AsOf:    time.Now().UTC(),
		Sources: sources,
		Flags:   Flags{SingleSource: len(sources) == 1},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return streaming.Event{}, false
	}
	return streaming.Event{
		ID:   gen.Next(),
		Type: "tip_update",
		Data: body,
	}, true
}

// tipStreamPayload is the SSE-data shape — a flattened envelope
// matching the request endpoint's wire response. Keeping the shape
// identical means SDK consumers can use one type for both polling
// and streaming.
type tipStreamPayload struct {
	Data    PriceSnapshot `json:"data"`
	AsOf    time.Time     `json:"as_of"`
	Sources []string      `json:"sources,omitempty"`
	Flags   Flags         `json:"flags"`
}
