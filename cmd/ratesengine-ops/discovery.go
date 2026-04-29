package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
)

// discoveryCmd dispatches the discovery sub-subcommand. v1 ships
// with one mode (`list`); future modes (e.g. `prune`, `flag`) plug
// in here without changing the top-level dispatch.
func discoveryCmd(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: discovery list [flags]")
	}
	switch args[0] {
	case "list":
		return discoveryList(args[1:])
	default:
		return fmt.Errorf("unknown discovery subcommand %q (expected: list)", args[0])
	}
}

// discoveryList queries `discovered_assets` and prints rows in
// ascending arrival order (newest first by first_seen_at) so an
// operator scanning the output sees fresh contracts up top.
//
// Flags:
//
//	-config PATH    Required. TOML config file (for Postgres DSN).
//	-since DUR      Filter to contracts first seen within the
//	                supplied duration (e.g. 1h, 24h). Empty (default)
//	                returns the full table subject to -limit.
//	-limit N        Cap result rows. Default 100. 0 means no cap.
//
// Output is a tab-separated columnar dump that aligns sensibly when
// piped through `column -t`. Stable column order so log-scrapers can
// build automation around it.
func discoveryList(args []string) error {
	fs := flag.NewFlagSet("discovery list", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	since := fs.Duration("since", 0,
		"Only contracts first seen within this duration (e.g. 1h, 24h). 0 = no filter.")
	limit := fs.Int("limit", 100, "Maximum rows to print. 0 = no cap.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return fmt.Errorf("-config is required")
	}
	if *limit < 0 {
		return fmt.Errorf("-limit must be ≥ 0 (got %d)", *limit)
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

	rows, err := store.ListDiscovered(ctx, *limit)
	if err != nil {
		return fmt.Errorf("ListDiscovered: %w", err)
	}

	// Apply -since in Go rather than at SQL level so the storage
	// layer's ListDiscovered stays simple. discovered_assets is
	// small (per-contract, not per-event), so client-side filtering
	// has no measurable cost.
	if *since > 0 {
		cutoff := time.Now().UTC().Add(-*since)
		filtered := make([]timescale.DiscoveredAsset, 0, len(rows))
		for _, r := range rows {
			if r.FirstSeenAt.After(cutoff) {
				filtered = append(filtered, r)
			}
		}
		rows = filtered
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if _, err := fmt.Fprintln(w, "CONTRACT_ID\tFIRST_SEEN_AT\tFIRST_EVENT\tLAST_LEDGER\tEVENT_COUNT"); err != nil {
		return fmt.Errorf("write header: %w", err)
	}
	for _, r := range rows {
		if _, err := fmt.Fprintf(w, "%s\t%s\t%s\t%d\t%d\n",
			r.ContractID,
			r.FirstSeenAt.UTC().Format(time.RFC3339),
			r.FirstSeenEvent,
			r.LastSeenLedger,
			r.EventCount,
		); err != nil {
			return fmt.Errorf("write row: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("flush stdout: %w", err)
	}
	fmt.Fprintf(os.Stderr, "\ndiscovery: %d contract(s) listed\n", len(rows))
	return nil
}
