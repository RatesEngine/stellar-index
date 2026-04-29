package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/RatesEngine/rates-engine/internal/api/streaming"
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// Observations-stream tunables. interval_seconds is the per-connection
// tick cadence — independent of the tip surface's window_seconds (the
// raw-observations surface doesn't aggregate, so there's no window to
// pin).
const (
	defaultObservationsIntervalSeconds = 5
	minObservationsIntervalSeconds     = 1
	maxObservationsIntervalSeconds     = 60

	// observationsStreamProducerQueueDepth is the per-connection
	// channel capacity between the producer goroutine and the SSE
	// writer. 4 is enough for the writer to fall a tick behind without
	// the producer blocking.
	observationsStreamProducerQueueDepth = 4
)

// handleObservationsStream serves GET /v1/observations/stream — the
// SSE counterpart to /v1/observations per ADR-0018 §"SSE wires onto
// the tip surface" (the same wire model applies to the observations
// surface).
//
// Wire shape per connection:
//
//   - Initial event: emitted on connect with the current per-source
//     observations (or `[]` when the pair has no trades yet —
//     observations returns empty arrays not 404s, and the stream
//     mirrors that).
//   - Recurring events: every interval_seconds (default 5, clamp 1–60)
//     a fresh LatestTradePerSource scan runs and an `observations_update`
//     event fires UNCONDITIONALLY (no client-side dedupe). Customers
//     who want change-detection diff against the previous payload.
//   - Heartbeats: every streaming.DefaultHeartbeatInterval (15 s) when
//     no real event has flowed.
//
// Same URL-discipline rules as /v1/observations: ?granularity= and
// ?window_seconds= return 400 (closed-bucket and tip concepts; not
// valid here). The `interval_seconds` knob is observations-specific
// — its name deliberately differs from `window_seconds` because the
// raw-observations surface has no aggregation window.
func (s *Server) handleObservationsStream(w http.ResponseWriter, r *http.Request) {
	if s.history == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/observations-unavailable",
			"Observations serving not configured", http.StatusServiceUnavailable,
			"this deployment has no HistoryReader wired — check binary configuration")
		return
	}

	if !rejectObservationsTierParams(w, r) {
		return
	}

	asset, quote, ok := parseObservationsAssetQuote(w, r)
	if !ok {
		return
	}
	pair, err := canonical.NewPair(asset, quote)
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-pair",
			"Invalid pair", http.StatusBadRequest, err.Error())
		return
	}

	source := r.URL.Query().Get("source")

	aggregate := r.URL.Query().Get("aggregate")
	if aggregate != "" && aggregate != "latest" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-aggregate",
			"Invalid aggregate parameter", http.StatusBadRequest,
			`aggregate must be "latest" or omitted`)
		return
	}

	interval, ok := parseObservationsIntervalSeconds(w, r)
	if !ok {
		return
	}

	// Synchronous first compute. We commit to streaming here — even
	// an empty array is a valid steady-state for this surface, so we
	// don't 404 on emptiness (the request endpoint doesn't either).
	first, err := s.computeObservations(r.Context(), pair, source, aggregate)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("LatestTradePerSource failed (stream prelude)",
			"err", err, "asset", asset.String(), "quote", quote.String(), "source", source)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	var gen streaming.Generator
	ch := make(chan streaming.Event, observationsStreamProducerQueueDepth)
	prodCtx, cancelProd := context.WithCancel(r.Context())
	defer cancelProd()

	go s.runObservationsStreamProducer(
		prodCtx, ch, &gen, pair, source, aggregate, interval, first,
	)

	streaming.StreamFromChannel(w, r, ch, streaming.StreamOptions{})
}

// computeObservations is the shared core of [Server.handleObservations]
// and [Server.handleObservationsStream]. Returns the post-aggregate
// trade slice ready for wire encoding.
func (s *Server) computeObservations(
	ctx context.Context, pair canonical.Pair, source, aggregate string,
) ([]canonical.Trade, error) {
	trades, err := s.history.LatestTradePerSource(ctx, pair, source)
	if err != nil {
		return nil, err
	}
	if aggregate == "latest" {
		trades = collapseToLatest(trades)
	}
	return trades, nil
}

// runObservationsStreamProducer is the per-connection compute + push
// loop. Emits the pre-computed initial event, then ticks every
// `intervalSeconds` recomputing and emitting unconditionally.
//
// Per-tick failures are logged + skipped (heartbeats keep the
// connection alive). The function returns when ctx cancels and
// closes ch on the way out so [streaming.StreamFromChannel] returns
// cleanly.
func (s *Server) runObservationsStreamProducer(
	ctx context.Context,
	ch chan<- streaming.Event,
	gen *streaming.Generator,
	pair canonical.Pair,
	source, aggregate string,
	intervalSeconds int,
	first []canonical.Trade,
) {
	defer close(ch)

	if firstEv, ok := observationsStreamEvent(gen, first); ok {
		select {
		case <-ctx.Done():
			return
		case ch <- firstEv:
		}
	}

	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			trades, err := s.computeObservations(ctx, pair, source, aggregate)
			if err != nil {
				if ctx.Err() == nil {
					s.logger.Warn("computeObservations failed (stream tick) — skipping emit",
						"err", err, "pair", pair.String(), "source", source)
				}
				continue
			}
			ev, ok := observationsStreamEvent(gen, trades)
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

// observationsStreamEvent builds one SSE event payload from a slice
// of trades. Returns (_, false) on JSON-marshal failure.
//
// Wire shape mirrors the request endpoint: an envelope with `data`
// as an array of TradeRow, `as_of`, `sources`, and `flags`. Single-
// source flag mirrors the request handler (true when exactly one
// source contributed).
func observationsStreamEvent(gen *streaming.Generator, trades []canonical.Trade) (streaming.Event, bool) {
	rows := make([]TradeRow, len(trades))
	srcSet := make(map[string]struct{}, len(trades))
	for i, t := range trades {
		rows[i] = tradeRowFrom(t, 0)
		srcSet[t.Source] = struct{}{}
	}
	srcs := make([]string, 0, len(srcSet))
	for src := range srcSet {
		srcs = append(srcs, src)
	}

	body, err := json.Marshal(observationsStreamPayload{
		Data:    rows,
		AsOf:    time.Now().UTC(),
		Sources: srcs,
		Flags:   Flags{SingleSource: len(srcs) == 1},
	})
	if err != nil {
		return streaming.Event{}, false
	}
	return streaming.Event{
		ID:   gen.Next(),
		Type: "observations_update",
		Data: body,
	}, true
}

// observationsStreamPayload is the SSE-data shape — same envelope
// as the request endpoint so SDK consumers can use one type for
// both polling and streaming.
type observationsStreamPayload struct {
	Data    []TradeRow `json:"data"`
	AsOf    time.Time  `json:"as_of"`
	Sources []string   `json:"sources,omitempty"`
	Flags   Flags      `json:"flags"`
}

// parseObservationsIntervalSeconds reads the optional interval_seconds
// query param, defaulting to defaultObservationsIntervalSeconds and
// rejecting values outside the documented clamp.
func parseObservationsIntervalSeconds(w http.ResponseWriter, r *http.Request) (int, bool) {
	raw := r.URL.Query().Get("interval_seconds")
	if raw == "" {
		return defaultObservationsIntervalSeconds, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n < minObservationsIntervalSeconds || n > maxObservationsIntervalSeconds {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-interval",
			"Invalid interval_seconds", http.StatusBadRequest,
			"interval_seconds must be an integer in [1, 60]")
		return 0, false
	}
	return n, true
}
