package main

import (
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

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
	if serr := seedSoroswapForRecon(ctx, cfg, soroswapDec); serr != nil {
		fmt.Fprintf(os.Stderr, "ch-rebuild: soroswap seed failed (%v) — soroswap may undercount\n", serr)
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

	// ─── Event-based pass ────────────────────────────────────────────────
	evStart := time.Now()
	cherr := clickhouse.StreamContractEvents(ctx, *chAddr, lo, hi, func(ev events.Event) error {
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
			for _, out := range outs {
				if *write {
					pipeline.HandleEvent(ctx, logger, store, out)
				}
				written[src.name]++
			}
		}
		return nil
	})
	if cherr != nil {
		return fmt.Errorf("ch-rebuild: event stream: %w", cherr)
	}
	fmt.Fprintf(os.Stderr, "ch-rebuild: event pass done in %s\n", time.Since(evStart).Round(time.Second))

	// ─── SDEX op-based pass (opt-in) ─────────────────────────────────────
	if *includeSDEX && enabled("sdex") {
		sdexDec := sdex.NewDecoder()
		sStart := time.Now()
		serr := clickhouse.StreamSDEXOps(ctx, *chAddr, lo, hi, func(op clickhouse.SDEXOp) error {
			// SDEX Decode never returns a non-nil error (soft-fails per claim).
			outs, _ := sdexDec.Decode(dispatcher.OpContext{
				Ledger:   op.Ledger,
				ClosedAt: op.ClosedAt,
				TxHash:   op.TxHash,
				TxSource: op.Source,
				OpIndex:  int(op.OpIndex),
				Op:       op.Op,
				OpResult: op.OpResult,
			})
			for _, out := range outs {
				if *write {
					pipeline.HandleEvent(ctx, logger, store, out)
				}
				written["sdex"]++
			}
			return nil
		})
		if serr != nil {
			return fmt.Errorf("ch-rebuild: sdex op stream: %w", serr)
		}
		fmt.Fprintf(os.Stderr, "ch-rebuild: SDEX op pass done in %s\n", time.Since(sStart).Round(time.Second))
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
