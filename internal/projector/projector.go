// Package projector tails the `soroban_events` raw-event landing
// zone (ADR-0029) and writes per-source classifier rows by
// invoking each protocol's existing Go decoder
// (`internal/sources/<protocol>/decode.go`). Per ADR-0032 the
// projector is the SINGLE write path for per-source tables;
// during Phase 3 it runs in parallel with the dispatcher's
// existing per-source sink (both write, ON CONFLICT DO NOTHING
// absorbs duplicates) so we can verify projection rate matches
// live ingest before Phase 4 makes the projector primary.
//
// Architecture (one component, many cursors):
//
//	soroban_events  (raw, authoritative)
//	     │
//	     ▼  StreamSorobanEvents from cursor.last_ledger
//	   Projector
//	     │
//	     ├─► aquarius.Decoder ──► persistTrade            (trades)
//	     ├─► blend.Decoder    ──► persistBlend*           (blend_*)
//	     ├─► phoenix.Decoder  ──► persistPhoenix*         (phoenix_*)
//	     ├─► ... per protocol
//	     ▼
//	   projector.cursor[source].last_ledger  (advances per cycle)
//
// Per-source cursors mean one stuck source (e.g. a decoder bug
// flooding decode_errors) doesn't block the others — each loops
// independently.
//
// Parallel-mode safety (Phase 3): the dispatcher's pre-existing
// per-source sink runs unchanged. Both writers race for the same
// (ledger, tx_hash, op_index, …) PK; ON CONFLICT DO NOTHING means
// whichever wins, the other no-ops. The projector's correctness
// signal is `projector_lag_ledgers` — if it stays low, the
// projector is keeping up; Phase 4 flips the dispatcher's
// per-source sink off and the projector becomes sole writer.
package projector

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/sources/sorobanevents"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// Interval is the catch-up cadence. The projector reads new
// soroban_events rows every Interval; between cycles the
// projector is idle. Right-sized to balance read-after-write
// latency (smaller is fresher) with Postgres scan overhead
// (smaller is more queries). 5s is a default that keeps r1's
// per-source tables ~5-10s behind raw; tunable per-deployment.
const Interval = 5 * time.Second

// BatchLimit caps how many soroban_events rows the projector
// reads per source per cycle. Without a cap a catch-up after
// long outage would stream millions of rows in one transaction,
// blocking other work. 10K rows = ~50 MB at typical size; one
// batch processes in ~1-3s; subsequent cycles drain the rest.
const BatchLimit = 10_000

// PerSourceTimeout caps one source's per-cycle work. A wedged
// downstream sink can't block other sources past this.
const PerSourceTimeout = 60 * time.Second

// SinkFunc is the per-event handler the projector calls after
// successful decode. The existing
// `internal/pipeline/sink.go::handleOneEvent` function is the
// production wiring (Phase 3 parallel mode); Phase 4 will route
// to a direct per-source persister bypassing handleOneEvent.
//
// The projector treats sink failures as warnings (decode succeeded,
// downstream write failed) — does not advance the cursor for that
// row, retries on the next cycle. ON CONFLICT DO NOTHING in the
// downstream Insert* ensures repeated writes are idempotent.
type SinkFunc func(ctx context.Context, ev consumer.Event)

// Source describes one protocol's projection target. The
// projector keeps an independent cursor per source so one stuck
// decoder doesn't block the rest.
type Source struct {
	// Name is the cursor sub_source key + log label. Must be
	// unique within a Registry. Examples: "aquarius", "blend",
	// "phoenix", "soroswap-skim".
	Name string

	// Decoder is the protocol-specific event handler. Same
	// interface the dispatcher uses; the projector calls
	// Matches + Decode in the same order.
	Decoder dispatcher.Decoder

	// ContractIDs / Topic0Syms narrow the SQL pre-filter so the
	// projector doesn't stream irrelevant rows. Pass nil for
	// "match by Decoder.Matches alone" — coarser network read
	// but simpler config. Mirrors `StreamSorobanEvents`'s args.
	ContractIDs []string
	Topic0Syms  []string
}

// Registry is the set of sources the projector handles. Built
// once at startup; immutable while the projector runs.
type Registry struct {
	Sources []Source
}

