package v1

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/api/streaming"
)

const (
	// ledgerStreamProducerQueueDepth is the per-connection buffer
	// between the producer goroutine and the SSE writer. Matches
	// tipStreamProducerQueueDepth — small enough that a wedged writer
	// is detected promptly (next send blocks → ctx teardown).
	ledgerStreamProducerQueueDepth = 4

	// ledgerStreamPollInterval is how often the producer re-reads the
	// ingest cursor. The indexer commits a ledger every ~5s, so a 2s
	// poll surfaces each new ledger within ~2s of it landing.
	ledgerStreamPollInterval = 2 * time.Second

	// ledgerStreamRefreshInterval forces an emit even when the ledger
	// has NOT advanced, so lag_seconds stays current. Without it a
	// stalled indexer would freeze the client on a stale lag that
	// looks healthy; with it the client keeps seeing the (truthfully
	// growing) lag every ~10s during a stall.
	ledgerStreamRefreshInterval = 10 * time.Second
)

// handleLedgerStream serves GET /v1/ledger/stream — the SSE
// counterpart of /v1/ledger/tip. It pushes a `ledger_update` event
// each time the live-ingest frontier advances, so a status page can
// render blocks arriving in real time instead of polling.
//
// Wire shape per connection:
//
//   - Headers: text/event-stream + no-cache + X-Accel-Buffering: no
//     (set by streaming.StreamFromChannel).
//   - Initial event: a ledger_update with the current tip, emitted
//     synchronously on connect.
//   - Recurring events: one per new ledger (poll cadence ~2s), plus
//     a keepalive refresh every ~10s if the ledger has not advanced
//     — so lag_seconds never goes stale even during an ingest stall.
//   - Heartbeats: streaming.DefaultHeartbeatInterval (15s) comment
//     frames when nothing else flows.
//
// Pre-stream errors (no CursorsReader wired, cursor not yet
// established) are returned as problem+json with the right status —
// once the SSE body starts there is no way to set a non-200 code.
func (s *Server) handleLedgerStream(w http.ResponseWriter, r *http.Request) {
	if s.cursors == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/ledger-tip-unavailable",
			"Ledger tip not available", http.StatusServiceUnavailable,
			"this deployment has no CursorsReader wired — check binary configuration")
		return
	}

	// First synchronous read — the chance to return a non-200 before
	// the response switches into SSE mode.
	first, ok, err := s.ledgerTip(r.Context())
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		s.logger.Error("ledgerTip failed (stream prelude)", "err", err)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}
	if !ok {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/ledger-tip-unavailable",
			"Ledger tip not available", http.StatusServiceUnavailable,
			"the live-ingest cursor has not been established yet — the indexer "+
				"has not committed its first ledger on this deployment")
		return
	}

	ch := make(chan streaming.Event, ledgerStreamProducerQueueDepth)
	prodCtx, cancelProd := context.WithCancel(r.Context())
	defer cancelProd()

	go s.runLedgerStreamProducer(prodCtx, ch, first)

	streaming.StreamFromChannel(w, r, ch, streaming.StreamOptions{})
}

// runLedgerStreamProducer is the per-connection poll loop. It emits
// the pre-computed initial event, then polls the ingest cursor every
// ledgerStreamPollInterval and emits when the ledger advanced — or
// when ledgerStreamRefreshInterval has elapsed without an advance, so
// lag_seconds stays honest during a stall. Returns and closes ch on
// ctx cancel (client disconnect). The per-tick decision lives in
// nextLedgerEvent to keep this loop flat.
func (s *Server) runLedgerStreamProducer(
	ctx context.Context,
	ch chan<- streaming.Event,
	first LedgerTipView,
) {
	defer close(ch)

	var gen streaming.Generator
	lastLedger := first.LatestLedger
	lastEmit := time.Now()

	if ev, ok := ledgerStreamEvent(&gen, first); ok {
		if !sendStreamEvent(ctx, ch, ev) {
			return
		}
	}

	ticker := time.NewTicker(ledgerStreamPollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			ev, view, ok := s.nextLedgerEvent(ctx, &gen, lastLedger, lastEmit)
			if !ok {
				continue
			}
			if !sendStreamEvent(ctx, ch, ev) {
				return
			}
			lastLedger = view.LatestLedger
			lastEmit = time.Now()
		}
	}
}

// nextLedgerEvent runs one producer tick: re-read the ingest cursor
// and decide whether to emit. Returns ok=false (caller skips the
// tick, keeping the connection alive on heartbeats) on a transient
// cursor-read failure, a not-yet-established cursor, or when the
// ledger has neither advanced nor aged past the refresh interval.
func (s *Server) nextLedgerEvent(
	ctx context.Context,
	gen *streaming.Generator,
	lastLedger uint32,
	lastEmit time.Time,
) (streaming.Event, LedgerTipView, bool) {
	view, ok, err := s.ledgerTip(ctx)
	if err != nil {
		if ctx.Err() == nil {
			s.logger.Warn("ledgerTip failed (stream tick) — skipping emit", "err", err)
		}
		return streaming.Event{}, LedgerTipView{}, false
	}
	if !ok {
		return streaming.Event{}, LedgerTipView{}, false
	}
	advanced := view.LatestLedger > lastLedger
	refresh := time.Since(lastEmit) >= ledgerStreamRefreshInterval
	if !advanced && !refresh {
		return streaming.Event{}, LedgerTipView{}, false
	}
	ev, ok := ledgerStreamEvent(gen, view)
	if !ok {
		return streaming.Event{}, LedgerTipView{}, false
	}
	return ev, view, true
}

// sendStreamEvent pushes one event onto the per-connection channel,
// returning false if ctx cancelled first (client disconnect) so the
// producer can tear down.
func sendStreamEvent(ctx context.Context, ch chan<- streaming.Event, ev streaming.Event) bool {
	select {
	case <-ctx.Done():
		return false
	case ch <- ev:
		return true
	}
}

// ledgerStreamPayload is the SSE-data shape of a ledger_update
// event. It mirrors the /v1/ledger/tip envelope (data + as_of) so an
// SDK consumer can decode polled and streamed responses with one
// type.
type ledgerStreamPayload struct {
	Data LedgerTipView `json:"data"`
	AsOf time.Time     `json:"as_of"`
}

// ledgerStreamEvent builds one ledger_update SSE event. Returns
// (_, false) on a JSON-marshal failure (a programming error in
// LedgerTipView) so the caller skips the emit and keeps the stream
// alive rather than tearing it down.
func ledgerStreamEvent(gen *streaming.Generator, view LedgerTipView) (streaming.Event, bool) {
	body, err := json.Marshal(ledgerStreamPayload{
		Data: view,
		AsOf: time.Now().UTC(),
	})
	if err != nil {
		return streaming.Event{}, false
	}
	return streaming.Event{
		ID:   gen.Next(),
		Type: "ledger_update",
		Data: body,
	}, true
}
