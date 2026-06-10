package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// tradeOf extracts the canonical.Trade from a trade-shaped event so the rebuild
// can batch-insert trades (the bulk of the projected output). Mirrors
// pipeline.tradeFromEvent for the projected trade sources.
func tradeOf(ev consumer.Event) (canonical.Trade, bool) {
	switch e := ev.(type) {
	case aquarius.TradeEvent:
		return e.Trade, true
	case soroswap.TradeEvent:
		return e.Trade, true
	case phoenix.TradeEvent:
		return e.Trade, true
	case comet.TradeEvent:
		return e.Trade, true
	case sdex.TradeEvent:
		return e.Trade, true
	}
	return canonical.Trade{}, false
}

// seedSoroswapFromPG seeds the soroswap decoder's pair registry from the
// persisted soroswap_pairs table (fast, no RPC). Mirrors the seeding half of
// pipeline.SoroswapPersistenceOptions.
func seedSoroswapFromPG(ctx context.Context, store *timescale.Store, dec *soroswap.Decoder) (int, error) {
	rows, err := store.LoadSoroswapPairRegistry(ctx)
	if err != nil {
		return 0, err
	}
	n := 0
	for _, r := range rows {
		t0, err := canonical.NewSorobanAsset(r.Token0Strkey)
		if err != nil {
			continue
		}
		t1, err := canonical.NewSorobanAsset(r.Token1Strkey)
		if err != nil {
			continue
		}
		dec.SeedPair(r.PairStrkey, t0, t1)
		n++
	}
	return n, nil
}