// Projector reads soroban_events and routes decoded events to
// the sink for each registered source.
type Projector struct {
	store    *timescale.Store
	registry Registry
	sink     SinkFunc
	logger   *slog.Logger

	// chAddr, when non-empty, switches the per-source read from the Postgres
	// soroban_events landing zone to the ClickHouse Tier-1 lake's
	// contract_events (ADR-0034 #10 feed-switch — the dual-sink feeds CH
	// inline, so CH is authoritative for forward events and soroban_events can
	// be decommissioned). The per-source cursor (last_ledger) is
	// source-agnostic, so the switch is seamless. Empty = legacy
	// soroban_events read.
	chAddr string
}

// SetClickHouseSource switches the projector to read forward events from the
// ClickHouse lake at addr instead of Postgres soroban_events (ADR-0034 #10).
// Call before Run. Empty addr keeps the legacy soroban_events source.
func (p *Projector) SetClickHouseSource(addr string) { p.chAddr = addr }

// New constructs a Projector. Callers must call Run to start
// the loop.
func New(store *timescale.Store, registry Registry, sink SinkFunc, logger *slog.Logger) *Projector {
	if logger == nil {
		logger = slog.Default()
	}
	return &Projector{
		store:    store,
		registry: registry,
		sink:     sink,
		logger:   logger,
	}
}

// Run blocks until ctx is cancelled. Drives one goroutine per
// source; each independently tails its slice of soroban_events
// and advances its cursor.
func (p *Projector) Run(ctx context.Context) error {
	if p.store == nil {
		return errors.New("projector: nil store")
	}
	if p.sink == nil {
		return errors.New("projector: nil sink")
	}
	if len(p.registry.Sources) == 0 {
		p.logger.Warn("projector: empty registry; nothing to project")
		<-ctx.Done()
		return ctx.Err()
	}

	var wg sync.WaitGroup
	for _, src := range p.registry.Sources {
		wg.Add(1)
		go func(src Source) {
			defer wg.Done()
			p.runOneSource(ctx, src)
		}(src)
	}
	wg.Wait()
	return ctx.Err()
}

// runOneSource is the per-source catch-up loop. Reads from the
// projector cursor's last_ledger forward, batches up to
// BatchLimit rows per cycle, advances the cursor on success.
func (p *Projector) runOneSource(ctx context.Context, src Source) {
	t := time.NewTicker(Interval)
	defer t.Stop()
	// First cycle runs immediately so a fresh deploy starts
	// catching up without waiting Interval.
	p.cycleOneSource(ctx, src)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.cycleOneSource(ctx, src)
		}
	}
}

