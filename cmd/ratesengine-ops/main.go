// Binary ratesengine-ops is the admin CLI: backfill, gap-detect,
// cache-prime, docs-config, and other operational tasks that don't
// belong in the long-running binaries.
//
// Subcommands land alongside the features they support. Today only
// `docs-config` is wired; the rest land with the corresponding
// implementation PRs.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

func main() {
	args := os.Args[1:]
	if len(args) == 0 {
		printUsage()
		os.Exit(2)
	}

	switch args[0] {
	case "docs-config":
		if err := config.EmitMarkdown(os.Stdout); err != nil {
			fmt.Fprintf(os.Stderr, "docs-config: %v\n", err)
			os.Exit(1)
		}
	case "rpc-probe":
		endpoint := "http://127.0.0.1:8000"
		if len(args) > 1 {
			endpoint = args[1]
		}
		if err := rpcProbe(endpoint); err != nil {
			fmt.Fprintf(os.Stderr, "rpc-probe: %v\n", err)
			os.Exit(1)
		}
	case "list-cursors":
		if err := listCursors(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "list-cursors: %v\n", err)
			os.Exit(1)
		}
	case "detect-gaps":
		if err := detectGaps(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "detect-gaps: %v\n", err)
			os.Exit(1)
		}
	case "version", "--version", "-v":
		fmt.Println(version.String())
	case "help", "--help", "-h":
		printUsage()
	default:
		// TODO(#0): backfill, cache-prime, verify-invariants.
		fmt.Fprintf(os.Stderr, "ratesengine-ops: unknown subcommand %q\n", args[0])
		printUsage()
		os.Exit(2)
	}
}

func printUsage() {
	fmt.Fprintf(os.Stderr, `ratesengine-ops %s

Usage:
  ratesengine-ops <subcommand>

Subcommands:
  docs-config             Emit the generated config reference to stdout.
  rpc-probe [endpoint]    Diagnostic probe against a stellar-rpc endpoint.
                          Default: http://127.0.0.1:8000.
  list-cursors -config PATH
                          Print every source's last-indexed ledger + age.
  detect-gaps -config PATH [-threshold N]
                          Report sources lagging more than N ledgers (default 100)
                          behind the stellar-rpc network tip. Exit code 1 if any
                          source is lagging.
  version                 Print version + build date.
  help                    This help.

TODO subcommands (land with their feature PRs):
  backfill                Replay a ledger range into the trades hypertable.
  cache-prime             Warm the Redis hot-path cache from Timescale.
  verify-invariants       Cross-check aggregated prices against divergence.
`, version.String())
}

// listCursors loads the storage layer and prints every per-source
// ingestion cursor — source, sub (pair contract or ""), last ledger,
// and age of the last update.
//
// Operators use this to spot lagging sources without needing psql
// or a dashboard. Empty output means no source has written a cursor
// yet, which usually indicates a fresh deploy.
func listCursors(args []string) error {
	fs := flag.NewFlagSet("list-cursors", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
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

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	cursors, err := store.ListCursors(ctx)
	if err != nil {
		return err
	}
	if len(cursors) == 0 {
		fmt.Println("(no cursors stored — fresh deploy or ingestion hasn't written yet)")
		return nil
	}

	now := time.Now().UTC()
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tSUB\tLAST LEDGER\tAGE\tUPDATED")
	for _, c := range cursors {
		sub := c.Sub
		if sub == "" {
			sub = "-"
		}
		age := now.Sub(c.UpdatedAt.UTC()).Round(time.Second)
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\t%s\n",
			c.Source, sub, c.LastLedger, age, c.UpdatedAt.Format(time.RFC3339))
	}
	return w.Flush()
}

