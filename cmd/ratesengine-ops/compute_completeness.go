package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/completeness"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// sorobanEraGenesis is the first pubnet ledger with Soroban — the lower
// bound for the global recognition scan.
const sorobanEraGenesis = 50_457_424

// computeCompleteness is the ADR-0033 Phase 6 computor: it derives the
// per-source completeness WATERMARK (substrate ∧ recognition ∧
// projection) and writes it to completeness_snapshots for the API +
// status page. Operator / cron-driven; compute-once / read-cheap, like
// the gap detector's source_coverage_snapshots.
//
// Per-source watermark = substrate continuity + hash chain (Claim 1) ∧
// projection reconciliation across ALL the source's tables (Claim 2b) ∧
// recognition for the source's own contracts (Claim 2a). Recognition
// gaps on a CONTRACT-PINNED source (oracles) cap that source; gaps on
// contracts no source owns go to a system-wide `recognition` snapshot
// (topic-based sources can't attribute an unhandled topic to themselves).
//
// Projection is bounded to the substrate∧recognition-verified region:
// no point re-deriving where an earlier claim already failed.
func computeCompleteness(args []string) error { //nolint:funlen,gocognit,gocyclo // linear computor; one block per claim.
	fs := flag.NewFlagSet("compute-completeness", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	toFlag := fs.Uint("to", 0, "Tip ledger (inclusive); 0 = resolve from the live ledgerstream cursor")
	only := fs.String("source", "", "Limit to one source (e.g. soroswap|blend|reflector-dex|sdex)")
	useCH := fs.Bool("ch", false, "Read all three claims from the certified ClickHouse lake (substrate + recognition + projection re-derive) instead of Postgres soroban_events — fast, off the serving DB (ADR-0033 + ADR-0034)")
	chAddr := fs.String("ch-addr", "127.0.0.1:9300", "ClickHouse native address (with -ch)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return fmt.Errorf("-config is required")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Minute)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage open: %w", err)
	}
	defer func() { _ = store.Close() }()

	tip := uint32(*toFlag)
	if tip == 0 {
		cur, gerr := store.GetCursor(ctx, "ledgerstream", "")
		if gerr != nil {
			return fmt.Errorf("resolve tip from ledgerstream cursor: %w (pass -to to override)", gerr)
		}
		tip = cur.LastLedger
	}
	if tip == 0 {
		return fmt.Errorf("tip resolved to 0 — pass -to")
	}
	fmt.Fprintf(os.Stderr, "compute-completeness: tip=%d\n", tip)

	catalogue, soroswapDec := buildReconciliationCatalogue(cfg)
	if *only == "" || *only == "soroswap" {
		if serr := seedSoroswapForRecon(ctx, cfg, soroswapDec); serr != nil {
			fmt.Fprintf(os.Stderr, "compute-completeness: soroswap seed failed (%v) — soroswap projection may undercount\n", serr)
		}
	}

	// CH lake event source for projection re-derive (ADR-0034) when -ch.
	var chStreamer completeness.EventStreamer
	if *useCH {
		chStreamer = clickhouse.ReconcileEventStreamer{Addr: *chAddr}
	}

	// ── Recognition (Claim 2a): one global scan, attributed per source ──
	var (
		recGaps []completeness.RecognitionGap
		recErr  error
	)
	if *useCH {
		recGaps, recErr = computeRecognitionGapsCH(ctx, cfg, *chAddr, tip)
	} else {
		recGaps, recErr = computeRecognitionGaps(ctx, store, cfg, tip)
	}
	if recErr != nil {
		fmt.Fprintf(os.Stderr, "compute-completeness: recognition scan failed: %v\n", recErr)
	}
	ownerOf := map[string]string{} // contract_id → source name (contract-pinned sources)
	for _, src := range catalogue {
		for _, c := range src.contractIDs {
			ownerOf[c] = src.name
		}
	}
	recBySource := map[string][]uint32{}
	var unattributed []completeness.RecognitionGap
	for _, g := range recGaps {
		if owner, ok := ownerOf[g.ContractID]; ok {
			recBySource[owner] = append(recBySource[owner], g.MinLedger)
		} else {
			unattributed = append(unattributed, g)
		}
	}

	// Substrate (Claim 1) is a property of the lake, not a source — compute the
	// earliest gap/break ONCE in -ch mode (over the whole Soroban-era range) and
	// reuse per source. The CH lake is the certified authoritative substrate.
	var chSubProblem uint32
	var chSubHas bool
	if *useCH {
		p, has, d, serr := clickhouse.SubstrateProblem(ctx, *chAddr, 2, tip)
		if serr != nil {
			return fmt.Errorf("ch substrate: %w", serr)
		}
		chSubProblem, chSubHas = p, has
		if has {
			fmt.Fprintf(os.Stderr, "compute-completeness: CH substrate problem at %d (%s)\n", p, d)
		} else {
			fmt.Fprintln(os.Stderr, "compute-completeness: CH substrate intact [2,tip] — contiguous + hash-chained")
		}
	}

	// ── Per-source watermark ────────────────────────────────────────
	for _, src := range catalogue {
		if *only != "" && src.name != *only {
			continue
		}
		genesis := src.genesis
		var problems []uint32
		var detail []string

		// Claim 1: substrate continuity + hash chain over [genesis, tip].
		var substrateOK bool
		if *useCH {
			// Reuse the once-computed lake substrate; it's this source's
			// problem only if it falls at/after the source's genesis.
			substrateOK = !chSubHas || chSubProblem < genesis
			if !substrateOK {
				problems = append(problems, chSubProblem)
				detail = append(detail, fmt.Sprintf("substrate: lake gap/break at %d", chSubProblem))
			}
		} else {
			subGaps, err := store.FindLedgerIngestGaps(ctx, genesis, tip)
			if err != nil {
				return fmt.Errorf("%s: substrate gaps: %w", src.name, err)
			}
			breaks, err := store.VerifyLedgerHashChain(ctx, genesis, tip)
			if err != nil {
				return fmt.Errorf("%s: hash chain: %w", src.name, err)
			}
			substrateOK = len(subGaps) == 0 && len(breaks) == 0
			for _, g := range subGaps {
				problems = append(problems, uint32(g.Start))
			}
			for _, b := range breaks {
				problems = append(problems, b.LedgerSeq)
			}
			if !substrateOK {
				detail = append(detail, fmt.Sprintf("substrate: %d gap(s), %d chain break(s)", len(subGaps), len(breaks)))
			}
		}

		// Claim 2a: recognition gaps attributed to this source's contracts.
		recOK := true
		for _, l := range recBySource[src.name] {
			if l >= genesis {
				problems = append(problems, l)
				recOK = false
			}
		}
		if !recOK {
			detail = append(detail, "recognition: unhandled topic on this source's contract(s)")
		}

		// Bound projection to the substrate∧recognition-verified region.
		srW := completeness.ComputeWatermark(genesis, tip, problems)
		projOK := false
		if srW.Ledger >= genesis {
			projHi := srW.Ledger
			pgaps, perr := reconcileSourceProjection(ctx, store, chStreamer, src, genesis, projHi)
			if perr != nil {
				return fmt.Errorf("%s: projection: %w", src.name, perr)
			}
			projOK = len(pgaps) == 0
			problems = append(problems, pgaps...)
			if !projOK {
				detail = append(detail, fmt.Sprintf("projection: %d mismatched ledger(s) in [%d,%d]", len(pgaps), genesis, projHi))
			}
		} else {
			detail = append(detail, "projection: not evaluated (earlier claim failed at genesis)")
		}

		w := completeness.ComputeWatermark(genesis, tip, problems)
		if len(detail) == 0 {
			detail = append(detail, "complete: substrate + recognition + projection verified to tip")
		}
		if err := store.UpsertCompletenessSnapshot(ctx, timescale.CompletenessSnapshot{
			Source: src.name, Genesis: genesis, Tip: tip,
			Watermark: w.Ledger, CoveragePct: w.CoveragePct, Complete: w.Complete,
			FirstProblem: w.FirstProblem,
			SubstrateOK:  substrateOK, RecognitionOK: recOK, ProjectionOK: projOK,
			Detail: strings.Join(detail, "; "),
		}); err != nil {
			return fmt.Errorf("%s: upsert snapshot: %w", src.name, err)
		}
		fmt.Fprintf(os.Stderr, "compute-completeness: %-14s watermark=%d coverage=%.4f complete=%v (%s)\n",
			src.name, w.Ledger, w.CoveragePct, w.Complete, strings.Join(detail, "; "))
	}

	// ── System recognition snapshot (gaps on contracts no source owns) ──
	if *only == "" {
		var earliest uint32
		for _, g := range unattributed {
			if earliest == 0 || g.MinLedger < earliest {
				earliest = g.MinLedger
			}
		}
		recW := completeness.ComputeWatermark(sorobanEraGenesis, tip, nilOrOne(earliest))
		detail := "no unrecognized event shapes on unowned contracts"
		if len(unattributed) > 0 {
			detail = fmt.Sprintf("%d unrecognized shape(s) on unowned contracts (earliest ledger %d) — run verify-recognition", len(unattributed), earliest)
		}
		if err := store.UpsertCompletenessSnapshot(ctx, timescale.CompletenessSnapshot{
			Source: "recognition", Genesis: sorobanEraGenesis, Tip: tip,
			Watermark: recW.Ledger, CoveragePct: recW.CoveragePct, Complete: recW.Complete,
			FirstProblem: recW.FirstProblem, SubstrateOK: true, RecognitionOK: len(unattributed) == 0, ProjectionOK: true,
			Detail: detail,
		}); err != nil {
			return fmt.Errorf("upsert recognition snapshot: %w", err)
		}
		fmt.Fprintf(os.Stderr, "compute-completeness: recognition  unattributed=%d coverage=%.4f\n", len(unattributed), recW.CoveragePct)
	}

	return nil
}

