package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/RatesEngine/rates-engine/internal/completeness"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// verifyRecognition implements ADR-0033 Claim 2a (recognition): it
// pulls every distinct (contract_id, topic_0_sym) shape present in
// soroban_events over a ledger range and runs each through the
// production decoder chain's Matches(). Any shape no decoder claims is
// a recognition gap — an on-chain event we would silently drop, which
// is exactly what a WASM upgrade that adds a topic looks like.
//
// Exit code is non-zero when any gap exists, so cron / CI can gate on
// it. Uses the same dispatcher the indexer builds from
// ingestion.enabled_sources, so the verdict reflects what r1 actually
// handles — not a hand-maintained topic list that could drift.
func verifyRecognition(args []string) error {
	fs := flag.NewFlagSet("verify-recognition", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
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

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage open: %w", err)
	}
	defer func() { _ = store.Close() }()

	disp, err := pipeline.BuildDispatcher(cfg.Ingestion.EnabledSources, cfg.Oracle)
	if err != nil {
		return fmt.Errorf("build dispatcher: %w", err)
	}

	samples, err := store.DistinctSorobanTopicSamples(ctx, uint32(*from), uint32(*to))
	if err != nil {
		return fmt.Errorf("distinct topic samples: %w", err)
	}
	fmt.Fprintf(os.Stderr, "verify-recognition: %d distinct (contract, topic) shape(s) in ledgers [%d, %d]\n",
		len(samples), *from, *to)

	gaps := completeness.AuditRecognition(samples, disp)
	if len(gaps) == 0 {
		fmt.Fprintln(os.Stderr, "verify-recognition: OK — every on-chain event shape is recognized by a decoder")
		return nil
	}

	fmt.Fprintf(os.Stderr, "verify-recognition: %d UNRECOGNIZED event shape(s):\n", len(gaps))
	for _, g := range gaps {
		sym := g.Topic0Sym
		if sym == "" {
			sym = "(non-symbol topic[0])"
		}
		fmt.Fprintf(os.Stdout, "  contract=%s topic0=%q count=%d ledgers=[%d,%d] — %s\n",
			g.ContractID, sym, g.Count, g.MinLedger, g.MaxLedger, g.Reason)
	}
	return fmt.Errorf("%d unrecognized event shape(s) — a decoder is missing a topic (ADR-0033 EVERY-event policy)", len(gaps))
}
