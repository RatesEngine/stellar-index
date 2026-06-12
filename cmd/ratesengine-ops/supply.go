package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"math/big"
	"os"
	"text/tabwriter"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/timescale"
	"github.com/StellarAtlas/stellar-atlas/internal/supply"
)

// supplyCmd dispatches the supply sub-subcommand. v1 ships with two
// modes — `audit` (read) + `snapshot` (write); future modes
// (e.g. `recompute`, `policy-validate`) plug in here.
func supplyCmd(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: supply <audit|snapshot> [flags]")
	}
	switch args[0] {
	case "audit":
		return supplyAudit(args[1:])
	case "snapshot":
		return supplySnapshot(args[1:])
	default:
		return fmt.Errorf("unknown supply subcommand %q (expected: audit | snapshot)", args[0])
	}
}

// supplySnapshot computes a fresh supply snapshot and writes it to
// asset_supply_history. The CLI is intentionally native-XLM-only —
// Algorithm 2 (classic) and Algorithm 3 (SEP-41) computers shipped
// in Tasks #55 and #56 but their CLI surface is the aggregator-
// resident goroutine path (`[supply] aggregator_refresh_enabled`),
// not this subcommand. Per `docs/operations/supply-snapshot.md`
// §"Asset-class scope": the two refresh paths are mutually
// exclusive at the operator level, and the goroutine path is the
// canonical surface for non-XLM assets.
//
// Reserve balances come from the chained-fallback reader
// (live LCM AccountEntry observer wins when populated; operator-
// static `[supply] reserve_balances_stroops` is the bring-up
// fallback). The live observer was wired into the indexer
// dispatcher by L2.12a (PRs #411-#413).
//
// Flags:
//
//	-config PATH     Required. Operator TOML config.
//	-asset <id>      Asset to snapshot. `native` only via this CLI;
//	                 classic and SEP-41 are served by the
//	                 aggregator-resident goroutine path
//	                 (`[supply] aggregator_refresh_enabled`).
//	                 Passing a non-native asset returns an error
//	                 pointing at that path.
//	-ledger N        Ledger sequence to attribute the snapshot to.
//	                 Default: latest known ledger across all
//	                 ingestion cursors (so the snapshot is dated
//	                 against current chain state, not against the
//	                 wall-clock).
//	-dry-run         Compute + print but do not write.
func supplySnapshot(args []string) error {
	fs := flag.NewFlagSet("supply snapshot", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	assetRaw := fs.String("asset", "native", "Asset to snapshot (native only at v1)")
	ledgerArg := fs.Int("ledger", 0, "Ledger sequence to attribute to (default: latest from ingestion_cursors)")
	dryRun := fs.Bool("dry-run", false, "Compute + print without writing to asset_supply_history")
	textfileOut := fs.String("textfile-output", "", "Path to write Prometheus textfile (node_exporter textfile_collector format). Empty = no metrics emit.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("-config is required")
	}
	if *assetRaw != "native" {
		return fmt.Errorf("supply snapshot: asset %q is not served by this CLI subcommand — classic (Algorithm 2) + SEP-41 (Algorithm 3) computers ship in `internal/supply/{classic,sep41}.go` and run via the aggregator-resident goroutine path (`[supply] aggregator_refresh_enabled`). See docs/operations/supply-snapshot.md §\"Asset-class scope\"", *assetRaw)
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}
	if err := cfg.Supply.Validate(); err != nil {
		return fmt.Errorf("config: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	startedAt := time.Now()
	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return supplySnapshotMaybeEmitFailure(*textfileOut, *assetRaw, startedAt, err)
	}
	defer func() { _ = store.Close() }()

	ledger, observedAt, err := resolveSnapshotLedger(ctx, store, uint32(*ledgerArg))
	if err != nil {
		return supplySnapshotMaybeEmitFailure(*textfileOut, *assetRaw, startedAt, err)
	}

	staticReader, err := supply.NewConfigReserveBalanceReader(cfg.Supply.ReserveBalancesStroops)
	if err != nil {
		return supplySnapshotMaybeEmitFailure(*textfileOut, *assetRaw, startedAt, fmt.Errorf("reserve reader: %w", err))
	}
	// Live LCM reader tries first; ErrNoObservation falls back to
	// static config per ADR-0021. The supplyChainReader wraps both.
	reader := supplyChainReader{
		live:   supply.NewLCMReserveBalanceReader(supplyStoreLookup{s: store}),
		static: staticReader,
	}
	computer, err := supply.NewXLMComputer(cfg.Supply.SDFReserveAccounts, reader)
	if err != nil {
		return supplySnapshotMaybeEmitFailure(*textfileOut, *assetRaw, startedAt, fmt.Errorf("xlm computer: %w", err))
	}

	snap, err := computer.Compute(ctx, ledger, observedAt)
	if err != nil {
		return supplySnapshotMaybeEmitFailure(*textfileOut, *assetRaw, startedAt, fmt.Errorf("compute: %w", err))
	}

	printSupplySnapshot("SNAPSHOT", "native", snap.AssetKey, snap)
	if *dryRun {
		fmt.Println("─── DRY RUN ─── snapshot NOT written to asset_supply_history.")
		return nil
	}
	if err := store.InsertSupply(ctx, snap); err != nil {
		return supplySnapshotMaybeEmitFailure(*textfileOut, *assetRaw, startedAt, fmt.Errorf("InsertSupply: %w", err))
	}
	fmt.Printf("Wrote snapshot for asset_key=%s ledger=%d basis=%s\n",
		snap.AssetKey, snap.LedgerSequence, snap.Basis)

	if *textfileOut != "" {
		if err := supply.WriteSnapshotTextfile(*textfileOut, snap, time.Since(startedAt).Seconds(), true); err != nil {
			return fmt.Errorf("write textfile: %w", err)
		}
	}
	return nil
}

// supplyStoreLookup adapts *timescale.Store to
// supply.AccountObservationLookup. The timescale row carries a
// pointer-or-NULL HomeDomain that the supply reader doesn't need;
// we project to the smaller AccountObservationRow shape.
type supplyStoreLookup struct{ s *timescale.Store }

func (a supplyStoreLookup) LatestAccountObservationAtOrBefore(ctx context.Context, accountID string, asOfLedger uint32) (supply.AccountObservationRow, error) {
	row, err := a.s.LatestAccountObservationAtOrBefore(ctx, accountID, asOfLedger)
	if err != nil {
		return supply.AccountObservationRow{}, err
	}
	return supply.AccountObservationRow{
		Balance:   row.Balance,
		IsRemoval: row.IsRemoval,
		Ledger:    row.Ledger,
	}, nil
}

// supplyChainReader composes the live LCM reader with the
// operator-static config reader. Tries live first; on
// ErrNoObservation (any account in the request set has no
// observation, OR a transient storage error) falls through to
// the static reader for the whole call. Per ADR-0021 we don't
// mix live + static within one call — that would silently produce
// a partially-fresh sum the operator can't audit.
type supplyChainReader struct {
	live   supply.ReserveBalanceReader
	static supply.ReserveBalanceReader
}

func (c supplyChainReader) ReserveBalanceTotal(ctx context.Context, accounts []string, ledger uint32) (*big.Int, error) {
	out, err := c.live.ReserveBalanceTotal(ctx, accounts, ledger)
	if err == nil {
		return out, nil
	}
	if errors.Is(err, supply.ErrNoObservation) {
		// Drop to static. The static reader's own error path
		// (missing-balance, parse error) bubbles up unchanged
		// because that's an operator-config error, not a transient
		// LCM gap.
		return c.static.ReserveBalanceTotal(ctx, accounts, ledger)
	}
	return nil, err
}

// MinReserveAccountLedger forwards the freshness probe to the
// live reader when it implements [supply.ReserveBalanceFreshnessReader].
// The static fallback reader has no per-ledger freshness concept;
// when the live reader can't satisfy the probe (or doesn't implement
// the interface) we return 0 — the gate-permissive bypass — so the
// supply-snapshot subcommand stays on the legacy posture for static
// deployments. F-1236 (codex audit-2026-05-12).
func (c supplyChainReader) MinReserveAccountLedger(ctx context.Context, accounts []string, ledger uint32) (uint32, error) {
	if fr, ok := c.live.(supply.ReserveBalanceFreshnessReader); ok {
		got, err := fr.MinReserveAccountLedger(ctx, accounts, ledger)
		if err == nil {
			return got, nil
		}
		if errors.Is(err, supply.ErrNoObservation) {
			return 0, nil
		}
		return 0, err
	}
	return 0, nil
}

// supplySnapshotMaybeEmitFailure writes a fail-marker textfile (so
// the staleness alert keys on time-since-last-pass) and returns
// the original error. Empty textfileOut skips the emit but still
// returns the error.
func supplySnapshotMaybeEmitFailure(textfileOut, assetRaw string, startedAt time.Time, cause error) error {
	if textfileOut != "" {
		// Best-effort fail emit — don't mask the original cause.
		_ = supply.WriteSnapshotFailureTextfile(textfileOut, assetRaw, time.Since(startedAt).Seconds())
	}
	return cause
}

// resolveSnapshotLedger picks the ledger to attribute the snapshot
// to. Operator-supplied -ledger wins; otherwise we use the max
// last_ledger across all ingestion cursors.
//
// F-1236 (codex audit-2026-05-12) — KNOWN: a stalled supply
// observer can leave a component balance behind the snapshot
// ledger; the per-component reader's at-or-before query silently
// returns an older row. The matching long-form note lives on
// `supplyAggregatorLedgers` in cmd/ratesengine-aggregator/main.go
// — full fix needs per-component ledger threading into snapshot
// acceptance (the per-row Ledger is already returned by
// AccountObservationRow et al, so it's a refactor of the Refresher
// + Supply shapes, not a new storage primitive).
func resolveSnapshotLedger(ctx context.Context, store *timescale.Store, opLedger uint32) (uint32, time.Time, error) {
	if opLedger > 0 {
		return opLedger, time.Now().UTC(), nil
	}
	cursors, err := store.ListCursors(ctx)
	if err != nil {
		return 0, time.Time{}, fmt.Errorf("ListCursors: %w", err)
	}
	var maxLedger uint32
	for _, c := range cursors {
		if c.LastLedger > maxLedger {
			maxLedger = c.LastLedger
		}
	}
	if maxLedger == 0 {
		return 0, time.Time{}, errors.New("no ingestion cursors yet — pass -ledger explicitly until the indexer has produced a cursor")
	}
	return maxLedger, time.Now().UTC(), nil
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
		return supply.Supply{}, fmt.Errorf("no supply snapshot for %s (key %s) — populate asset_supply_history via your supply snapshot writer before using this audit", displayName, assetKey)
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