// reconcileSourceProjection reconciles every table a source writes over
// [genesis, hi] and returns the union of mismatched ledgers. SDEX uses
// the LCM census; event sources re-derive (by kind) and project each
// table's kinds.
func reconcileSourceProjection(ctx context.Context, store *timescale.Store, chStreamer completeness.EventStreamer, src reconSource, genesis, hi uint32) ([]uint32, error) {
	var mismatched []uint32
	if src.census {
		expected, eerr := store.ClassicTradeEffectCountsByLedger(ctx, genesis, hi)
		if eerr != nil {
			return nil, eerr
		}
		for _, tgt := range src.targets {
			actual, aerr := store.CountRowsByLedger(ctx, tgt.table, "ledger", tgt.whereFilter, genesis, hi)
			if aerr != nil {
				return nil, aerr
			}
			for _, g := range completeness.ReconcileCounts(expected, actual) {
				mismatched = append(mismatched, g.Ledger)
			}
		}
		return mismatched, nil
	}

	// Re-derive expected outputs: from the CH lake (certified, off the serving
	// DB) when -ch, else from Postgres soroban_events.
	var byKind map[string]map[uint32]int
	var derr error
	if chStreamer != nil {
		byKind, derr = completeness.ReDeriveOutputCountsByKindFromEvents(ctx, chStreamer, src.dec, src.contractIDs, src.topic0Syms, genesis, hi)
	} else {
		byKind, derr = completeness.ReDeriveOutputCountsByKind(ctx, store, src.dec, src.contractIDs, src.topic0Syms, genesis, hi)
	}
	if derr != nil {
		return nil, derr
	}
	for _, tgt := range src.targets {
		expected := completeness.SumKinds(byKind, tgt.kinds...)
		actual, aerr := store.CountRowsByLedger(ctx, tgt.table, "ledger", tgt.whereFilter, genesis, hi)
		if aerr != nil {
			return nil, aerr
		}
		for _, g := range completeness.ReconcileCounts(expected, actual) {
			mismatched = append(mismatched, g.Ledger)
		}
	}
	return mismatched, nil
}

