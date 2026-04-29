package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/supply"
)

// supplyCmd dispatches the supply sub-subcommand. v1 ships with one
// mode (`audit`); future modes (e.g. `recompute`, `policy-validate`)
// plug in here.
func supplyCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: supply audit <asset> [flags]")
	}
	switch args[0] {
	case "audit":
		return supplyAudit(args[1:])
	default:
		return fmt.Errorf("unknown supply subcommand %q (expected: audit)", args[0])
	}
}

// supplyAudit prints the latest supply snapshot for an asset, plus
// optional history trail and SAC-wrapped cross-check. Used during
// supply-divergence triage per docs/operations/runbooks/
// supply-cross-check-divergence.md.
//
// Asset is the first positional arg in canonical wire form
// (native | CODE-ISSUER | <C-strkey>). Flags:
//
//	-config PATH            Required. TOML config (Postgres DSN).
//	-cross-check <asset>    Other asset (typically the SAC
//	                        counterpart of a classic, or vice
//	                        versa). When set, fetches its snapshot
//	                        and runs supply.CrossCheck per ADR-0011.
//	-history-hours N        Print the trailing N-hour snapshot
//	                        window so an operator sees the trend.
//	                        Default 0 (latest only).
func supplyAudit(args []string) error {
	fs := flag.NewFlagSet("supply audit", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	crossCheck := fs.String("cross-check", "",
		"Counterpart asset to compare against (canonical wire form). "+
			"Typically the SAC contract id of a classic asset, or vice versa.")
	historyHours := fs.Int("history-hours", 0,
		"Trailing window of historical snapshots to print (hours). 0 = latest only.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() == 0 {
		return errors.New("usage: supply audit <asset> [-config PATH] [-cross-check <asset>] [-history-hours N]")
	}
	if *cfgPath == "" {
		return errors.New("-config is required")
	}
	if *historyHours < 0 {
		return fmt.Errorf("-history-hours must be ≥ 0 (got %d)", *historyHours)
	}

	primaryRaw := fs.Arg(0)
	primary, err := canonical.ParseAsset(primaryRaw)
	if err != nil {
		return fmt.Errorf("parse asset %q: %w", primaryRaw, err)
	}
	primaryKey, err := supply.AssetKey(primary)
	if err != nil {
		return fmt.Errorf("asset %s has no supply key: %w", primaryRaw, err)
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	primarySnap, err := fetchSupplyOrReport(ctx, store, primaryKey, primaryRaw)
	if err != nil {
		return err
	}
	printSupplySnapshot("PRIMARY", primaryRaw, primaryKey, primarySnap)

	if *historyHours > 0 {
		if err := printSupplyHistory(ctx, store, primaryKey, *historyHours); err != nil {
			return err
		}
	}

	if *crossCheck != "" {
		if err := runSupplyCrossCheck(ctx, store, *crossCheck, primaryKey, primarySnap); err != nil {
			return err
		}
	}
	return nil
}

// fetchSupplyOrReport fetches the latest supply for assetKey,
// returning a typed error when no snapshot exists (so the caller
// can decide whether absence is a hard fail or just informational).
// At v1 we treat absence as a hard fail — the audit subcommand only
// makes sense when the asset has been observed.
func fetchSupplyOrReport(ctx context.Context, store *timescale.Store, assetKey, displayName string) (supply.Supply, error) {
	snap, err := store.LatestSupply(ctx, assetKey)
	if errors.Is(err, timescale.ErrNotFound) {
		return supply.Supply{}, fmt.Errorf("no supply snapshot for %s (key %s) — run the orchestrator or check the indexer is wired", displayName, assetKey)
	}
	if err != nil {
		return supply.Supply{}, fmt.Errorf("LatestSupply %s: %w", assetKey, err)
	}
	return snap, nil
}

// printSupplySnapshot writes a labelled block for one snapshot.
// Stable layout so log-scrapers can pick fields out by line.
func printSupplySnapshot(label, displayName, key string, snap supply.Supply) {
	fmt.Printf("─── %s: %s ───\n", label, displayName)
	fmt.Printf("  asset_key:        %s\n", key)
	fmt.Printf("  total_supply:     %s\n", snap.TotalSupply.String())
	fmt.Printf("  circulating:      %s\n", snap.CirculatingSupply.String())
	if snap.MaxSupply == nil {
		fmt.Printf("  max_supply:       (null — uncapped)\n")
	} else {
		fmt.Printf("  max_supply:       %s\n", snap.MaxSupply.String())
	}
	fmt.Printf("  basis:            %s\n", snap.Basis)
	fmt.Printf("  ledger_sequence:  %d\n", snap.LedgerSequence)
	fmt.Printf("  observed_at:      %s\n", snap.ObservedAt.UTC().Format(time.RFC3339))
	fmt.Println()
}

// printSupplyHistory prints the trailing-N-hour snapshot window for
// the asset. Used by the runbook's "is this divergence fresh or
// chronic" question.
func printSupplyHistory(ctx context.Context, store *timescale.Store, assetKey string, hours int) error {
	now := time.Now().UTC()
	from := now.Add(-time.Duration(hours) * time.Hour)
	rows, err := store.SupplyHistory(ctx, assetKey, from, now, 0)
	if err != nil {
		return fmt.Errorf("SupplyHistory %s: %w", assetKey, err)
	}
	if len(rows) == 0 {
		fmt.Printf("─── HISTORY (last %dh): no snapshots ───\n\n", hours)
		return nil
	}
	fmt.Printf("─── HISTORY (last %dh, %d snapshots) ───\n", hours, len(rows))
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "TIME\tLEDGER\tBASIS\tTOTAL\tCIRCULATING\tMAX"); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	for _, r := range rows {
		maxStr := "(null)"
		if r.MaxSupply != nil {
			maxStr = r.MaxSupply.String()
		}
		if _, err := fmt.Fprintf(w, "%s\t%d\t%s\t%s\t%s\t%s\n",
			r.ObservedAt.UTC().Format(time.RFC3339),
			r.LedgerSequence,
			r.Basis,
			r.TotalSupply.String(),
			r.CirculatingSupply.String(),
			maxStr,
		); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush: %w", err)
	}
	fmt.Println()
	return nil
}

// runSupplyCrossCheck fetches the counterpart's snapshot and runs
// supply.CrossCheck. Per ADR-0011 the two totals must agree within
// 1 stroop; divergence > 1 surfaces with the same wording the
// supply-cross-check-divergence runbook uses, so the operator can
// pattern-match against the alert text.
func runSupplyCrossCheck(ctx context.Context, store *timescale.Store, otherRaw, primaryKey string, primarySnap supply.Supply) error {
	otherAsset, err := canonical.ParseAsset(otherRaw)
	if err != nil {
		return fmt.Errorf("parse cross-check asset %q: %w", otherRaw, err)
	}
	otherKey, err := supply.AssetKey(otherAsset)
	if err != nil {
		return fmt.Errorf("cross-check asset %s has no supply key: %w", otherRaw, err)
	}
	otherSnap, err := fetchSupplyOrReport(ctx, store, otherKey, otherRaw)
	if err != nil {
		return err
	}
	printSupplySnapshot("CROSS-CHECK", otherRaw, otherKey, otherSnap)

	result, err := supply.CrossCheck(primarySnap, otherSnap)
	if err != nil {
		return fmt.Errorf("CrossCheck: %w", err)
	}

	fmt.Println("─── CROSS-CHECK RESULT ───")
	fmt.Printf("  primary_total:        %s\n", result.ClassicTotal.String())
	fmt.Printf("  counterpart_total:    %s\n", result.SACTotal.String())
	fmt.Printf("  divergence_stroops:   %s\n", result.DivergenceStroops.String())
	if result.WithinTolerance {
		fmt.Printf("  status:               WITHIN TOLERANCE ✓ (≤ %s stroop per ADR-0011)\n",
			supply.CrossCheckTolerance.String())
	} else {
		fmt.Printf("  status:               OVER TOLERANCE ✗ — investigate per supply-cross-check-divergence runbook\n")
		fmt.Printf("  alert label:          classic_key=\"%s\"\n", primaryKey)
		fmt.Println("  next action:          ratesengine-ops supply audit <asset> -history-hours 24 to identify when divergence appeared")
	}
	fmt.Println()

	if !result.WithinTolerance {
		return errors.New("cross-check failed — divergence exceeds 1 stroop tolerance")
	}
	return nil
}