// detectGaps compares every per-source cursor against the
// stellar-rpc network tip and reports any source lagging by more
// than `threshold` ledgers. Exits non-zero when at least one source
// is lagging so the command works as a prometheus-style health
// probe from a cron / k8s Job.
//
// For sources that track multiple sub-cursors (Soroswap per-pair
// cursors), the MINIMUM last-ledger across the source's rows is
// used — we care about the slowest position, not the fastest.
func detectGaps(args []string) error {
	fs := flag.NewFlagSet("detect-gaps", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	threshold := fs.Uint("threshold", 100, "Ledgers of lag that count as a gap")
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Pick the first RPC endpoint to query for tip. Failover across
	// the full list is the long-running binaries' job; this is a
	// one-shot probe and we keep it simple.
	if len(cfg.Stellar.RPCEndpoints) == 0 {
		return fmt.Errorf("stellar.rpc_endpoints is empty")
	}
	rpc := stellarrpc.New(cfg.Stellar.RPCEndpoints[0], stellarrpc.WithTimeout(5*time.Second))
	tip, err := rpc.LatestLedger(ctx)
	if err != nil {
		return fmt.Errorf("rpc: %w", err)
	}

	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	cursors, err := store.ListCursors(ctx)
	if err != nil {
		return err
	}

	// Per-source min across sub_source rows.
	minBySource := map[string]uint32{}
	for _, c := range cursors {
		if cur, ok := minBySource[c.Source]; !ok || c.LastLedger < cur {
			minBySource[c.Source] = c.LastLedger
		}
	}

	if len(minBySource) == 0 {
		fmt.Printf("(no cursors stored — nothing to check against tip %d)\n", tip.Sequence)
		return nil
	}

	var lagging []string
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "SOURCE\tLAST LEDGER\tTIP\tLAG\tSTATUS\n")
	// Sorted iteration so output is reproducible across invocations
	// — operators pipe into diff / grep and expect stable ordering.
	sources := make([]string, 0, len(minBySource))
	for s := range minBySource {
		sources = append(sources, s)
	}
	sort.Strings(sources)
	for _, source := range sources {
		last := minBySource[source]
		lag := uint32(0)
		if tip.Sequence > last {
			lag = tip.Sequence - last
		}
		status := "ok"
		if lag > uint32(*threshold) {
			status = "LAGGING"
			lagging = append(lagging, source)
		}
		fmt.Fprintf(w, "%s\t%d\t%d\t%d\t%s\n", source, last, tip.Sequence, lag, status)
	}
	_ = w.Flush()

	if len(lagging) > 0 {
		return fmt.Errorf("%d source(s) lagging past threshold %d: %v",
			len(lagging), *threshold, lagging)
	}
	return nil
}