// chRebuild is the ADR-0034 Phase-4 write path: it re-derives a ledger range's
// protocol output from the ClickHouse Tier-1 lake using the EXISTING decoders
// and WRITES it to the Postgres served tier via the production sink
// (pipeline.HandleEvent — idempotent ON CONFLICT). It is the write-enabled
// sibling of ch-reproject (which only counts + compares).
//
// Two passes, mirroring the dataflow split:
//   - Event-based sources (soroswap / aquarius / phoenix / comet / blend /
//     cctp / rozo / defindex / reflector / redstone): one StreamContractEvents
//     pass, every Matches-gated decoder per event. This is where the
//     event_index-collision recovery lands (CH > served: aquarius +61%,
//     defindex/cctp/blend_emissions 0→N).
//   - SDEX (op-based, NOT in contract_events): a StreamSDEXOps pass feeding the
//     SDEX OpDecoder. Gated behind -sdex because it decodes ~15.5 B trade ops
//     across all history and the loss it recovers (passive-offer + one-side-zero
//     fills) is ~0.004 % and pricing-immaterial (the aggregator skips zero legs;
//     served pricing is CEX+SDEX-dominated). The fixed live indexer captures
//     these forward; a full historical SDEX rebuild is opt-in.
//
// Defaults to DRY-RUN (count only). Pass -write to persist. For a clean-slate
// rebuild (ADR-0034 "rebuild, not repair") the operator truncates the target
// tables first; the writes are idempotent either way (recover-into-existing or
// repopulate-after-truncate). Window [from,to] per partition for the full run
// so the streamed result set + the successful-tx IN-set stay bounded.
func chRebuild(args []string) error { //nolint:gocognit,gocyclo,funlen // linear: seed, event pass, optional op pass, report; splitting hurts clarity.
	fs := flag.NewFlagSet("ch-rebuild", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 0, "first ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "last ledger sequence (inclusive, required)")
	chAddr := fs.String("ch-addr", "127.0.0.1:9300", "ClickHouse native address")
	only := fs.String("sources", "", "comma-separated source names to rebuild (default: all event-based)")
	includeSDEX := fs.Bool("sdex", false, "also re-derive SDEX trades from operations (expensive: ~15.5B op decodes all-history)")
	sdexGaps := fs.Bool("sdex-gaps", false, "with -sdex: re-derive ONLY the served gaps in [from,to] in one pass (each gap is an empty range → pure insert, no ON CONFLICT walk) — efficient drop-backlog recovery vs re-scanning the whole range")
	write := fs.Bool("write", false, "actually write to Postgres (default: dry-run, count only)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return fmt.Errorf("-config, -from, -to are required; -to must be >= -from")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	ctx, cancel := signalContext()
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage open: %w", err)
	}
	defer func() { _ = store.Close() }()

	// Warn-level logger: HandleEvent debug-logs per event, which would flood at
	// rebuild volume. Errors + warns still surface.
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	lo, hi := uint32(*from), uint32(*to)

	cat, soroswapDec := buildReconciliationCatalogue(cfg)
	// Seed soroswap pairs from the PG registry (NOT the RPC factory seed —
	// per-window invocations would each pay a ~200s RPC round-trip + depend on
	// an external endpoint). The live indexer persists every new_pair to
	// soroswap_pairs, so the registry covers all historical pairs; pairs created
	// within a window are also self-discovered from in-range new_pair events.
	if n, serr := seedSoroswapFromPG(ctx, store, soroswapDec); serr != nil {
		fmt.Fprintf(os.Stderr, "ch-rebuild: soroswap PG seed failed (%v) — soroswap may undercount\n", serr)
	} else {
		fmt.Fprintf(os.Stderr, "ch-rebuild: seeded %d soroswap pairs from PG\n", n)
	}

	srcFilter := map[string]bool{}
	for _, s := range strings.Split(*only, ",") {
		if s = strings.TrimSpace(s); s != "" {
			srcFilter[s] = true
		}
	}
	enabled := func(name string) bool { return len(srcFilter) == 0 || srcFilter[name] }

	mode := "DRY-RUN (count only)"
	if *write {
		mode = "WRITE"
	}
	fmt.Fprintf(os.Stderr, "ch-rebuild: [%d,%d] mode=%s sources=%q sdex=%v ch=%s\n",
		lo, hi, mode, *only, *includeSDEX, *chAddr)

	written := map[string]int{} // source name -> events written (or counted in dry-run)

	// Buffer decoded events during the CH stream, then write to Postgres AFTER
	// the stream closes. Holding the CH FINAL stream open across slow per-row PG
	// writes trips the client read timeout mid-stream; decoupling read from
	// write keeps the CH connection short-lived. Windows are partition-aligned
	// (1M) so a window's decoded set stays bounded in memory.
	var buf []consumer.Event

	// ─── Event-based pass (read → buffer) ────────────────────────────────
	evStart := time.Now()
	// Exclude the CAP-67 classic-token firehose — none of the projected DEX/
	// lending sources consume it, and it's 99.99% of contract_events. Use
	// FirehoseExcludeSyms (NOT ClassicTokenTopic0Syms): set_admin must be
	// RETAINED because Blend/Comet emit a pool set_admin sharing that topic —
	// excluding it wholesale dropped blend_admin's set_admin rows from the
	// re-derive (matches the projector's firehoseExcludeSyms).
	cherr := clickhouse.StreamContractEvents(ctx, *chAddr, lo, hi, clickhouse.FirehoseExcludeSyms, func(ev events.Event) error {
		for _, src := range cat {
			if src.dec == nil || !enabled(src.name) {
				continue
			}
			if len(src.contractIDs) > 0 && !containsStr(src.contractIDs, ev.ContractID) {
				continue
			}
			if !src.dec.Matches(ev) {
				continue
			}
			outs, derr := src.dec.Decode(ev)
			if derr != nil {
				continue // soft-fail, mirroring the projector + live path
			}
			buf = append(buf, outs...)
		}
		return nil
	})
	if cherr != nil {
		return fmt.Errorf("ch-rebuild: event stream: %w", cherr)
	}
	fmt.Fprintf(os.Stderr, "ch-rebuild: event read done in %s (%d events buffered)\n",
		time.Since(evStart).Round(time.Second), len(buf))

	// ─── SDEX op-based pass (opt-in; read → buffer) ──────────────────────
	if *includeSDEX && enabled("sdex") {
		sdexDec := sdex.NewDecoder()
		sStart := time.Now()
		decodeRange := func(rlo, rhi uint32) error {
			// SDEX Decode never returns a non-nil error (soft-fails per claim).
			return clickhouse.StreamSDEXOps(ctx, *chAddr, rlo, rhi, func(op clickhouse.SDEXOp) error {
				outs, _ := sdexDec.Decode(dispatcher.OpContext{
					Ledger:   op.Ledger,
					ClosedAt: op.ClosedAt,
					TxHash:   op.TxHash,
					TxSource: op.Source,
					OpIndex:  int(op.OpIndex),
					Op:       op.Op,
					OpResult: op.OpResult,
				})
				buf = append(buf, outs...)
				return nil
			})
		}
		if *sdexGaps {
			// Re-derive ONLY the served gaps in one pass: each gap is an empty
			// ledger range, so the decode + write is a pure insert (no ON CONFLICT
			// walk over the 121M served rows that makes a full-range pass slow).
			// This clears the dual-sink drop backlog cheaply and safely.
			targets, terr := resolveFindDataGapsTargets("sdex")
			if terr != nil {
				return fmt.Errorf("ch-rebuild: sdex gap targets: %w", terr)
			}
			var ng int
			for _, tgt := range targets {
				gaps, gerr := store.FindPerSourceLedgerGaps(ctx, tgt, int64(lo), int64(hi), 1)
				if gerr != nil {
					return fmt.Errorf("ch-rebuild: find sdex gaps: %w", gerr)
				}
				for _, g := range gaps {
					if derr := decodeRange(uint32(g.Start), uint32(g.End)); derr != nil { //nolint:gosec // ledger seq fits uint32
						return fmt.Errorf("ch-rebuild: sdex gap [%d,%d]: %w", g.Start, g.End, derr)
					}
					ng++
				}
			}
			fmt.Fprintf(os.Stderr, "ch-rebuild: SDEX gap-only read done (%d gaps) in %s\n", ng, time.Since(sStart).Round(time.Second))
		} else if derr := decodeRange(lo, hi); derr != nil {
			return fmt.Errorf("ch-rebuild: sdex op stream: %w", derr)
		} else {
			fmt.Fprintf(os.Stderr, "ch-rebuild: SDEX read done in %s\n", time.Since(sStart).Round(time.Second))
		}
	}

	// ─── write the buffered events to Postgres (trades batched) ──────────
	// Per-row HandleEvent does 2 PG round-trips per trade (WouldPopulateUSDVolume
	// + InsertTrade); at dense-window volume (175k events/window) that's hours.
	// Batch trade-shaped events via BatchInsertTrades (one multi-row INSERT per
	// batch); everything else (protocol entities, fewer rows) stays per-row.
	wStart := time.Now()
	const tradeBatchN = 1000
	batch := make([]canonical.Trade, 0, tradeBatchN)
	flush := func() {
		if len(batch) == 0 {
			return
		}
		if err := store.BatchInsertTrades(ctx, batch); err != nil {
			logger.Warn("batch trade insert failed; per-row fallback", "n", len(batch), "err", err)
			for _, t := range batch {
				if ierr := store.InsertTrade(ctx, t); ierr != nil {
					logger.Error("per-row trade insert failed", "err", ierr)
				}
			}
		}
		batch = batch[:0]
	}
	for _, ev := range buf {
		if *write {
			if t, ok := tradeOf(ev); ok {
				batch = append(batch, t)
				if len(batch) >= tradeBatchN {
					flush()
				}
			} else {
				pipeline.HandleEvent(ctx, logger, store, ev)
			}
		}
		written[ev.Source()]++
	}
	if *write {
		flush()
		fmt.Fprintf(os.Stderr, "ch-rebuild: wrote %d events in %s\n", len(buf), time.Since(wStart).Round(time.Second))
	}

	// ─── report ──────────────────────────────────────────────────────────
	fmt.Printf("\n=== ch-rebuild [%d,%d] %s ===\n", lo, hi, mode)
	fmt.Printf("%-16s %14s\n", "source", "events")
	var total int
	for _, src := range cat {
		n, ok := written[src.name]
		if !ok {
			continue
		}
		fmt.Printf("%-16s %14d\n", src.name, n)
		total += n
	}
	fmt.Printf("%-16s %14d\n", "TOTAL", total)
	if !*write {
		fmt.Printf("\n(dry-run — re-run with -write to persist to Postgres)\n")
	}
	return nil
}
