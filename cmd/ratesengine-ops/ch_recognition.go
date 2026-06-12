package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"sort"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/storage/clickhouse"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// chRecognition is the ClickHouse-backed ADR-0033 Claim 2a (recognition) audit.
// It pulls every distinct (contract_id, topic_0_sym) shape from the CH lake —
// the complete, certified authoritative source, so no Postgres soroban_events
// scan and no serving-DB load — and runs each through the production decoder
// chain's Matches(). Any shape no decoder claims is a recognition gap: an
// on-chain event the system would silently drop (a WASM upgrade adding a topic,
// or a decoder that never handled a topic its protocol emits). This is the
// "every event for every protocol" gate.
//
// The CAP-67 classic-token firehose (transfer/mint/burn/…) is excluded by
// default: the enabled protocol decoders don't claim it (sep41 isn't enabled),
// so including it would report the entire SAC layer as "unrecognized" and drown
// the protocol signal. Pass -include-firehose to audit it too.
func chRecognition(args []string) error { //nolint:gocognit,funlen // linear: parse, scan, recognize, report.
	fs := flag.NewFlagSet("ch-recognition", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "path to ratesengine.toml (required)")
	from := fs.Uint("from", 2, "first ledger (inclusive)")
	to := fs.Uint("to", 0, "last ledger (inclusive); 0 = CH max ledger")
	chAddr := fs.String("ch-addr", "127.0.0.1:9300", "ClickHouse native address")
	includeFirehose := fs.Bool("include-firehose", false, "also audit CAP-67 classic-token topics (off by default — sep41 not enabled)")
	topN := fs.Int("top", 60, "print the top-N unrecognized shapes by event count")
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
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	// Warm the factory-anchored gated registries (ADR-0035) read-only from
	// protocol_contracts so real protocol children are recognized and a
	// FOREIGN emitter of the same topic is flagged. The recognition shapes
	// come from CH, but the child-contract registry lives in Postgres, so
	// we open a short-lived store just to warm it. Requires
	// protocol_contracts seeded (`seed-protocol-contracts`).
	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage open (gated registry warm): %w", err)
	}
	defer func() { _ = store.Close() }()
	gatedOpts, err := pipeline.GatedRegistryOptions(ctx, store, slog.Default(), ctx, false)
	if err != nil {
		return fmt.Errorf("gated registry warm: %w", err)
	}

	disp, err := pipeline.BuildDispatcher(cfg.Ingestion.EnabledSources, cfg.Oracle, gatedOpts)
	if err != nil {
		return fmt.Errorf("build dispatcher: %w", err)
	}

	hi := uint32(*to)
	if hi == 0 {
		hi, err = clickhouse.MaxLedger(ctx, *chAddr)
		if err != nil {
			return err
		}
	}
	var exclude []string
	if !*includeFirehose {
		exclude = clickhouse.ClassicTokenTopic0Syms
	}

	fmt.Fprintf(os.Stderr, "ch-recognition: scanning distinct (contract,topic) shapes in [%d,%d] (exclude-firehose=%v)\n", *from, hi, !*includeFirehose)
	shapes, err := clickhouse.DistinctTopicShapes(ctx, *chAddr, uint32(*from), hi, exclude)
	if err != nil {
		return err
	}

	var (
		gaps        []clickhouse.TopicShape
		recognized  int
		totalEvents uint64
		gapEvents   uint64
		bySym       = map[string]struct {
			contracts int
			events    uint64
		}{}
	)
	for _, s := range shapes {
		totalEvents += s.Count
		if _, ok := disp.Recognize(s.Event()); ok {
			recognized++
			continue
		}
		gaps = append(gaps, s)
		gapEvents += s.Count
		e := bySym[s.Topic0Sym]
		e.contracts++
		e.events += s.Count
		bySym[s.Topic0Sym] = e
	}

	fmt.Printf("\n=== ch-recognition [%d,%d] ===\n", *from, hi)
	fmt.Printf("distinct shapes: %d  recognized: %d  UNRECOGNIZED: %d\n", len(shapes), recognized, len(gaps))
	fmt.Printf("events: total=%d  unrecognized=%d (%.3f%%)\n", totalEvents, gapEvents,
		pctOf(gapEvents, totalEvents))

	if len(gaps) == 0 {
		fmt.Println("OK — every protocol event shape is recognized by a decoder.")
		return nil
	}

	// Aggregated by topic[0] symbol — the actionable list (which symbols a
	// decoder is missing), highest event volume first.
	type symRow struct {
		sym       string
		contracts int
		events    uint64
	}
	symRows := make([]symRow, 0, len(bySym))
	for sym, v := range bySym {
		symRows = append(symRows, symRow{sym, v.contracts, v.events})
	}
	sort.Slice(symRows, func(i, j int) bool { return symRows[i].events > symRows[j].events })
	fmt.Printf("\nunrecognized topic[0] symbols (by event volume):\n%-32s %10s %10s\n", "topic_0_sym", "events", "contracts")
	for _, r := range symRows {
		sym := r.sym
		if sym == "" {
			sym = "(non-symbol)"
		}
		fmt.Printf("%-32s %10d %10d\n", sym, r.events, r.contracts)
	}

	// Highest-volume individual shapes (contract + topic) for drill-down.
	sort.Slice(gaps, func(i, j int) bool { return gaps[i].Count > gaps[j].Count })
	fmt.Printf("\ntop %d unrecognized shapes (contract, topic):\n", *topN)
	for i, g := range gaps {
		if i >= *topN {
			fmt.Printf("… %d more\n", len(gaps)-*topN)
			break
		}
		sym := g.Topic0Sym
		if sym == "" {
			sym = "(non-symbol)"
		}
		fmt.Printf("  %s  topic0=%-24q events=%d ledgers=[%d,%d]\n", g.ContractID, sym, g.Count, g.MinLedger, g.MaxLedger)
	}
	return nil
}

func pctOf(n, d uint64) float64 {
	if d == 0 {
		return 0
	}
	return 100 * float64(n) / float64(d)
}