// rpcProbe runs a one-shot diagnostic against a stellar-rpc endpoint:
// getHealth, getLatestLedger, getNetwork, getVersionInfo, getFeeStats.
// Prints a human-readable report to stdout + returns the first fatal
// error (e.g. endpoint unreachable). Stale-rpc is not fatal — it's
// reported in the staleness line.
func rpcProbe(endpoint string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	c := stellarrpc.New(endpoint, stellarrpc.WithTimeout(5*time.Second))
	fmt.Printf("stellar-rpc probe — %s\n\n", c.Endpoint())

	// getVersionInfo first — cheapest and tells us we can reach the
	// thing at all. On failure, print actionable context before
	// propagating the error so an operator running `rpc-probe` at
	// 3 AM sees WHY the connection failed rather than just a Go
	// error string.
	vi, err := c.VersionInfo(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\n❌ cannot reach stellar-rpc at %s\n", endpoint)
		fmt.Fprintf(os.Stderr, "   error: %v\n", err)
		fmt.Fprintf(os.Stderr, "\n   Likely causes, in order:\n")
		fmt.Fprintf(os.Stderr, "     1. URL scheme/port wrong (expected http://<host>:8000)\n")
		fmt.Fprintf(os.Stderr, "     2. stellar-rpc process not running on that host\n")
		fmt.Fprintf(os.Stderr, "     3. Firewall / NetworkPolicy blocks outbound\n")
		fmt.Fprintf(os.Stderr, "     4. DNS for %q doesn't resolve\n", endpoint)
		return fmt.Errorf("getVersionInfo: %w", err)
	}
	fmt.Printf("  version:         %s\n", vi.Version)
	fmt.Printf("  commitHash:      %s\n", vi.CommitHash)
	fmt.Printf("  captiveCore:     %s\n", vi.CaptiveCoreVersion)
	fmt.Printf("  protocolVersion: %d\n\n", vi.ProtocolVersion)

	net, err := c.Network(ctx)
	if err != nil {
		return fmt.Errorf("getNetwork: %w", err)
	}
	fmt.Printf("  network:         %s (protocol %d)\n\n", net.Passphrase, net.ProtocolVersion)

	// Health returns an error envelope on stale — report, don't fail.
	if _, err := c.Health(ctx); err != nil {
		fmt.Printf("  health:          ⚠ %v\n", err)
	} else {
		fmt.Printf("  health:          ✓ healthy\n")
	}

	l, err := c.LatestLedger(ctx)
	if err != nil {
		return fmt.Errorf("getLatestLedger: %w", err)
	}
	fmt.Printf("  latestLedger:    %d (closeTime %s, id %s…)\n\n", l.Sequence, l.CloseTime, shortHex(l.ID, 12))

	fs, err := c.FeeStats(ctx)
	if err != nil {
		fmt.Printf("  getFeeStats:     ⚠ %v\n", err)
	} else {
		fmt.Printf("  fees (classic):  min=%s mode=%s p99=%s (%d ledgers)\n",
			fs.InclusionFee.Min, fs.InclusionFee.Mode, fs.InclusionFee.P99, fs.InclusionFee.LedgerCount)
		fmt.Printf("  fees (soroban):  min=%s mode=%s p99=%s (%d ledgers)\n",
			fs.SorobanInclusionFee.Min, fs.SorobanInclusionFee.Mode, fs.SorobanInclusionFee.P99,
			fs.SorobanInclusionFee.LedgerCount)
	}

	// Range of events available — 1-event probe from just before tip
	// so we know the retention window.
	start := l.Sequence - 1
	er, err := c.GetEvents(ctx, start, 0, nil, &stellarrpc.Pagination{Limit: 1})
	if err != nil {
		fmt.Printf("\n  getEvents:       ⚠ %v\n", err)
	} else {
		window := er.LatestLedger - er.OldestLedger
		fmt.Printf("\n  events window:   oldest=%d  latest=%d  (~%d ledgers ≈ %.1f d at 5s cadence)\n",
			er.OldestLedger, er.LatestLedger, window, float64(window)*5/86400)
		if len(er.Events) > 0 {
			fmt.Printf("  sample event:    contract=%s… type=%s topics=%d\n",
				shortHex(er.Events[0].ContractID, 12), er.Events[0].Type, len(er.Events[0].Topic))
		}

		// getTransaction round-trip against the sample event's tx
		// hash. Proves the RPC's tx retention window covers at least
		// the current tip — sources rely on this to decode tx-level
		// context (observer account, envelope XDR).
		if len(er.Events) > 0 && er.Events[0].TxHash != "" {
			tx, err := c.GetTransaction(ctx, er.Events[0].TxHash)
			switch {
			case err != nil:
				fmt.Printf("  getTransaction:  ⚠ %v\n", err)
			case tx.Status == stellarrpc.TxStatusNotFound:
				// Should not happen for a tx we JUST saw in getEvents,
				// but surfaces any retention-window mismatch.
				fmt.Printf("  getTransaction:  ⚠ tx %s… not found (retention window mismatch)\n",
					shortHex(er.Events[0].TxHash, 8))
			default:
				fmt.Printf("  getTransaction:  ✓ status=%s ledger=%d appOrder=%d\n",
					tx.Status, tx.Ledger, tx.ApplicationOrder)
			}
		}
	}

	fmt.Println()
	return nil
}

// shortHex returns the first `n` characters of s, or s if it is
// already shorter. Guards the probe against panicking on a
// malformed-RPC response whose ID/hash is shorter than expected —
// a diagnostic binary should never crash on bad input, it should
// report whatever it got.
func shortHex(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