// cycleOneSource runs one read-decode-write cycle for one source.
// Soft-fails: read errors / decode errors / sink errors all log
// + leave the cursor untouched so the next cycle retries the
// same rows (ON CONFLICT DO NOTHING absorbs the idempotent
// repeats).
//
//nolint:gocognit,funlen // linear cycle (cursor read → tip → scan → cursor write) with a source branch (soroban_events vs CH); splitting into helpers would scatter the cycle's success/failure metric emissions and make the control flow harder to audit.
func (p *Projector) cycleOneSource(ctx context.Context, src Source) {
	start := time.Now()
	cycleCtx, cancel := context.WithTimeout(ctx, PerSourceTimeout)
	defer cancel()

	cursor, err := p.store.GetCursor(cycleCtx, "projector", src.Name)
	if err != nil && !errors.Is(err, timescale.ErrNotFound) {
		p.logger.Warn("projector: read cursor failed", "source", src.Name, "err", err)
		obs.ProjectorRunsTotal.WithLabelValues(src.Name, "error").Inc()
		return
	}
	fromLedger := uint32(0)
	if err == nil {
		// Resume one ledger AFTER the last fully-processed one.
		// soroban_events.ledger BETWEEN $1 AND $2 is inclusive on
		// both ends so adding 1 here avoids reprocessing the seam.
		fromLedger = cursor.LastLedger + 1
	}

	// Upper bound: live tip from ledgerstream. Without a tip we
	// scan to "wherever soroban_events extends," which during a
	// fresh deploy could be far ahead of where live writes have
	// committed. Better to track ledgerstream so the projector
	// never gets ahead of "what we promise is durable."
	tip, err := p.resolveTip(cycleCtx)
	if err != nil {
		p.logger.Warn("projector: tip resolve failed", "source", src.Name, "err", err)
		obs.ProjectorRunsTotal.WithLabelValues(src.Name, "error").Inc()
		return
	}
	if tip <= fromLedger {
		// Caught up — no rows to process this cycle.
		obs.ProjectorRunsTotal.WithLabelValues(src.Name, "idle").Inc()
		obs.ProjectorLagLedgers.WithLabelValues(src.Name).Set(0)
		return
	}

	toLedger := tip
	if toLedger-fromLedger > BatchLimit {
		toLedger = fromLedger + BatchLimit
	}

	var (
		rowsScanned    int
		eventsEmitted  int
		decodeErrors   int
		lastSeenLedger uint32
	)
	// process runs the per-event decode + route, identical regardless of the
	// read source (soroban_events or CH contract_events). Decode failures
	// soft-fail (cursor still advances; the row is deterministically broken so
	// a retry would re-fail) and are counted for visibility.
	process := func(ev events.Event) {
		if !src.Decoder.Matches(ev) {
			return
		}
		outs, derr := src.Decoder.Decode(ev)
		if derr != nil {
			decodeErrors++
			return
		}
		for _, out := range outs {
			p.sink(cycleCtx, out)
			eventsEmitted++
		}
	}

	if p.chAddr != "" {
		// CH feed-switch (#10): read contract_events directly (already an
		// events.Event, no Reconstruct). No FINAL — small forward window +
		// idempotent downstream writes absorb any duplicate.
		err = clickhouse.StreamContractEventsFiltered(cycleCtx, p.chAddr, fromLedger, toLedger,
			src.ContractIDs, src.Topic0Syms,
			func(ev events.Event) error {
				rowsScanned++
				if ev.Ledger > lastSeenLedger {
					lastSeenLedger = ev.Ledger
				}
				process(ev)
				return nil
			})
	} else {
		err = p.store.StreamSorobanEvents(cycleCtx, fromLedger, toLedger,
			src.ContractIDs, src.Topic0Syms,
			func(row sorobanevents.Row) error {
				rowsScanned++
				if row.Ledger > lastSeenLedger {
					lastSeenLedger = row.Ledger
				}
				ev, rerr := sorobanevents.Reconstruct(row)
				if rerr != nil {
					// Skip a malformed row but keep the cursor advancing; the
					// row is unrecoverable so re-reading it next cycle would
					// just re-fail. Count it for visibility.
					decodeErrors++
					return nil //nolint:nilerr // intentional soft-fail; see comment.
				}
				process(ev)
				return nil
			})
	}
	if err != nil {
		p.logger.Warn("projector: stream failed", "source", src.Name, "err", err, "from", fromLedger, "to", toLedger)
		obs.ProjectorRunsTotal.WithLabelValues(src.Name, "error").Inc()
		return
	}

	// Advance cursor to toLedger (not lastSeenLedger): a source
	// that's silent in a range still moves the cursor so we don't
	// rescan empty stretches every cycle. lastSeenLedger is only
	// logged for visibility.
	if err := p.store.UpsertCursor(cycleCtx, "projector", src.Name, toLedger); err != nil {
		p.logger.Warn("projector: cursor advance failed", "source", src.Name, "err", err)
		obs.ProjectorRunsTotal.WithLabelValues(src.Name, "error").Inc()
		return
	}

	obs.ProjectorLagLedgers.WithLabelValues(src.Name).Set(float64(tip - toLedger))
	obs.ProjectorEventsDecoded.WithLabelValues(src.Name, "ok").Add(float64(eventsEmitted))
	if decodeErrors > 0 {
		obs.ProjectorEventsDecoded.WithLabelValues(src.Name, "decode_error").Add(float64(decodeErrors))
	}
	obs.ProjectorRunsTotal.WithLabelValues(src.Name, "ok").Inc()
	obs.ProjectorCycleDurationSeconds.WithLabelValues(src.Name).Observe(time.Since(start).Seconds())

	if eventsEmitted > 0 || decodeErrors > 0 {
		p.logger.Info("projector cycle",
			"source", src.Name,
			"from", fromLedger, "to", toLedger,
			"rows_scanned", rowsScanned,
			"events_emitted", eventsEmitted,
			"decode_errors", decodeErrors,
			"lag_ledgers", tip-toLedger,
			"elapsed", time.Since(start).Round(time.Millisecond),
		)
	}
}

// resolveTip returns the live ledgerstream cursor's last_ledger
// — the upper scan bound. Same approach as the gap detector
// (gap_detector.go::resolveGapDetectorTip).
func (p *Projector) resolveTip(ctx context.Context) (uint32, error) {
	c, err := p.store.GetCursor(ctx, "ledgerstream", "")
	if err != nil {
		if errors.Is(err, timescale.ErrNotFound) {
			return 0, nil
		}
		return 0, fmt.Errorf("ledgerstream cursor: %w", err)
	}
	return c.LastLedger, nil
}