// computeRecognitionGapsCH is the CH-backed recognition audit: distinct
// (contract, topic) shapes from the certified lake (excluding the CAP-67
// classic-token firehose — sep41 isn't enabled, so it's out of protocol scope)
// run through the dispatcher's Recognize(). Fast + off the serving DB vs the
// Postgres soroban_events scan in computeRecognitionGaps.
func computeRecognitionGapsCH(ctx context.Context, cfg config.Config, chAddr string, tip uint32) ([]completeness.RecognitionGap, error) {
	disp, err := pipeline.BuildDispatcher(cfg.Ingestion.EnabledSources, cfg.Oracle)
	if err != nil {
		return nil, fmt.Errorf("build dispatcher: %w", err)
	}
	shapes, err := clickhouse.DistinctTopicShapes(ctx, chAddr, sorobanEraGenesis, tip, clickhouse.ClassicTokenTopic0Syms)
	if err != nil {
		return nil, err
	}
	var gaps []completeness.RecognitionGap
	for _, s := range shapes {
		if _, ok := disp.Recognize(s.Event()); ok {
			continue
		}
		gaps = append(gaps, completeness.RecognitionGap{
			ContractID: s.ContractID,
			Topic0Sym:  s.Topic0Sym,
			Count:      int64(s.Count),
			MinLedger:  s.MinLedger,
			MaxLedger:  s.MaxLedger,
			Reason:     "no decoder matches",
		})
	}
	return gaps, nil
}

// computeRecognitionGaps runs the global recognition audit over the
// Soroban era and returns every unrecognized event shape.
func computeRecognitionGaps(ctx context.Context, store *timescale.Store, cfg config.Config, tip uint32) ([]completeness.RecognitionGap, error) {
	disp, err := pipeline.BuildDispatcher(cfg.Ingestion.EnabledSources, cfg.Oracle)
	if err != nil {
		return nil, fmt.Errorf("build dispatcher: %w", err)
	}
	samples, err := store.DistinctSorobanTopicSamples(ctx, sorobanEraGenesis, tip)
	if err != nil {
		return nil, err
	}
	return completeness.AuditRecognition(samples, disp), nil
}

func nilOrOne(v uint32) []uint32 {
	if v == 0 {
		return nil
	}
	return []uint32{v}
}
