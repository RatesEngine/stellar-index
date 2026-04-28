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
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"text/tabwriter"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/support/datastore"
	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/archivecompleteness"
	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/sources/aquarius"
	"github.com/RatesEngine/rates-engine/internal/sources/band"
	"github.com/RatesEngine/rates-engine/internal/sources/comet"
	"github.com/RatesEngine/rates-engine/internal/sources/external"
	externalbinance "github.com/RatesEngine/rates-engine/internal/sources/external/binance"
	externalbitstamp "github.com/RatesEngine/rates-engine/internal/sources/external/bitstamp"
	externalcoinbase "github.com/RatesEngine/rates-engine/internal/sources/external/coinbase"
	externalcoingecko "github.com/RatesEngine/rates-engine/internal/sources/external/coingecko"
	externalcoinmarketcap "github.com/RatesEngine/rates-engine/internal/sources/external/coinmarketcap"
	externalcryptocompare "github.com/RatesEngine/rates-engine/internal/sources/external/cryptocompare"
	externalecb "github.com/RatesEngine/rates-engine/internal/sources/external/ecb"
	externalexchangerates "github.com/RatesEngine/rates-engine/internal/sources/external/exchangeratesapi"
	externalkraken "github.com/RatesEngine/rates-engine/internal/sources/external/kraken"
	externalpolygonforex "github.com/RatesEngine/rates-engine/internal/sources/external/polygonforex"
	"github.com/RatesEngine/rates-engine/internal/sources/phoenix"
	"github.com/RatesEngine/rates-engine/internal/sources/redstone"
	"github.com/RatesEngine/rates-engine/internal/sources/reflector"
	"github.com/RatesEngine/rates-engine/internal/sources/sdex"
	"github.com/RatesEngine/rates-engine/internal/sources/soroswap"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
	"github.com/RatesEngine/rates-engine/internal/storage/timescale"
	"github.com/RatesEngine/rates-engine/internal/version"
)

func main() { //nolint:gocyclo,gocognit,funlen // subcommand switch; each case is trivial, splitting adds indirection without clarity
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
	case "backfill-external":
		if err := backfillExternal(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "backfill-external: %v\n", err)
			os.Exit(1)
		}
	case "verify-decoders":
		if err := verifyDecoders(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "verify-decoders: %v\n", err)
			os.Exit(1)
		}
	case "verify-external":
		if err := verifyExternal(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "verify-external: %v\n", err)
			os.Exit(1)
		}
	case "verify-archive":
		if err := verifyArchive(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "verify-archive: %v\n", err)
			os.Exit(1)
		}
	case "archive-completeness":
		if err := archiveCompleteness(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "archive-completeness: %v\n", err)
			os.Exit(1)
		}
	case "wasm-history":
		if err := wasmHistory(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "wasm-history: %v\n", err)
			os.Exit(1)
		}
	case "cross-region-check":
		if err := crossRegionCheck(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "cross-region-check: %v\n", err)
			os.Exit(1)
		}
	case "cross-region-monitor":
		if err := crossRegionMonitor(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "cross-region-monitor: %v\n", err)
			os.Exit(1)
		}
	case "backfill":
		if err := backfill(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "backfill: %v\n", err)
			os.Exit(1)
		}
	case "hubble-check":
		if err := hubbleCheck(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "hubble-check: %v\n", err)
			os.Exit(1)
		}
	case "hubble-soroban-events":
		if err := hubbleSorobanEvents(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "hubble-soroban-events: %v\n", err)
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

// usageBody is the static portion of `ratesengine-ops -h`. The header
// (with version) is prepended at print time so the binary's build
// version shows in the output. Kept as a package-level const so the
// printUsage func itself stays short — funlen lint counts the
// multi-line string literal against the function it's defined in.
const usageBody = `
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
  verify-decoders -config PATH -from N -to N
                          Stream a bounded ledger range from Galexie through
                          every registered decoder and print a per-source tally
                          (events matched, outputs emitted, first sample). No
                          DB writes; dispatcher runs in a dry harness. Useful
                          as an end-to-end smoke test after a decoder change
                          and for proving each venue emits on the wire.
  verify-external -config PATH [-timeout DUR]
                          Start every enabled off-chain connector
                          (cfg.External.<venue>.enabled = true), drain the
                          shared sink for up to -timeout (default 60s), and
                          print per-venue first-trade/update samples. Exits
                          early once every enabled venue has emitted at
                          least one output. No DB, no Timescale, no cursors.
  verify-archive -config PATH [-bucket NAME] [-from N] [-to N] [-tier MODE] [-archive-root PATH] [-peers URLs] [-peer-samples N] [-archivist-bin BIN] [-archivist-url URL] [-archivist-timeout DUR]
                          Verify a galexie bucket at one or more tiers:
                            chain      (Tier A) — chain-link hash integrity:
                                       each ledger N's PreviousLedgerHash
                                       equals ledger N-1's Hash. Default.
                            checkpoint (Tier B) — cross-check our LCM's
                                       hash at every 64-ledger checkpoint
                                       against the canonical header-hash
                                       from the local history-archive
                                       (-archive-root, default
                                       /srv/history-archive).
                            peers      (Tier D) — sample checkpoints
                                       within the range and cross-compare
                                       history-XXXXXXXX.json across N
                                       tier-1 validator archives (-peers
                                       URL list or default set of 7).
                                       Consensus-level cryptographic
                                       agreement.
                            archivist  (Tier E) — shell out to
                                       stellar-archivist scan for a full
                                       bucket-by-bucket sha256 audit of
                                       the archive. -archivist-url
                                       defaults to file://<archive-root>;
                                       any peer's https:// archive URL
                                       also works. Requires
                                       stellar-archivist (or rs-stellar-
                                       archivist via -archivist-bin) on
                                       PATH; long-running, gated by
                                       -archivist-timeout (default 30m).
                            all        run all four.
                          Exit 0 = clean; 1 = first break with details.
  archive-completeness <mode> [flags]
                          Completeness check + repair across the dual-archive
                          stack per ADR-0017. Modes:
                            check      Read-only enumeration. Walks expected
                                       checkpoint positions in the cross-anchor
                                       archive and writes a JSON gap report.
                                       Flags: -archive-root PATH, -from N, -to N,
                                              -output-file PATH (default stdout).
                            fix        Run check, then fetch every missing
                                       checkpoint via the multi-source fallback
                                       chain (SDF core_live_001/002/003 +
                                       tier-1 validators) and place each file
                                       atomically. Re-checks after the fill so
                                       the emitted report reflects post-fix
                                       state. Flags: -archive-root, -from, -to,
                                              -workers N, -owner-user STR,
                                              -owner-group STR, -output-file.
                            verify     Daily-cron mode. Runs check → fix →
                                       re-check, then emits a Prometheus
                                       textfile for node_exporter to scrape.
                                       Flags: same as fix, plus -textfile-output
                                       PATH (target node_exporter's
                                       textfile_collector dir, e.g.
                                       /var/lib/node_exporter/textfile_collector/
                                       archive_completeness.prom).
                          PR A/B/C cover cross-anchor; primary MinIO bucket
                          scan lands in PR D.
                          Exit 0 = clean; 1 = at least one missing file remains.
  cross-region-check -regions name=URL,name=URL,... [-pairs PAIR,...] [-metric vwap|twap|ohlc] [-window DUR] [-samples N] [-to TS]
                          Hit each region's /v1/{vwap|twap|ohlc} endpoint
                          for the same closed-bucket window and assert
                          byte-equality on the price field (and OHLC
                          open/high/low/close where applicable). Per
                          ADR-0015 the response should be byte-identical
                          across regions once trades have replicated;
                          divergence here flags one of: replication lag,
                          decoder version drift, upstream divergence,
                          or postgres replication broken. Designed for
                          periodic execution from a monitoring host.
                          Exit 0 = clean; 1 = divergence with diff.
                          Example:
                            ratesengine-ops cross-region-check \
                              -regions r1=https://r1.api.example.net,r2=https://r2.api.example.net \
                              -pairs native/fiat:USD,crypto:BTC/fiat:USD \
                              -metric vwap -samples 5
  cross-region-monitor -regions name=URL,name=URL,... [-pairs PAIR,...] [-metric vwap|twap|ohlc] [-window DUR] [-samples N] [-interval DUR] [-listen :PORT]
                          Long-running daemon variant of cross-region-check.
                          Runs the same per-bucket comparison on a fixed
                          interval and exposes the outcome as Prometheus
                          metrics on -listen (default :9479). Designed
                          to live as a sidecar systemd service on the
                          observability host. Metrics:
                            ratesengine_cross_region_checks_total{outcome=ok|divergence|error}
                            ratesengine_cross_region_divergences_total
                            ratesengine_cross_region_fetch_errors_total{region}
                            ratesengine_cross_region_last_run_timestamp_seconds
                          /healthz returns 503 until the first sweep
                          completes; 200 thereafter. Example:
                            ratesengine-ops cross-region-monitor \
                              -regions r1=...,r2=...,r3=... \
                              -interval 60s -listen :9479
  wasm-history -config PATH -contracts ID,ID,... [-from N] [-to N] [-bucket NAME]
                          Walk a galexie bucket and emit a per-contract
                          WASM-version timeline. For each watched contract,
                          tracks every change to its instance's executable
                          hash and reports the active ledger range per hash.
                          Read-only audit; no DB writes. Output is JSON to
                          stdout. Defaults to S3BucketArchive (the historical
                          bucket) — pass -bucket to override.
                          Example:
                            ratesengine-ops wasm-history \
                              -config /etc/ratesengine.toml \
                              -from 21000000 -to 25000000 \
                              -contracts CDLZ...,CARFAC... \
                              > soroswap-wasm-history.json
  backfill-external -config PATH -source SRC -pair SYM -from TS -to TS -granularity D
                          Pull historical candles from an external venue
                          (binance / kraken / bitstamp / coinbase) and
                          insert synthesised canonical.Trade rows into
                          the trades hypertable. -dry-run prints stats
                          only, no writes. Example:
                            ratesengine-ops backfill-external \
                              -config configs/prod.toml \
                              -source binance -pair XLMUSDT \
                              -from 2024-01-01T00:00:00Z \
                              -to   2024-12-31T00:00:00Z \
                              -granularity 1h
  hubble-check -config PATH -from N -to N -bigquery-project PROJ [-max-mismatches N] [-dry-run-bytes]
                          Cross-check our SDEX trades against SDF's
                          published hubble-public.crypto_stellar.history_trades
                          BigQuery table for the same ledger range.
                          Reports every ledger where the counts disagree.
                          Catches decoder coverage gaps + over-eager
                          decoding on classic SDEX (ManageOffer +
                          classic LP) which Tier A/B/D/E (bytes-level)
                          and cross-region-check (intra-fleet) do not.
                          Soroban DEXes have no decoded Hubble counterpart;
                          covered by the per-WASM decoder audit instead.
                          Off-chain sources (CEX/FX) are out of scope.
                          Auth: Application Default Credentials (run
                          gcloud auth application-default login first).
                          Cost: ~$0.05 per 1M-ledger range at on-demand
                          pricing. Use -dry-run-bytes for a pre-flight
                          estimate. Example:
                            ratesengine-ops hubble-check \
                              -config /etc/ratesengine.toml \
                              -from 21000000 -to 22000000 \
                              -bigquery-project my-gcp-project
  hubble-soroban-events -from N -to N -bigquery-project PROJ -contracts CID,CID [-topic0 SYM] [-topic1 SYM] [-output json|total|csv] [-dry-run-bytes]
                          Per-ledger event-count primitive against
                          hubble-public.crypto_stellar.history_contract_events
                          for the supplied Soroban contract IDs, with
                          optional topic[0]/topic[1] filters. Operators
                          combine this with knowledge of per-source
                          (events ↔ trades) ratios to cross-check
                          decoder coverage on Soroswap / Aquarius /
                          Phoenix / Comet / Reflector / Redstone.
                          See docs/operations/hubble-event-counts.md
                          for the per-source recipe. Auth via
                          Application Default Credentials (same as
                          hubble-check). Cost: 20-40 GB scan per
                          1M-ledger range — use -dry-run-bytes for
                          a preview.
  backfill -config PATH -from N -to N [-source S,S,...] [-bucket NAME] [-dry-run] [-resume]
                          Replay a bounded ledger range through the
                          full ingest pipeline (galexie → dispatcher
                          → decoders → trades hypertable). Same code
                          path as the live indexer, no live tail; CAGGs
                          auto-roll on the inserted rows. Refuses to
                          run any source that isn't BackfillSafe in
                          internal/sources/external/registry.go — for
                          on-chain Soroban sources that means the
                          per-WASM-hash audit (ratesengine-ops
                          wasm-history) must land first per CLAUDE.md
                          "Soroban DeFi contracts upgrade in place".
                          Idempotent: the trades hypertable's unique
                          index on (source, ledger, tx_hash, op_index)
                          makes re-runs over the same range a no-op.
                          Example:
                            ratesengine-ops backfill \
                              -config /etc/ratesengine.toml \
                              -from 21000000 -to 25000000 \
                              -source soroswap,aquarius
  version                 Print version + build date.
  help                    This help.

TODO subcommands (land with their feature PRs):
  cache-prime             Warm the Redis hot-path cache from Timescale.
  verify-invariants       Cross-check aggregated prices against divergence.
`

func printUsage() {
	fmt.Fprintf(os.Stderr, "ratesengine-ops %s\n%s", version.String(), usageBody)
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

// ─── backfill-external ──────────────────────────────────────────

// backfillExternal drives the Backfiller interface for one external
// venue. Operator passes the venue-native symbol (the same shape
// each venue's Streamer would subscribe with): "XLMUSDT" for
// Binance, "XLM/USD" for Kraken, "xlmusd" for Bitstamp, "XLM-USD"
// for Coinbase. Keeps the CLI surface honest to venue conventions
// rather than inventing our own cross-venue normalisation.
func backfillExternal(args []string) error {
	fs := flag.NewFlagSet("backfill-external", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	source := fs.String("source", "", "Venue: binance | kraken | bitstamp | coinbase (required)")
	pairSym := fs.String("pair", "", "Venue-native symbol, e.g. XLMUSDT / XLM/USD / xlmusd / XLM-USD (required)")
	fromStr := fs.String("from", "", "Start time, RFC 3339 (required, e.g. 2024-01-01T00:00:00Z)")
	toStr := fs.String("to", "", "End time, RFC 3339 (required, e.g. 2024-12-31T00:00:00Z)")
	granStr := fs.String("granularity", "1h", "Candle granularity as a Go duration (1m / 15m / 1h / 4h / 1d / 1w)")
	dryRun := fs.Bool("dry-run", false, "Fetch + synthesise trades but don't write to Timescale")
	progressEvery := fs.Int("progress-every", 1000, "Print a progress line every N trades inserted")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *source == "" || *pairSym == "" || *fromStr == "" || *toStr == "" {
		fs.Usage()
		return fmt.Errorf("-config, -source, -pair, -from, -to all required")
	}

	from, err := time.Parse(time.RFC3339, *fromStr)
	if err != nil {
		return fmt.Errorf("parse -from %q: %w", *fromStr, err)
	}
	to, err := time.Parse(time.RFC3339, *toStr)
	if err != nil {
		return fmt.Errorf("parse -to %q: %w", *toStr, err)
	}
	if !from.Before(to) {
		return fmt.Errorf("-from %v must be before -to %v", from, to)
	}
	granularity, err := time.ParseDuration(*granStr)
	if err != nil {
		return fmt.Errorf("parse -granularity %q: %w", *granStr, err)
	}

	backfiller, pair, err := buildBackfiller(*source, *pairSym)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	fmt.Fprintf(os.Stderr, "backfill-external: source=%s pair=%s granularity=%v from=%s to=%s dry-run=%v\n",
		*source, pair.String(), granularity,
		from.Format(time.RFC3339), to.Format(time.RFC3339), *dryRun)

	t0 := time.Now()
	trades, err := backfiller.Backfill(ctx, pair, from, to, granularity)
	if err != nil {
		return fmt.Errorf("backfill: %w", err)
	}
	fmt.Fprintf(os.Stderr, "backfill-external: fetched %d trades in %v\n",
		len(trades), time.Since(t0).Round(time.Millisecond))

	if *dryRun {
		summariseDryRun(trades)
		return nil
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}
	store, err := timescale.Open(ctx, cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("storage: %w", err)
	}
	defer func() { _ = store.Close() }()

	inserted, skipped := 0, 0
	for i, tr := range trades {
		if err := store.InsertTrade(ctx, tr); err != nil {
			skipped++
			fmt.Fprintf(os.Stderr, "insert trade %d (%s): %v\n", i, tr.TxHash, err)
			continue
		}
		inserted++
		if *progressEvery > 0 && inserted%*progressEvery == 0 {
			fmt.Fprintf(os.Stderr, "  ... %d inserted, %d skipped\n", inserted, skipped)
		}
	}
	fmt.Fprintf(os.Stderr, "backfill-external: done — %d inserted, %d skipped in %v\n",
		inserted, skipped, time.Since(t0).Round(time.Millisecond))
	return nil
}

// buildBackfiller maps the -source flag to the venue's Backfiller
// implementation. Each venue's DefaultPairs is consulted to resolve
// the venue-native symbol into a canonical.Pair. Unknown sources or
// unconfigured pairs return a clear error rather than a generic
// "not in map".
func buildBackfiller(source, symbol string) (external.Backfiller, canonical.Pair, error) {
	switch source {
	case externalbinance.SourceName:
		pm, err := externalbinance.DefaultPairs()
		if err != nil {
			return nil, canonical.Pair{}, fmt.Errorf("binance pairs: %w", err)
		}
		pair, ok := pm[symbol]
		if !ok {
			return nil, canonical.Pair{}, unknownPairError(source, symbol, pm)
		}
		return externalbinance.NewStreamer(pm), pair, nil
	case externalkraken.SourceName:
		pm, err := externalkraken.DefaultPairs()
		if err != nil {
			return nil, canonical.Pair{}, fmt.Errorf("kraken pairs: %w", err)
		}
		pair, ok := pm[symbol]
		if !ok {
			return nil, canonical.Pair{}, unknownPairError(source, symbol, pm)
		}
		return externalkraken.NewStreamer(pm), pair, nil
	case externalbitstamp.SourceName:
		pm, err := externalbitstamp.DefaultPairs()
		if err != nil {
			return nil, canonical.Pair{}, fmt.Errorf("bitstamp pairs: %w", err)
		}
		pair, ok := pm[symbol]
		if !ok {
			return nil, canonical.Pair{}, unknownPairError(source, symbol, pm)
		}
		return externalbitstamp.NewStreamer(pm), pair, nil
	case externalcoinbase.SourceName:
		pm, err := externalcoinbase.DefaultPairs()
		if err != nil {
			return nil, canonical.Pair{}, fmt.Errorf("coinbase pairs: %w", err)
		}
		pair, ok := pm[symbol]
		if !ok {
			return nil, canonical.Pair{}, unknownPairError(source, symbol, pm)
		}
		return externalcoinbase.NewStreamer(pm), pair, nil
	}
	return nil, canonical.Pair{}, fmt.Errorf("unknown -source %q (supported: binance, kraken, bitstamp, coinbase)", source)
}

// unknownPairError prints the configured set so the operator can
// see the venue-specific symbol format without consulting docs.
func unknownPairError(source, want string, pm map[string]canonical.Pair) error {
	keys := make([]string, 0, len(pm))
	for k := range pm {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return fmt.Errorf("pair %q not in %s DefaultPairs — known symbols: %v", want, source, keys)
}

// summariseDryRun prints a compact stats view for --dry-run mode.
// Shows first/last trade timestamps, trade count, and pair-level
// volume totals so the operator can sanity-check a range before
// committing a large insert.
func summariseDryRun(trades []canonical.Trade) {
	if len(trades) == 0 {
		fmt.Println("(no trades in range)")
		return
	}
	totalBase, totalQuote := 0.0, 0.0
	for _, t := range trades {
		// Convert 10^8-scaled Amount to float for display. Precision
		// loss here is fine — it's a dry-run summary, not a computed
		// price.
		bf := amountToFloat(t.BaseAmount, 8)
		qf := amountToFloat(t.QuoteAmount, 8)
		totalBase += bf
		totalQuote += qf
	}
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "FIELD\tVALUE")
	fmt.Fprintf(w, "trade count\t%d\n", len(trades))
	fmt.Fprintf(w, "first ts\t%s\n", trades[0].Timestamp.Format(time.RFC3339))
	fmt.Fprintf(w, "last  ts\t%s\n", trades[len(trades)-1].Timestamp.Format(time.RFC3339))
	fmt.Fprintf(w, "pair\t%s\n", trades[0].Pair.String())
	fmt.Fprintf(w, "total base volume\t%.8f\n", totalBase)
	fmt.Fprintf(w, "total quote volume\t%.8f\n", totalQuote)
	if totalBase > 0 {
		fmt.Fprintf(w, "vwap (quote/base)\t%.8f\n", totalQuote/totalBase)
	}
	_ = w.Flush()
}

// amountToFloat converts a canonical.Amount at the given decimal
// scale to a float64 for display. Precision-lossy; never use this
// path for anything that writes back to storage.
func amountToFloat(a canonical.Amount, decimals int) float64 {
	bi := a.BigInt()
	if bi == nil {
		return 0
	}
	// Build "INT.FRAC" then parse via strconv.
	s := bi.String()
	neg := false
	if len(s) > 0 && s[0] == '-' {
		neg = true
		s = s[1:]
	}
	if len(s) <= decimals {
		s = strings.Repeat("0", decimals-len(s)+1) + s
	}
	cut := len(s) - decimals
	formatted := s[:cut] + "." + s[cut:]
	if neg {
		formatted = "-" + formatted
	}
	f, _ := strconv.ParseFloat(formatted, 64)
	return f
}

// ─── verify-decoders ─────────────────────────────────────────────

// verifyDecoders streams a bounded ledger range from the configured
// Galexie datastore through a Dispatcher wired with EVERY registered
// decoder (regardless of cfg.Ingestion.EnabledSources), then prints
// a per-source table of:
//
//	source | matched events | outputs emitted | first sample line
//
// This is a dry harness — no Timescale, no Redis, no cursor writes.
// Useful for:
//
//   - Proving each decoder fires at least once over a recent window,
//     which is the cheapest way to confirm live pubnet traffic
//     matches the topic bytes + schema we compiled against.
//   - Smoke-testing a decoder change: pick a historical range known
//     to contain the source's events, verify outputs didn't regress.
//
// Oracle-variant decoders need their contract addresses in
// cfg.Oracle; any variant with an empty address is skipped with a
// warning rather than failing the whole run.
func verifyDecoders(args []string) error { //nolint:funlen,gocognit,gocyclo // linear diagnostic, splitting reduces readability
	fs := flag.NewFlagSet("verify-decoders", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return fmt.Errorf("-config, -from, and -to are required; -to must be >= -from")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	// Build a dispatcher with every decoder we ship, not just the
	// subset in cfg.Ingestion.EnabledSources. The whole point of
	// verify is to confirm each one fires on the range.
	disp, soroswapDec, registered := buildVerifyDispatcher(cfg.Oracle)
	if len(registered) == 0 {
		return fmt.Errorf("no decoders registered — check oracle contract addresses in config")
	}

	// Optional Soroswap factory seed. Without it, pairs created
	// before the -from ledger are invisible to the decoder (see
	// docs/discovery/dexes-amms/soroswap.md on the swap event's
	// missing token identities).
	if cfg.Oracle.Soroswap.FactoryContract != "" {
		seedEndpoint := cfg.Oracle.Soroswap.SeedRPCEndpoint
		if seedEndpoint == "" && len(cfg.Stellar.RPCEndpoints) > 0 {
			seedEndpoint = cfg.Stellar.RPCEndpoints[0]
		}
		if seedEndpoint == "" {
			return fmt.Errorf("soroswap.factory_contract is set but no RPC endpoint — " +
				"set oracle.soroswap.seed_rpc_endpoint or stellar.rpc_endpoints")
		}
		fmt.Fprintf(os.Stderr, "verify-decoders: seeding soroswap pairs from %s...\n", seedEndpoint)
		seedCtx, seedCancel := context.WithTimeout(context.Background(), 15*time.Minute)
		rpc := stellarrpc.New(seedEndpoint, stellarrpc.WithTimeout(60*time.Second))
		n, err := soroswapDec.SeedFromFactoryRPC(seedCtx, rpc, cfg.Oracle.Soroswap.FactoryContract)
		seedCancel()
		if err != nil {
			return fmt.Errorf("soroswap seed: %w", err)
		}
		fmt.Fprintf(os.Stderr, "verify-decoders: seeded %d soroswap pairs\n", n)
	}

	fmt.Fprintf(os.Stderr, "verify-decoders: registered %d decoders: %s\n",
		len(registered), strings.Join(registered, ", "))
	fmt.Fprintf(os.Stderr, "verify-decoders: streaming ledgers %d..%d from %s\n",
		*from, *to, cfg.Storage.S3Endpoint)

	lsCfg := ledgerstream.Config{
		DataStore: datastore.DataStoreConfig{
			Type: "S3",
			Params: map[string]string{
				"destination_bucket_path": cfg.Storage.S3BucketLive,
				"region":                  cfg.Storage.S3Region,
				"endpoint_url":            cfg.Storage.S3Endpoint,
			},
			NetworkPassphrase: cfg.Stellar.Passphrase(),
			Compression:       "zstd",
		},
	}

	type perSourceStat struct {
		outputs int
		first   string // one-line summary of the first output
	}
	stats := make(map[string]*perSourceStat)
	var totalLedgers, totalOutputs int

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	// Signal channel: total events processed (not emitted outputs —
	// the dispatcher's internal unmatched hit counter tracks "events
	// the decoders saw but ignored"; here we're interested in what
	// each decoder OUTPUTTED, which is the verify claim).
	err = ledgerstream.Stream(ctx, lsCfg, uint32(*from), uint32(*to),
		func(lcm sdkxdr.LedgerCloseMeta) error {
			totalLedgers++
			outputs, perr := disp.ProcessLedger(lcm, cfg.Stellar.Passphrase())
			if perr != nil {
				fmt.Fprintf(os.Stderr, "verify-decoders: ledger %d: %v\n",
					lcm.LedgerSequence(), perr)
				return nil
			}
			for _, ev := range outputs {
				src := ev.Source()
				s, ok := stats[src]
				if !ok {
					s = &perSourceStat{}
					stats[src] = s
				}
				s.outputs++
				if s.first == "" {
					s.first = summariseEvent(ev, lcm.LedgerSequence())
				}
				totalOutputs++
			}
			return nil
		},
	)
	if err != nil {
		return fmt.Errorf("stream: %w", err)
	}

	fmt.Fprintf(os.Stderr, "verify-decoders: processed %d ledgers, %d total outputs\n\n",
		totalLedgers, totalOutputs)

	// Print the per-source table. Include registered-but-silent
	// decoders so operators can see "X was wired but fired zero
	// times" rather than "X was missing from the report."
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "SOURCE\tOUTPUTS\tFIRST SAMPLE")
	names := make([]string, 0, len(registered))
	names = append(names, registered...)
	sort.Strings(names)
	silent := 0
	for _, name := range names {
		s := stats[name]
		if s == nil {
			fmt.Fprintf(w, "%s\t0\t(none)\n", name)
			silent++
			continue
		}
		fmt.Fprintf(w, "%s\t%d\t%s\n", name, s.outputs, s.first)
	}
	if err := w.Flush(); err != nil {
		return err
	}

	if silent > 0 {
		fmt.Fprintf(os.Stderr, "\nverify-decoders: %d/%d decoders emitted zero outputs — "+
			"either the range genuinely lacks their events, or their topic/schema "+
			"no longer matches.\n", silent, len(registered))
	}

	// Dispatcher-internal stats surface here. They distinguish
	// "matched but Decode errored" (decodeErrors) from "no decoder
	// claimed the event" (unmatchedHits) — essential for localising
	// a silent-source finding to either the match or decode side.
	dispStats := disp.Stats()
	if len(dispStats.DecodeErrors) > 0 || dispStats.UnmatchedHits > 0 {
		fmt.Fprintf(os.Stderr, "\ndispatcher stats — unmatched events: %d\n", dispStats.UnmatchedHits)
		if len(dispStats.DecodeErrors) > 0 {
			fmt.Fprintln(os.Stderr, "decoder errors by source:")
			errNames := make([]string, 0, len(dispStats.DecodeErrors))
			for k := range dispStats.DecodeErrors {
				errNames = append(errNames, k)
			}
			sort.Strings(errNames)
			for _, name := range errNames {
				fmt.Fprintf(os.Stderr, "  %s: %d\n", name, dispStats.DecodeErrors[name])
			}
		}
	}
	return nil
}

// buildVerifyDispatcher wires every decoder we ship, returning the
// dispatcher, the Soroswap decoder (so callers can seed it from the
// factory RPC), and the list of source names that were actually
// registered (oracle variants with an unset contract address are
// skipped).
func buildVerifyDispatcher(oracle config.OracleConfig) (*dispatcher.Dispatcher, *soroswap.Decoder, []string) {
	soroswapDec := soroswap.NewDecoder()
	decoders := []dispatcher.Decoder{
		soroswapDec,
		aquarius.NewDecoder(),
		phoenix.NewDecoder(),
		comet.NewDecoder(),
	}
	registered := []string{
		soroswap.SourceName,
		aquarius.SourceName,
		phoenix.SourceName,
		comet.SourceName,
	}

	// Oracle variants: only register if their contract address is set.
	if oracle.Reflector.DEXContract != "" {
		decoders = append(decoders, reflector.NewDecoder(reflector.VariantDEX, oracle.Reflector.DEXContract))
		registered = append(registered, reflector.SourceDEX)
	} else {
		fmt.Fprintln(os.Stderr, "verify-decoders: skip reflector-dex — oracle.reflector.dex_contract empty")
	}
	if oracle.Reflector.CEXContract != "" {
		decoders = append(decoders, reflector.NewDecoder(reflector.VariantCEX, oracle.Reflector.CEXContract))
		registered = append(registered, reflector.SourceCEX)
	} else {
		fmt.Fprintln(os.Stderr, "verify-decoders: skip reflector-cex — oracle.reflector.cex_contract empty")
	}
	if oracle.Reflector.FXContract != "" {
		decoders = append(decoders, reflector.NewDecoder(reflector.VariantFX, oracle.Reflector.FXContract))
		registered = append(registered, reflector.SourceFX)
	} else {
		fmt.Fprintln(os.Stderr, "verify-decoders: skip reflector-fx — oracle.reflector.fx_contract empty")
	}

	var callDecoders []dispatcher.ContractCallDecoder
	if oracle.Redstone.AdapterContract != "" {
		decoders = append(decoders, redstone.NewDecoder(oracle.Redstone.AdapterContract))
		registered = append(registered, redstone.SourceName)
	} else {
		fmt.Fprintln(os.Stderr, "verify-decoders: skip redstone — oracle.redstone.adapter_contract empty")
	}
	if oracle.Band.StandardReferenceContract != "" {
		callDecoders = append(callDecoders, band.NewDecoder(oracle.Band.StandardReferenceContract))
		registered = append(registered, band.SourceName)
	} else {
		fmt.Fprintln(os.Stderr, "verify-decoders: skip band — oracle.band.standard_reference_contract empty")
	}

	disp := dispatcher.New(decoders...)
	disp.AddOpDecoder(sdex.NewDecoder())
	registered = append(registered, sdex.SourceName)
	for _, ccd := range callDecoders {
		disp.AddContractCallDecoder(ccd)
	}
	return disp, soroswapDec, registered
}

// summariseEvent renders one consumer.Event as a one-line human
// summary for the verify-decoders table. We don't need the full
// canonical.Trade / OracleUpdate — just enough to confirm the
// decoder produced structurally-valid output.
func summariseEvent(ev consumer.Event, ledger uint32) string {
	switch e := any(ev).(type) {
	case soroswap.TradeEvent:
		return fmt.Sprintf("trade ledger=%d pair=%s", ledger, e.Trade.Pair.String())
	case aquarius.TradeEvent:
		return fmt.Sprintf("trade ledger=%d pair=%s", ledger, e.Trade.Pair.String())
	case phoenix.TradeEvent:
		return fmt.Sprintf("trade ledger=%d pair=%s", ledger, e.Trade.Pair.String())
	case comet.TradeEvent:
		return fmt.Sprintf("trade ledger=%d pair=%s", ledger, e.Trade.Pair.String())
	case sdex.TradeEvent:
		return fmt.Sprintf("trade ledger=%d pair=%s", ledger, e.Trade.Pair.String())
	case reflector.UpdateEvent:
		return fmt.Sprintf("oracle ledger=%d asset=%s", ledger, e.Update.Asset.String())
	case redstone.UpdateEvent:
		return fmt.Sprintf("oracle ledger=%d asset=%s", ledger, e.Update.Asset.String())
	case band.UpdateEvent:
		return fmt.Sprintf("oracle ledger=%d asset=%s", ledger, e.Update.Asset.String())
	default:
		return fmt.Sprintf("event kind=%s ledger=%d", ev.EventKind(), ledger)
	}
}

// ─── verify-external ─────────────────────────────────────────────

// verifyExternal starts every enabled off-chain connector, drains
// the shared sink for up to -timeout, and prints a per-venue table
// of first trades / oracle updates observed. Exits early once every
// enabled venue has emitted at least one output.
//
// "Enabled" means cfg.External.<venue>.enabled = true AND (for
// paid-tier venues) the API key is non-empty after env resolution.
// Free venues (binance, kraken, bitstamp, coinbase, coingecko, ecb)
// start unconditionally once enabled; paid venues (polygonforex,
// coinmarketcap, cryptocompare, exchangeratesapi) need their
// respective API keys.
//
// Like verify-decoders, this writes nothing to Timescale or Redis —
// it's purely a diagnostic that the connector goroutines reach live
// vendor endpoints and produce well-formed canonical.Trade /
// canonical.OracleUpdate rows.
func verifyExternal(args []string) error { //nolint:funlen,gocognit,gocyclo // dispatch-heavy; splitting would reduce linearity
	fs := flag.NewFlagSet("verify-external", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	timeout := fs.Duration("timeout", 60*time.Second, "Max time to wait for every enabled venue to emit")
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

	streamers, pollers, enabled, err := buildVerifyExternal(cfg.External)
	if err != nil {
		return err
	}
	if len(enabled) == 0 {
		return fmt.Errorf("no external connectors enabled — set [external.<venue>].enabled = true " +
			"and, for paid venues, provide the API key env var")
	}

	fmt.Fprintf(os.Stderr, "verify-external: enabled %d venues: %s\n",
		len(enabled), strings.Join(enabled, ", "))
	fmt.Fprintf(os.Stderr, "verify-external: waiting up to %s for first output from each\n\n", *timeout)

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	logger := slog.Default()
	sink := make(chan consumer.Event, 256)

	wait, err := external.Run(ctx, streamers, pollers, sink, logger)
	if err != nil {
		return fmt.Errorf("external.Run: %w", err)
	}

	type perVenueStat struct {
		outputs int
		first   string // one-line summary
	}
	stats := make(map[string]*perVenueStat)

	allSeen := func() bool {
		for _, name := range enabled {
			if stats[name] == nil {
				return false
			}
		}
		return true
	}

DRAIN:
	for {
		select {
		case <-ctx.Done():
			break DRAIN
		case ev, ok := <-sink:
			if !ok {
				break DRAIN
			}
			src := ev.Source()
			s, ok := stats[src]
			if !ok {
				s = &perVenueStat{}
				stats[src] = s
			}
			s.outputs++
			if s.first == "" {
				s.first = summariseExternalEvent(ev)
			}
			if allSeen() {
				break DRAIN
			}
		}
	}

	cancel()
	wait()

	// Print table.
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "VENUE\tCLASS\tOUTPUTS\tFIRST SAMPLE")
	sort.Strings(enabled)
	silent := 0
	for _, name := range enabled {
		entry := external.Lookup(name)
		cls := string(entry.Class)
		s := stats[name]
		if s == nil {
			fmt.Fprintf(w, "%s\t%s\t0\t(none — poll interval too long or connection failed)\n", name, cls)
			silent++
			continue
		}
		fmt.Fprintf(w, "%s\t%s\t%d\t%s\n", name, cls, s.outputs, s.first)
	}
	if err := w.Flush(); err != nil {
		return err
	}
	if silent > 0 {
		fmt.Fprintf(os.Stderr, "\nverify-external: %d/%d venues silent — ECB polls daily, "+
			"exchangeratesapi minute; raise -timeout or inspect logs.\n",
			silent, len(enabled))
	}
	return nil
}

// buildVerifyExternal mirrors cmd/ratesengine-indexer/main.go's
// startExternalConnectors — just without the indexer's logger +
// sink wiring. Returns the StreamerSpec/PollerSpec lists ready for
// external.Run plus the flat list of enabled venue names the caller
// waits on.
func buildVerifyExternal(cfg config.ExternalConfig) ([]external.StreamerSpec, []external.PollerSpec, []string, error) { //nolint:funlen,gocognit,gocyclo // dispatch-heavy; splitting would reduce linearity
	var streamers []external.StreamerSpec
	var pollers []external.PollerSpec
	var enabled []string

	if cfg.Binance.Enabled {
		pairMap, err := externalbinance.DefaultPairs()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("binance: %w", err)
		}
		pairs, err := externalbinance.DefaultPairList()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("binance: %w", err)
		}
		streamers = append(streamers, external.StreamerSpec{
			Streamer: externalbinance.NewStreamer(pairMap),
			Pairs:    pairs,
		})
		enabled = append(enabled, externalbinance.SourceName)
	}
	if cfg.Kraken.Enabled {
		pairMap, err := externalkraken.DefaultPairs()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("kraken: %w", err)
		}
		pairs, err := externalkraken.DefaultPairList()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("kraken: %w", err)
		}
		streamers = append(streamers, external.StreamerSpec{
			Streamer: externalkraken.NewStreamer(pairMap),
			Pairs:    pairs,
		})
		enabled = append(enabled, externalkraken.SourceName)
	}
	if cfg.Bitstamp.Enabled {
		pairMap, err := externalbitstamp.DefaultPairs()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("bitstamp: %w", err)
		}
		pairs, err := externalbitstamp.DefaultPairList()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("bitstamp: %w", err)
		}
		streamers = append(streamers, external.StreamerSpec{
			Streamer: externalbitstamp.NewStreamer(pairMap),
			Pairs:    pairs,
		})
		enabled = append(enabled, externalbitstamp.SourceName)
	}
	if cfg.Coinbase.Enabled {
		pairMap, err := externalcoinbase.DefaultPairs()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("coinbase: %w", err)
		}
		pairs, err := externalcoinbase.DefaultPairList()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("coinbase: %w", err)
		}
		streamers = append(streamers, external.StreamerSpec{
			Streamer: externalcoinbase.NewStreamer(pairMap),
			Pairs:    pairs,
		})
		enabled = append(enabled, externalcoinbase.SourceName)
	}

	// Pollers. Pair lists mirror the indexer's defaults. ECB and FX
	// venues take fiat cross pairs; aggregators take a fixed crypto-
	// vs-G3 fiat set.
	fxPairs := verifyDefaultFXPairs("USD")
	aggPairs := verifyDefaultAggregatorPairs()

	if cfg.ExchangeRatesApi.Enabled {
		p, err := externalexchangerates.NewPoller(cfg.ExchangeRatesApi.APIKey)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("exchangeratesapi: %w", err)
		}
		if cfg.ExchangeRatesApi.Base != "" {
			p.Base = cfg.ExchangeRatesApi.Base
		}
		pollers = append(pollers, external.PollerSpec{Poller: p, Pairs: verifyDefaultFXPairs(p.Base)})
		enabled = append(enabled, externalexchangerates.SourceName)
	}
	if cfg.PolygonForex.Enabled {
		p, err := externalpolygonforex.NewPoller(cfg.PolygonForex.APIKey)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("polygon-forex: %w", err)
		}
		if cfg.PolygonForex.Base != "" {
			p.Base = cfg.PolygonForex.Base
		}
		pollers = append(pollers, external.PollerSpec{Poller: p, Pairs: verifyDefaultFXPairs(p.Base)})
		enabled = append(enabled, externalpolygonforex.SourceName)
	}
	if cfg.CoinGecko.Enabled {
		pollers = append(pollers, external.PollerSpec{
			Poller: externalcoingecko.NewPoller(),
			Pairs:  aggPairs,
		})
		enabled = append(enabled, externalcoingecko.SourceName)
	}
	if cfg.CoinMarketCap.Enabled {
		p, err := externalcoinmarketcap.NewPoller(cfg.CoinMarketCap.APIKey)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("coinmarketcap: %w", err)
		}
		pollers = append(pollers, external.PollerSpec{Poller: p, Pairs: aggPairs})
		enabled = append(enabled, externalcoinmarketcap.SourceName)
	}
	if cfg.CryptoCompare.Enabled {
		p, err := externalcryptocompare.NewPoller(cfg.CryptoCompare.APIKey)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("cryptocompare: %w", err)
		}
		pollers = append(pollers, external.PollerSpec{Poller: p, Pairs: aggPairs})
		enabled = append(enabled, externalcryptocompare.SourceName)
	}
	if cfg.ECB.Enabled {
		pollers = append(pollers, external.PollerSpec{
			Poller: externalecb.NewPoller(),
			Pairs:  fxPairs,
		})
		enabled = append(enabled, externalecb.SourceName)
	}

	return streamers, pollers, enabled, nil
}

// verifyDefaultFXPairs mirrors the indexer's defaultFXPairs; kept
// local here so verify-external doesn't cross the cmd/ package
// boundary.
func verifyDefaultFXPairs(base string) []canonical.Pair {
	baseAsset, err := canonical.NewFiatAsset(base)
	if err != nil {
		return nil
	}
	targets := []string{"EUR", "GBP", "JPY", "CAD", "AUD", "CHF", "NZD", "SEK", "NOK", "MXN"}
	out := make([]canonical.Pair, 0, len(targets))
	for _, code := range targets {
		if code == base {
			continue
		}
		a, err := canonical.NewFiatAsset(code)
		if err != nil {
			continue
		}
		p, err := canonical.NewPair(a, baseAsset)
		if err != nil {
			continue
		}
		out = append(out, p)
	}
	return out
}

// verifyDefaultAggregatorPairs mirrors the indexer's
// defaultAggregatorPairs.
func verifyDefaultAggregatorPairs() []canonical.Pair {
	cryptos := []string{"XLM", "BTC", "ETH"}
	fiats := []string{"USD", "EUR", "GBP"}
	out := make([]canonical.Pair, 0, len(cryptos)*len(fiats))
	for _, c := range cryptos {
		ca, err := canonical.NewCryptoAsset(c)
		if err != nil {
			continue
		}
		for _, f := range fiats {
			fa, err := canonical.NewFiatAsset(f)
			if err != nil {
				continue
			}
			p, err := canonical.NewPair(ca, fa)
			if err != nil {
				continue
			}
			out = append(out, p)
		}
	}
	return out
}

// summariseExternalEvent renders one external-connector event as a
// one-line human summary for the verify-external table.
func summariseExternalEvent(ev consumer.Event) string {
	switch e := any(ev).(type) {
	case external.TradeEvent:
		return fmt.Sprintf("trade %s pair=%s base=%s quote=%s",
			e.Trade.Timestamp.Format(time.RFC3339),
			e.Trade.Pair.String(),
			e.Trade.BaseAmount.String(),
			e.Trade.QuoteAmount.String())
	case external.UpdateEvent:
		return fmt.Sprintf("update %s asset=%s price=%s",
			e.Update.Timestamp.Format(time.RFC3339),
			e.Update.Asset.String(),
			e.Update.Price.String())
	default:
		return fmt.Sprintf("event kind=%s", ev.EventKind())
	}
}

// ─── verify-archive ─────────────────────────────────────────────

// verifyArchive walks every LCM in a galexie bucket in sequence and
// asserts chain-link integrity — for each ledger N, we check that
// ledger[N].Header.PreviousLedgerHash == ledger[N-1].Hash. Any
// mismatch is a hard stop with the diverging ledger numbers and
// hashes printed for diagnosis.
//
// This is Tier A from docs/operations/galexie-backfill.md:
//
//	Catches any internal corruption, dropped ledger, or replay
//	divergence regardless of upstream trust.
//
// Tier B (checkpoint anchoring against the local history-archive)
// needs to parse ledger-XXXXXXXX.xdr.gz files to extract the
// canonical ledger-hash at each 64-ledger boundary; that lands in
// a follow-up.
//
// Defaults:
//   - bucket: cfg.Storage.S3BucketArchive, falling back to
//     S3BucketLive when -bucket is unset AND S3BucketArchive is
//     empty. Usually set -bucket explicitly when verifying the
//     historical half.
//   - from: 2 (ledger 1 has no predecessor; the chain-link check
//     starts from ledger 2).
//   - to: 0 = unbounded. For a bounded verify of a specific range
//     set both -from and -to.
func verifyArchive(args []string) error { //nolint:funlen,gocognit,gocyclo // linear diagnostic; splitting reduces readability
	fs := flag.NewFlagSet("verify-archive", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	bucketOverride := fs.String("bucket", "", "Override bucket name (default: storage.s3_bucket_archive, then s3_bucket_live)")
	from := fs.Uint("from", 2, "First ledger to verify (inclusive, default 2 — ledger 1 has no predecessor)")
	to := fs.Uint("to", 0, "Last ledger to verify (inclusive, 0 = unbounded/live)")
	tier := fs.String("tier", "chain", "Verification tier: chain (A) | checkpoint (B) | peers (D) | archivist (E) | all")
	archiveRoot := fs.String("archive-root", "/srv/history-archive",
		"Path to local rs-stellar-archivist mirror (used by checkpoint/all tier)")
	peerList := fs.String("peers", "",
		"Comma-separated peer archive URLs for Tier D (empty → built-in tier-1 default set)")
	peerSamples := fs.Int("peer-samples", 20,
		"Number of checkpoints to sample for Tier D cross-peer diff")
	archivistBin := fs.String("archivist-bin", "stellar-archivist",
		"Path to rs-stellar-archivist binary for Tier E (used in archivist/all tier)")
	archivistURL := fs.String("archivist-url", "",
		"Archive URL for Tier E (empty → file://<archive-root>)")
	archivistTimeout := fs.Duration("archivist-timeout", 30*time.Minute,
		"Maximum runtime for the rs-stellar-archivist scan command")
	if err := fs.Parse(args); err != nil {
		return err
	}
	doChain := *tier == "chain" || *tier == "all"
	doCheckpoint := *tier == "checkpoint" || *tier == "all"
	doPeers := *tier == "peers" || *tier == "all"
	doArchivist := *tier == "archivist" || *tier == "all"
	if !doChain && !doCheckpoint && !doPeers && !doArchivist {
		return fmt.Errorf("unknown -tier %q (expected chain | checkpoint | peers | archivist | all)", *tier)
	}
	if *cfgPath == "" {
		return fmt.Errorf("-config is required")
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	bucket := *bucketOverride
	if bucket == "" {
		bucket = cfg.Storage.S3BucketArchive
	}
	if bucket == "" {
		bucket = cfg.Storage.S3BucketLive
	}
	if bucket == "" {
		return fmt.Errorf("no bucket resolved — set -bucket or storage.s3_bucket_archive / storage.s3_bucket_live")
	}

	fmt.Fprintf(os.Stderr, "verify-archive: bucket=%s range=[%d,%d] tier=%s\n", bucket, *from, *to, *tier)
	if doCheckpoint {
		fmt.Fprintf(os.Stderr, "verify-archive: checkpoint anchor against %s\n", *archiveRoot)
	}

	// Tier A + B (LCM walk via ledgerstream). Skipped when tier=peers.
	if doChain || doCheckpoint {
		if err := verifyArchiveLCMWalk(cfg, bucket, uint32(*from), uint32(*to),
			doChain, doCheckpoint, *archiveRoot); err != nil {
			return err
		}
	}

	// Tier D (multi-peer checkpoint diff). Independent of LCM walk.
	if doPeers {
		if err := verifyArchivePeers(uint32(*from), uint32(*to), *peerList, *peerSamples); err != nil {
			return err
		}
	}

	// Tier E (rs-stellar-archivist scan). Independent of LCM walk and peer diff.
	if doArchivist {
		url := *archivistURL
		if url == "" {
			url = "file://" + *archiveRoot
		}
		if err := verifyArchiveArchivist(*archivistBin, url, *archivistTimeout); err != nil {
			return err
		}
	}
	return nil
}

// verifyArchiveLCMWalk runs the Tier A + B passes over every LCM in
// the given bucket range. Split from verifyArchive so Tier D can run
// standalone without the ledgerstream setup.
func verifyArchiveLCMWalk(cfg config.Config, bucket string, from, to uint32, doChain, doCheckpoint bool, archiveRoot string) error { //nolint:funlen,gocognit,gocyclo
	lsCfg := ledgerstream.Config{
		DataStore: datastore.DataStoreConfig{
			Type: "S3",
			Params: map[string]string{
				"destination_bucket_path": bucket,
				"region":                  cfg.Storage.S3Region,
				"endpoint_url":            cfg.Storage.S3Endpoint,
			},
			NetworkPassphrase: cfg.Stellar.Passphrase(),
			Compression:       "zstd",
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), 24*time.Hour)
	defer cancel()

	var (
		prevSeq           uint32
		prevHash          sdkxdr.Hash
		hasPrev           bool
		verified          int
		mismatches        int
		checkpointsOK     int
		checkpointsMissed int // archive file absent; skip rather than fail
		lastProgress      time.Time
		progressEvery     = 10 * time.Second
	)

	startedAt := time.Now()
	err := ledgerstream.Stream(ctx, lsCfg, from, to,
		func(lcm sdkxdr.LedgerCloseMeta) error {
			seq := lcm.LedgerSequence()
			hash := lcm.LedgerHash()
			header, ok := extractLedgerHeader(lcm)
			if !ok {
				return fmt.Errorf("ledger %d: cannot extract LedgerHeader", seq)
			}

			if doChain && hasPrev {
				// Gap in sequence (missing ledger) is itself a chain
				// break — the sequence must be dense.
				if seq != prevSeq+1 {
					mismatches++
					return fmt.Errorf("ledger sequence gap: %d → %d (expected %d)",
						prevSeq, seq, prevSeq+1)
				}
				if header.PreviousLedgerHash != prevHash {
					mismatches++
					return fmt.Errorf("chain break at ledger %d:\n"+
						"  ledger[%d].Hash              = %s\n"+
						"  ledger[%d].PreviousLedgerHash = %s",
						seq, prevSeq, hashToHex(prevHash),
						seq, hashToHex(header.PreviousLedgerHash))
				}
			}

			// Tier B — compare against the local history-archive at
			// each 64-ledger checkpoint boundary. The archive's
			// ledger-<hex>.xdr.gz file carries the canonical
			// LedgerHeaderHistoryEntry for every ledger in the
			// checkpoint; we match on LedgerSeq and compare the
			// bundled Hash against our LCM's hash.
			if doCheckpoint && seq%64 == 63 {
				expected, hit, cerr := readArchivedLedgerHash(archiveRoot, seq)
				switch {
				case cerr != nil:
					mismatches++
					return fmt.Errorf("ledger %d: archive read failed: %w", seq, cerr)
				case !hit:
					// File not present (e.g. mirror behind latest
					// checkpoints) — count and continue rather than
					// fail the whole sweep.
					checkpointsMissed++
				case expected != hash:
					mismatches++
					return fmt.Errorf("checkpoint anchor mismatch at ledger %d:\n"+
						"  our LCM hash          = %s\n"+
						"  archive-signed hash   = %s",
						seq, hashToHex(hash), hashToHex(expected))
				default:
					checkpointsOK++
				}
			}

			prevSeq = seq
			prevHash = hash
			hasPrev = true
			verified++

			if time.Since(lastProgress) >= progressEvery {
				fmt.Fprintf(os.Stderr, "verify-archive: ledger %d, %d verified, %.0f ledgers/s\n",
					seq, verified, float64(verified)/time.Since(startedAt).Seconds())
				lastProgress = time.Now()
			}
			return nil
		},
	)
	elapsed := time.Since(startedAt)

	fmt.Fprintf(os.Stderr, "\nverify-archive: verified %d ledgers in %s (%.0f ledgers/s)\n",
		verified, elapsed.Round(time.Second), float64(verified)/elapsed.Seconds())
	if doCheckpoint {
		fmt.Fprintf(os.Stderr, "verify-archive: checkpoints matched=%d missed=%d (missed = archive file absent, not a failure)\n",
			checkpointsOK, checkpointsMissed)
	}
	if err != nil {
		return fmt.Errorf("verification FAILED: %w", err)
	}
	if verified == 0 {
		return fmt.Errorf("verified 0 ledgers — bucket empty or range out of scope")
	}
	if doChain {
		fmt.Fprintf(os.Stderr, "verify-archive: chain-link integrity OK ✓\n")
	}
	if doCheckpoint {
		if checkpointsOK == 0 && checkpointsMissed > 0 {
			fmt.Fprintf(os.Stderr, "verify-archive: checkpoint anchor INCONCLUSIVE — %d missed, 0 matched (archive mirror may be stale)\n", checkpointsMissed)
		} else {
			fmt.Fprintf(os.Stderr, "verify-archive: checkpoint anchor OK ✓  (%d matched, %d missed)\n", checkpointsOK, checkpointsMissed)
		}
	}
	_ = mismatches // silence unused variable warning; set for future exit-code semantics
	return nil
}

// defaultTier1Peers is a representative set of tier-1 validator
// history-archive roots — one URL per operator-org. Chosen from the
// HISTORY entries in /etc/stellar/captive-core-galexie.cfg and
// cross-referenced against SEP-20 home-domain declarations.
//
// Each org runs 3 archives behind the same SCP quorum set; picking
// one per org is sufficient — if org A's nodes disagree internally,
// that's a different (intra-org) problem than what Tier D surfaces.
// Operators can override with -peers if they want more coverage.
var defaultTier1Peers = []string{
	"https://bootes-history.publicnode.org",
	"https://archive.v1.stellar.lobstr.co",
	"https://stellar-history-de-fra.satoshipay.io",
	"https://stellar-history-usc.franklintempleton.com/azuscshf401",
	"https://alpha-history.validator.stellar.creit.tech",
	"http://history.stellar.org/prd/core-live/core_live_001",
	"https://stellar-full-history1.bdnodes.net",
}

// historyCheckpoint is the subset of a history-XXXXXXXX.json that we
// compare across peers. We ignore `server` (version of stellar-core
// that built the archive — varies by operator) and `version` (schema
// version, rarely changes). What must agree across the network is
// the consensus state: currentLedger + the bucket-list hashes.
type historyCheckpoint struct {
	CurrentLedger  uint32          `json:"currentLedger"`
	CurrentBuckets []historyBucket `json:"currentBuckets"`
}

type historyBucket struct {
	Curr string          `json:"curr"`
	Snap string          `json:"snap"`
	Next json.RawMessage `json:"next"` // opaque; compare raw bytes
}

// verifyArchivePeers samples checkpoints in [from, to] and cross-
// compares each peer's history-XXXXXXXX.json. Any disagreement is a
// consensus-level finding — either one peer has replayed wrong, or
// a fork was retained somewhere. Either way, loud failure.
//
// sampleN is the target number of checkpoints to verify. Actual
// count may be less if the range contains fewer checkpoints; always
// includes the first and last checkpoint for edge coverage.
func verifyArchivePeers(from, to uint32, peerList string, sampleN int) error { //nolint:funlen,gocognit,gocyclo
	peers := defaultTier1Peers
	if peerList != "" {
		peers = strings.Split(peerList, ",")
		for i := range peers {
			peers[i] = strings.TrimSpace(peers[i])
		}
	}
	if len(peers) < 2 {
		return fmt.Errorf("tier peers needs ≥2 archive URLs; got %d", len(peers))
	}

	// Find checkpoint ledgers in range. Checkpoints are at seq
	// 63, 127, 191, ... (seq mod 64 == 63).
	firstCP := ((from + 63) / 64 * 64) - 1
	if firstCP < from {
		firstCP += 64
	}
	var lastCP uint32
	if to == 0 {
		// Unbounded range — pick "last few hours of pubnet" as a
		// stand-in. 10k ledgers before the current guessed tip.
		// This is coarse; better would be a HEAD query against one
		// peer, but we keep Tier D self-contained.
		lastCP = firstCP + 640 // 10 sample slots
	} else {
		lastCP = (to / 64 * 64) - 1
		if lastCP < firstCP {
			return fmt.Errorf("range [%d,%d] contains no checkpoint ledgers", from, to)
		}
	}

	// Sample evenly-spaced checkpoints. Always include first and last.
	samples := []uint32{firstCP}
	if lastCP != firstCP && sampleN > 1 {
		stride := uint32(1)
		totalCP := (lastCP-firstCP)/64 + 1
		if uint32(sampleN) < totalCP {
			stride = totalCP / uint32(sampleN)
		}
		for seq := firstCP + stride*64; seq < lastCP; seq += stride * 64 {
			samples = append(samples, seq)
		}
		if samples[len(samples)-1] != lastCP {
			samples = append(samples, lastCP)
		}
	}

	fmt.Fprintf(os.Stderr, "verify-archive: peer diff — %d peers × %d checkpoints\n",
		len(peers), len(samples))
	for _, p := range peers {
		fmt.Fprintf(os.Stderr, "  peer: %s\n", p)
	}

	client := &http.Client{Timeout: 30 * time.Second}
	matches, mismatches := 0, 0
	for _, seq := range samples {
		hexSeq := fmt.Sprintf("%08x", seq)
		relPath := fmt.Sprintf("history/%s/%s/%s/history-%s.json",
			hexSeq[0:2], hexSeq[2:4], hexSeq[4:6], hexSeq)

		observed := make(map[string]historyCheckpoint)
		for _, peer := range peers {
			url := strings.TrimRight(peer, "/") + "/" + relPath
			cp, err := fetchHistoryCheckpoint(client, url)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  ledger %d: peer %s: %v\n", seq, peer, err)
				continue
			}
			observed[peer] = cp
		}
		if len(observed) < 2 {
			fmt.Fprintf(os.Stderr, "  ledger %d: only %d peers responded; skipping (inconclusive)\n",
				seq, len(observed))
			continue
		}

		// Every peer's checkpoint must agree. Pick one as the
		// canonical reference and compare the rest.
		var ref historyCheckpoint
		var refPeer string
		for p, cp := range observed {
			ref = cp
			refPeer = p
			break
		}
		allAgree := true
		for p, cp := range observed {
			if p == refPeer {
				continue
			}
			if !checkpointsEqual(ref, cp) {
				mismatches++
				allAgree = false
				fmt.Fprintf(os.Stderr, "  ledger %d: PEERS DISAGREE\n    ref=%s\n    odd=%s\n",
					seq, refPeer, p)
			}
		}
		if allAgree {
			matches++
			fmt.Fprintf(os.Stderr, "  ledger %d: %d peers agree ✓\n", seq, len(observed))
		}
	}

	fmt.Fprintf(os.Stderr, "\nverify-archive: peer diff — %d consensus-verified checkpoints, %d disagreements\n",
		matches, mismatches)
	if mismatches > 0 {
		return fmt.Errorf("peer cross-check FAILED (%d disagreements)", mismatches)
	}
	if matches == 0 {
		return fmt.Errorf("peer cross-check INCONCLUSIVE — no checkpoint verified across ≥2 peers")
	}
	fmt.Fprintf(os.Stderr, "verify-archive: peer cross-check OK ✓\n")
	return nil
}

// verifyArchiveArchivist runs `<bin> scan <url>` against an archive
// URL (file:// for the local mirror, https:// for any peer's
// published archive) and surfaces the result.
//
// rs-stellar-archivist's scan walks every checkpoint in the
// archive, fetches every referenced bucket file, recomputes the
// sha256 of each, and confirms it matches the manifest. A
// successful scan is a strong integrity signal — orthogonal to
// Tier B (LCM-vs-checkpoint anchor) because Tier B trusts the
// local mirror's manifest, while Tier E re-validates the manifest
// itself by recomputing every bucket hash.
//
// We don't parse the binary's stdout structurally — formatting
// shifts across rs-stellar-archivist releases. Instead we stream
// the output to our stderr (so the operator sees progress) and
// rely on the exit code.
//
// Failure modes:
//   - bin not on $PATH                    → ErrNotFound, exits 127
//   - archive URL doesn't resolve         → non-zero exit
//   - any checkpoint / bucket fails hash  → non-zero exit
//   - takes longer than the timeout       → ctx cancel, killed
//
// The CLI flag default is "stellar-archivist" (the Go binary
// shipped with stellar-archivist). Operators using the Rust port
// (`rs-stellar-archivist`) override via `-archivist-bin`.
func verifyArchiveArchivist(bin, url string, timeout time.Duration) error {
	fmt.Fprintf(os.Stderr, "verify-archive: archivist scan bin=%s url=%s timeout=%s\n",
		bin, url, timeout)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	// gosec G204: bin + url are operator-supplied diagnostic flags
	// on a CLI that ALREADY shells the operator's environment —
	// any "untrusted input" boundary at this point has already
	// been crossed by the operator running this command at all.
	cmd := exec.CommandContext(ctx, bin, "scan", url) //nolint:gosec // operator-supplied flags

	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		// CommandContext closes stdin and surfaces a context-deadline
		// exit as a *exec.Error wrapping context.DeadlineExceeded;
		// preserve that signal.
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("archivist scan timed out after %s — re-run with longer -archivist-timeout", timeout)
		}
		return fmt.Errorf("archivist scan FAILED: %w", err)
	}
	fmt.Fprintf(os.Stderr, "verify-archive: archivist scan OK ✓\n")
	return nil
}

// fetchHistoryCheckpoint retrieves and parses one history-XXXXXXXX.json
// from a peer archive.
func fetchHistoryCheckpoint(client *http.Client, url string) (historyCheckpoint, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return historyCheckpoint{}, err
	}
	req.Header.Set("User-Agent", "rates-engine/verify-archive")
	resp, err := client.Do(req)
	if err != nil {
		return historyCheckpoint{}, err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return historyCheckpoint{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20)) // 4 MiB cap
	if err != nil {
		return historyCheckpoint{}, err
	}
	var cp historyCheckpoint
	if err := json.Unmarshal(body, &cp); err != nil {
		return historyCheckpoint{}, fmt.Errorf("parse: %w", err)
	}
	return cp, nil
}

// checkpointsEqual compares the consensus-state fields of two
// history-XXXXXXXX.json records. Ignores server + version which
// vary legitimately across operators.
func checkpointsEqual(a, b historyCheckpoint) bool {
	if a.CurrentLedger != b.CurrentLedger {
		return false
	}
	if len(a.CurrentBuckets) != len(b.CurrentBuckets) {
		return false
	}
	for i := range a.CurrentBuckets {
		if a.CurrentBuckets[i].Curr != b.CurrentBuckets[i].Curr ||
			a.CurrentBuckets[i].Snap != b.CurrentBuckets[i].Snap ||
			string(a.CurrentBuckets[i].Next) != string(b.CurrentBuckets[i].Next) {
			return false
		}
	}
	return true
}

// readArchivedLedgerHash fetches the canonical ledger-hash for
// ledger seq from the local rs-stellar-archivist mirror. seq must
// be a checkpoint ledger (seq % 64 == 63) — that's the last ledger
// in the file named ledger-<hex(seq)>.xdr.gz at path
// <archiveRoot>/ledger/XX/YY/ZZ/ where XX,YY,ZZ are the first three
// bytes of the hex-encoded sequence.
//
// The file is a gzipped, self-delimiting XDR stream of
// LedgerHeaderHistoryEntry records (64 of them, covering ledgers
// seq-63 through seq). We scan until the entry matching seq, then
// return entry.Hash.
//
// Returns (hash, true, nil) on success, (_, false, nil) if the file
// doesn't exist on disk (archive mirror hasn't synced that far), or
// (_, _, err) on any real read/parse error.
func readArchivedLedgerHash(archiveRoot string, seq uint32) (sdkxdr.Hash, bool, error) {
	hexSeq := fmt.Sprintf("%08x", seq)
	path := filepath.Join(archiveRoot, "ledger",
		hexSeq[0:2], hexSeq[2:4], hexSeq[4:6],
		fmt.Sprintf("ledger-%s.xdr.gz", hexSeq))

	f, err := os.Open(path) //nolint:gosec // archiveRoot is operator-supplied via flag
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return sdkxdr.Hash{}, false, nil
		}
		return sdkxdr.Hash{}, false, err
	}
	stream, err := sdkxdr.NewGzStream(f)
	if err != nil {
		_ = f.Close()
		return sdkxdr.Hash{}, false, fmt.Errorf("open gz stream: %w", err)
	}
	defer func() { _ = stream.Close() }()

	var entry sdkxdr.LedgerHeaderHistoryEntry
	for {
		if err := stream.ReadOne(&entry); err != nil {
			if errors.Is(err, io.EOF) {
				return sdkxdr.Hash{}, false,
					fmt.Errorf("checkpoint file %s did not contain ledger %d", path, seq)
			}
			return sdkxdr.Hash{}, false, fmt.Errorf("read entry: %w", err)
		}
		if uint32(entry.Header.LedgerSeq) == seq {
			return entry.Hash, true, nil
		}
	}
}

// extractLedgerHeader pulls the header out of an LCM regardless of
// version. V0 (pre-p20) and V1 (p20+) differ in structure; both
// expose a LedgerHeaderHistoryEntry at different paths.
func extractLedgerHeader(lcm sdkxdr.LedgerCloseMeta) (sdkxdr.LedgerHeader, bool) {
	switch lcm.V {
	case 0:
		if lcm.V0 == nil {
			return sdkxdr.LedgerHeader{}, false
		}
		return lcm.V0.LedgerHeader.Header, true
	case 1:
		if lcm.V1 == nil {
			return sdkxdr.LedgerHeader{}, false
		}
		return lcm.V1.LedgerHeader.Header, true
	case 2:
		if lcm.V2 == nil {
			return sdkxdr.LedgerHeader{}, false
		}
		return lcm.V2.LedgerHeader.Header, true
	}
	return sdkxdr.LedgerHeader{}, false
}

// hashToHex renders an xdr.Hash as a lowercase 64-char hex string.
func hashToHex(h sdkxdr.Hash) string {
	return hex.EncodeToString(h[:])
}

// ─── wasm-history ───────────────────────────────────────────────
//
// wasmHistory walks a galexie bucket over [from, to] and tracks
// when each watched contract's instance executable hash changes.
// Detection signal: any LedgerEntryChange (Created or Updated)
// whose entry is a CONTRACT_DATA with a LedgerKeyContractInstance
// key — that's the contract's instance row, and its Val is an
// ScContractInstance whose Executable field carries the WASM hash.
// Both deploys and `update_current_contract_wasm` invocations
// surface the same way.
//
// Output: a JSON document keyed by contract C-strkey, with the
// timeline of (wasm_hash, from_ledger, to_ledger) ranges.
// Read-only — no DB writes, no Timescale, no cursor changes.
//
// Default bucket is cfg.Storage.S3BucketArchive (historical) since
// audits typically span ranges before galexie-live's seam.

type wasmRange struct {
	WasmHash   string `json:"wasm_hash"`
	FromLedger uint32 `json:"from_ledger"`
	ToLedger   uint32 `json:"to_ledger,omitempty"` // 0 = open / current
}

type contractHistory struct {
	Contract string      `json:"contract"`
	Ranges   []wasmRange `json:"ranges"`
}

// wasmContractState tracks the open (most recently seen) WASM hash
// for one contract, plus the closed ranges that preceded it.
type wasmContractState struct {
	ranges  []wasmRange
	current string // current open WASM hash hex; empty = no open range
}

// archiveCompleteness dispatches the `archive-completeness <mode>`
// subcommand per ADR-0017. Modes: check (PR A), fix (PR B),
// verify (PR C — this PR).
func archiveCompleteness(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("archive-completeness: subcommand required (check / fix / verify)")
	}
	switch args[0] {
	case "check":
		return archiveCompletenessCheck(args[1:])
	case "fix":
		return archiveCompletenessFix(args[1:])
	case "verify":
		return archiveCompletenessVerify(args[1:])
	default:
		return fmt.Errorf("archive-completeness: unknown mode %q (supported: check, fix, verify)", args[0])
	}
}

// archiveCompletenessVerify is the daily-cron mode: runs check →
// fix → re-check, then emits a Prometheus textfile for
// node_exporter's textfile_collector to scrape. Also writes the
// JSON Report.
//
// This is the canonical command the systemd timer fires:
//
//	ratesengine-ops archive-completeness verify \
//	  -from 2 -to <network_head> \
//	  -textfile-output /var/lib/node_exporter/textfile_collector/archive_completeness.prom \
//	  -output-file /var/lib/galexie/last-completeness-report.json
//
// Exit semantics:
//   - 0: clean (no missing files after fix)
//   - 1: residual missing files (fallback chain exhausted some)
//   - other: I/O error
func archiveCompletenessVerify(args []string) error {
	fs := flag.NewFlagSet("archive-completeness verify", flag.ContinueOnError)
	archiveRoot := fs.String("archive-root", "/srv/history-archive",
		"Cross-anchor archive root.")
	from := fs.Uint("from", 2, "First ledger sequence (inclusive).")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive). Required.")
	workers := fs.Int("workers", 8, "Parallel fetch workers.")
	ownerUser := fs.String("owner-user", "stellar", "File owner user.")
	ownerGroup := fs.String("owner-group", "stellar", "File owner group.")
	outputFile := fs.String("output-file", "",
		"Path to write JSON report. Empty = stdout.")
	textfileOutput := fs.String("textfile-output", "",
		"Path to write Prometheus textfile (node_exporter textfile_collector format). Empty = no metrics emit.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == 0 {
		return fmt.Errorf("-to is required")
	}
	if uint64(*from) > uint64(*to) {
		return fmt.Errorf("-from (%d) must be <= -to (%d)", *from, *to)
	}

	startedAt := time.Now()

	// Phase 1 — initial check.
	checker := archivecompleteness.NewCrossAnchorChecker(*archiveRoot)
	preRes, err := checker.Check(uint32(*from), uint32(*to))
	if err != nil {
		return fmt.Errorf("initial cross-anchor check: %w", err)
	}

	report := archivecompleteness.NewReport(uint32(*from), uint32(*to))
	snapshot := archivecompleteness.NewMetricsSnapshot()

	// Phase 2 — fix any missing.
	var fillRes archivecompleteness.FillResult
	if len(preRes.Missing) > 0 {
		filler, err := archivecompleteness.NewCrossAnchorFiller(archivecompleteness.FillerOptions{
			ArchiveRoot: *archiveRoot,
			Workers:     *workers,
			OwnerUser:   *ownerUser,
			OwnerGroup:  *ownerGroup,
		})
		if err != nil {
			return fmt.Errorf("filler: %w", err)
		}
		fillRes = filler.Fill(context.Background(), preRes.Missing)
		fmt.Fprintf(os.Stderr,
			"archive-completeness verify: filled %d / %d missing checkpoints (workers=%d)\n",
			fillRes.Filled, len(preRes.Missing), *workers)
	}

	// Phase 3 — re-check; the post-fix state is what we report.
	postRes, err := checker.Check(uint32(*from), uint32(*to))
	if err != nil {
		return fmt.Errorf("post-fix cross-anchor check: %w", err)
	}
	report.SetCrossAnchor(*archiveRoot, postRes)

	// Populate metrics. LastSuccessTimestamp is set ONLY when the
	// post-fix state is clean — alert rules rely on this gauge
	// going stale when something's wrong.
	snapshot.PopulateFromReport(report)
	snapshot.PopulateFromFillResult(fillRes)
	snapshot.RunDurationSeconds = time.Since(startedAt).Seconds()
	if !report.AnyMissing() {
		snapshot.LastSuccessTimestamp = startedAt
	}

	// Write JSON report (operator-readable diagnostic).
	if err := writeReport(report, *outputFile); err != nil {
		return err
	}

	// Write Prometheus textfile (node_exporter scrapes this dir).
	if *textfileOutput != "" {
		if err := archivecompleteness.WriteTextfileAtomic(*textfileOutput, snapshot); err != nil {
			return fmt.Errorf("write textfile: %w", err)
		}
		fmt.Fprintf(os.Stderr,
			"archive-completeness verify: metrics written to %s\n", *textfileOutput)
	}

	if report.AnyMissing() {
		fmt.Fprintf(os.Stderr,
			"archive-completeness verify: %d residual missing checkpoint(s); see report\n",
			report.CrossAnchor.MissingCount)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr,
		"archive-completeness verify: clean (%.1fs)\n", snapshot.RunDurationSeconds)
	return nil
}

// archiveCompletenessFix runs the `check` then fetches every
// missing checkpoint via the multi-source fallback chain. Read-
// then-write — does NOT mutate either archive without first
// confirming the file is missing.
//
// Exit semantics:
//   - 0: every previously-missing file has been placed
//   - 1: some files still missing after exhausting the chain
//   - other: I/O / config error
func archiveCompletenessFix(args []string) error {
	fs := flag.NewFlagSet("archive-completeness fix", flag.ContinueOnError)
	archiveRoot := fs.String("archive-root", "/srv/history-archive",
		"Cross-anchor archive root (default: /srv/history-archive).")
	from := fs.Uint("from", 2, "First ledger sequence (inclusive).")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive). Required.")
	workers := fs.Int("workers", 8, "Parallel fetch workers (default 8).")
	ownerUser := fs.String("owner-user", "stellar",
		"Local user that should own placed files. Empty disables chown.")
	ownerGroup := fs.String("owner-group", "stellar",
		"Local group that should own placed files. Empty disables chown.")
	outputFile := fs.String("output-file", "",
		"Path to write JSON post-fix report. Default: stdout.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == 0 {
		return fmt.Errorf("-to is required (pass the network head ledger sequence)")
	}
	if uint64(*from) > uint64(*to) {
		return fmt.Errorf("-from (%d) must be <= -to (%d)", *from, *to)
	}

	// Phase 1 — check: enumerate the missing list.
	checker := archivecompleteness.NewCrossAnchorChecker(*archiveRoot)
	res, err := checker.Check(uint32(*from), uint32(*to))
	if err != nil {
		return fmt.Errorf("cross-anchor check: %w", err)
	}

	report := archivecompleteness.NewReport(uint32(*from), uint32(*to))
	report.SetCrossAnchor(*archiveRoot, res)

	if len(res.Missing) == 0 {
		// Already complete; nothing to do.
		return writeReport(report, *outputFile)
	}

	// Phase 2 — fix: fetch each missing checkpoint via the
	// multi-source fallback chain.
	filler, err := archivecompleteness.NewCrossAnchorFiller(archivecompleteness.FillerOptions{
		ArchiveRoot: *archiveRoot,
		Workers:     *workers,
		OwnerUser:   *ownerUser,
		OwnerGroup:  *ownerGroup,
	})
	if err != nil {
		return fmt.Errorf("filler: %w", err)
	}
	fillRes := filler.Fill(context.Background(), res.Missing)
	fmt.Fprintf(os.Stderr,
		"archive-completeness fix: %d filled / %d failed (workers=%d)\n",
		fillRes.Filled, len(fillRes.Failed), *workers)
	for source, count := range fillRes.PerSourceSuccess {
		fmt.Fprintf(os.Stderr, "  source %s: %d fetched\n", source, count)
	}
	for _, f := range fillRes.Failed {
		fmt.Fprintf(os.Stderr, "  FAILED seq=%d reason=%s\n", f.Seq, f.Reason)
	}

	// Phase 3 — re-check: after the fill, scan again so the report
	// reflects post-fix state. The Filler is idempotent (next run
	// will just skip files now present), so the re-check is the
	// authoritative measure of what's still missing.
	postRes, err := checker.Check(uint32(*from), uint32(*to))
	if err != nil {
		return fmt.Errorf("post-fix cross-anchor check: %w", err)
	}
	report.SetCrossAnchor(*archiveRoot, postRes)

	if err := writeReport(report, *outputFile); err != nil {
		return err
	}
	if report.AnyMissing() {
		fmt.Fprintf(os.Stderr,
			"archive-completeness fix: %d checkpoint(s) still missing after fallback chain — see report\n",
			report.CrossAnchor.MissingCount)
		os.Exit(1)
	}
	return nil
}

// writeReport encodes the Report to outputFile (or stdout when empty).
func writeReport(report *archivecompleteness.Report, outputFile string) error {
	var w io.Writer = os.Stdout
	if outputFile != "" {
		f, err := os.Create(outputFile) //nolint:gosec // operator-supplied path
		if err != nil {
			return fmt.Errorf("create output file: %w", err)
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	return report.WriteJSON(w)
}

// archiveCompletenessCheck implements the read-only `check` mode.
// Walks the cross-anchor archive (PR A; the primary archive scan
// lands in PR B), emits a JSON [archivecompleteness.Report].
//
// Exit semantics:
//   - 0: every section clean (no missing files in scope)
//   - 1: at least one section reported missing files
//   - other: I/O / config error before scan completed
func archiveCompletenessCheck(args []string) error {
	fs := flag.NewFlagSet("archive-completeness check", flag.ContinueOnError)
	archiveRoot := fs.String("archive-root", "/srv/history-archive",
		"Cross-anchor archive root (default: /srv/history-archive).")
	from := fs.Uint("from", 2, "First ledger sequence (inclusive).")
	to := fs.Uint("to", 0,
		"Last ledger sequence (inclusive). Required — pass the network head.")
	outputFile := fs.String("output-file", "",
		"Path to write JSON report. Default: stdout.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *to == 0 {
		return fmt.Errorf("-to is required (pass the network head ledger sequence)")
	}
	if uint64(*from) > uint64(*to) {
		return fmt.Errorf("-from (%d) must be <= -to (%d)", *from, *to)
	}

	report := archivecompleteness.NewReport(uint32(*from), uint32(*to))

	checker := archivecompleteness.NewCrossAnchorChecker(*archiveRoot)
	res, err := checker.Check(uint32(*from), uint32(*to))
	if err != nil {
		return fmt.Errorf("cross-anchor scan: %w", err)
	}
	report.SetCrossAnchor(*archiveRoot, res)

	// PR A scope: cross-anchor only. Primary section stays nil; PR B
	// will populate it.

	if err := writeReport(report, *outputFile); err != nil {
		return err
	}

	// Non-zero exit when anything is missing so cron / k8s Job
	// invocations surface gaps as a Prometheus-style probe.
	if report.AnyMissing() {
		fmt.Fprintf(os.Stderr,
			"archive-completeness check: %d missing checkpoint(s) in cross-anchor archive (range [%d, %d])\n",
			report.CrossAnchor.MissingCount, *from, *to)
		os.Exit(1)
	}
	return nil
}

func wasmHistory(args []string) error { //nolint:funlen,gocognit,gocyclo // linear diagnostic, splitting reduces readability
	fs := flag.NewFlagSet("wasm-history", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 2, "First ledger sequence (inclusive)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive). Required when -parallel > 1.")
	contractsCSV := fs.String("contracts", "",
		"Comma-separated contract C-strkey IDs to watch (required, at least one)")
	bucket := fs.String("bucket", "",
		"Galexie bucket name. Default: cfg.Storage.S3BucketArchive.")
	progressEvery := fs.Uint("progress-every", 100_000, "Emit progress lines to stderr every N ledgers")
	parallel := fs.Uint("parallel", 1,
		"Number of concurrent worker ranges. Range [from,to] is split into "+
			"N contiguous chunks. Each worker has its own ledgerstream + dispatcher; "+
			"results are merged at the end. Worth setting >1 for ranges of 1M+ ledgers.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return fmt.Errorf("-config is required")
	}
	if *contractsCSV == "" {
		return fmt.Errorf("-contracts is required (one or more comma-separated C-strkey IDs)")
	}
	if *parallel == 0 {
		*parallel = 1
	}
	if *parallel > 1 && *to == 0 {
		return fmt.Errorf("-parallel > 1 requires -to (workers split a bounded range)")
	}
	if *to != 0 && *to < *from {
		return fmt.Errorf("-to (%d) must be >= -from (%d)", *to, *from)
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	// Decode the watch list to fixed 32-byte hashes for cheap matching.
	watch := make(map[sdkxdr.Hash]string) // hash → C-strkey (for output)
	for _, s := range strings.Split(*contractsCSV, ",") {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		raw, err := strkey.Decode(strkey.VersionByteContract, s)
		if err != nil {
			return fmt.Errorf("invalid contract ID %q: %w", s, err)
		}
		if len(raw) != 32 {
			return fmt.Errorf("contract ID %q decoded to %d bytes, expected 32", s, len(raw))
		}
		var h sdkxdr.Hash
		copy(h[:], raw)
		watch[h] = s
	}
	if len(watch) == 0 {
		return fmt.Errorf("-contracts parsed to empty watch list")
	}

	bucketName := *bucket
	if bucketName == "" {
		bucketName = cfg.Storage.S3BucketArchive
	}
	fmt.Fprintf(os.Stderr, "wasm-history: watching %d contract(s), bucket=%s, range=[%d, %d], parallel=%d\n",
		len(watch), bucketName, *from, *to, *parallel)

	lsCfg := ledgerstream.Config{
		DataStore: datastore.DataStoreConfig{
			Type: "S3",
			Params: map[string]string{
				"destination_bucket_path": bucketName,
				"region":                  cfg.Storage.S3Region,
				"endpoint_url":            cfg.Storage.S3Endpoint,
			},
			NetworkPassphrase: cfg.Stellar.Passphrase(),
			Compression:       "zstd",
		},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	startedAt := time.Now()

	// Split the range into N contiguous chunks. Worker i gets
	// [from + i*size, from + (i+1)*size - 1] except the last
	// worker absorbs the remainder.
	workerStates, totalScanned, err := runWasmHistoryWorkers(
		ctx, lsCfg, watch, uint32(*from), uint32(*to), int(*parallel), uint64(*progressEvery))
	if err != nil {
		return err
	}

	fmt.Fprintf(os.Stderr, "\nwasm-history: scanned %d ledgers across %d worker(s) in %s\n",
		totalScanned, *parallel, time.Since(startedAt).Round(time.Second))

	// Merge worker outputs. Each worker's per-contract ranges are
	// already in ledger-order within its chunk; concatenating in
	// worker-order produces a globally ordered list, then we collapse
	// adjacent same-hash ranges across the boundaries.
	merged := mergeWasmHistories(workerStates, watch)

	// Render: stable order by C-strkey for deterministic output.
	out := make([]contractHistory, 0, len(watch))
	for h, ranges := range merged {
		out = append(out, contractHistory{
			Contract: watch[h],
			Ranges:   ranges,
		})
	}
	// Also emit watched contracts that produced zero changes — useful
	// signal that the audit ran and saw nothing rather than was misconfigured.
	for h, name := range watch {
		if _, seen := merged[h]; !seen {
			out = append(out, contractHistory{Contract: name, Ranges: nil})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Contract < out[j].Contract })

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// workerResult is what each parallel worker produces: a state map
// covering its bounded range, plus the actual upper bound it reached
// (used by merge to know where this worker's open ranges should close).
type workerResult struct {
	state    map[sdkxdr.Hash]*wasmContractState
	scanned  uint64
	upperEnd uint32 // last ledger the worker actually saw (inclusive)
}

// runWasmHistoryWorkers splits [from,to] into `parallel` contiguous
// chunks and runs each in its own goroutine. Returns per-worker
// state maps in worker-order plus the total ledgers scanned.
func runWasmHistoryWorkers(
	ctx context.Context,
	lsCfg ledgerstream.Config,
	watch map[sdkxdr.Hash]string,
	from, to uint32,
	parallel int,
	progressEvery uint64,
) ([]workerResult, uint64, error) {
	if parallel < 1 {
		parallel = 1
	}
	results := make([]workerResult, parallel)
	for i := range results {
		results[i].state = make(map[sdkxdr.Hash]*wasmContractState)
	}

	// Range partition. Use the unbounded form (to == 0) only when
	// parallel == 1 — the parallel path always works on bounded
	// chunks since unbounded only makes sense for live tail.
	bounds := splitRange(from, to, parallel)
	startedAt := time.Now()

	var wg sync.WaitGroup
	errCh := make(chan error, parallel)
	totalScanned := atomicUint64{}

	for i, b := range bounds {
		i, b := i, b
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i].upperEnd = b.to
			workerScanned := uint64(0)
			err := ledgerstream.Stream(ctx, lsCfg, b.from, b.to,
				func(lcm sdkxdr.LedgerCloseMeta) error {
					seq := lcm.LedgerSequence()
					scanLCMForWasmChanges(lcm, watch, results[i].state, seq)
					workerScanned++
					if progressEvery > 0 && workerScanned%progressEvery == 0 {
						total := totalScanned.add(progressEvery)
						rate := float64(total) / time.Since(startedAt).Seconds()
						fmt.Fprintf(os.Stderr, "wasm-history: w%d ledger %d, total scanned %d, %.0f ledgers/s\n",
							i, seq, total, rate)
					}
					return nil
				},
			)
			results[i].scanned = workerScanned
			// Add the un-counted residue (workerScanned mod progressEvery).
			totalScanned.add(workerScanned % progressEvery)
			if err != nil && !errors.Is(err, context.Canceled) {
				errCh <- fmt.Errorf("worker %d [%d,%d]: %w", i, b.from, b.to, err)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		return nil, totalScanned.load(), err // first error wins
	}
	return results, totalScanned.load(), nil
}

// rangeChunk is one worker's slice of the overall [from,to] range.
type rangeChunk struct{ from, to uint32 }

// splitRange divides [from,to] into n contiguous chunks. The last
// chunk absorbs any remainder so the union exactly covers [from,to].
func splitRange(from, to uint32, n int) []rangeChunk {
	if n <= 1 || to <= from {
		return []rangeChunk{{from, to}}
	}
	width := uint32(int(to-from+1) / n)
	out := make([]rangeChunk, n)
	for i := 0; i < n; i++ {
		chunkFrom := from + uint32(i)*width
		chunkTo := chunkFrom + width - 1
		if i == n-1 {
			chunkTo = to // last chunk absorbs remainder
		}
		out[i] = rangeChunk{chunkFrom, chunkTo}
	}
	return out
}

// mergeWasmHistories combines per-worker state maps into one
// per-contract timeline. Open ranges from each worker (where the
// worker exited mid-WASM-version) are closed at the worker's upper
// bound, then the timelines are concatenated in worker-order.
// Adjacent ranges with the same hash across worker boundaries are
// collapsed into a single range.
func mergeWasmHistories(
	workers []workerResult,
	watch map[sdkxdr.Hash]string,
) map[sdkxdr.Hash][]wasmRange {
	merged := make(map[sdkxdr.Hash][]wasmRange)
	for _, w := range workers {
		for h, s := range w.state {
			// Close the worker's open range at its upper bound.
			if len(s.ranges) > 0 && s.ranges[len(s.ranges)-1].ToLedger == 0 {
				s.ranges[len(s.ranges)-1].ToLedger = w.upperEnd
			}
			existing := merged[h]
			for _, r := range s.ranges {
				if len(existing) > 0 && existing[len(existing)-1].WasmHash == r.WasmHash &&
					existing[len(existing)-1].ToLedger+1 == r.FromLedger {
					// Adjacent same-hash → extend the prior range.
					existing[len(existing)-1].ToLedger = r.ToLedger
				} else {
					existing = append(existing, r)
				}
			}
			merged[h] = existing
		}
	}
	// Reopen the LAST range of each contract — i.e. clear ToLedger
	// if it hits the very last worker's upperEnd, since "we don't
	// know yet" is more honest than "ends here" for the operator
	// reading the JSON. Actually no — the operator scoped -to
	// explicitly; closing at to is correct. Leave as-is.
	_ = watch // referenced only for godoc symmetry; merging is keyed by Hash.
	return merged
}

// atomicUint64 is a tiny helper for thread-safe counter increments
// without pulling in sync/atomic boilerplate at every call site.
type atomicUint64 struct {
	mu sync.Mutex
	v  uint64
}

func (a *atomicUint64) add(n uint64) uint64 {
	a.mu.Lock()
	a.v += n
	r := a.v
	a.mu.Unlock()
	return r
}

func (a *atomicUint64) load() uint64 {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.v
}

// scanLCMForWasmChanges walks every operation's LedgerEntryChanges
// in lcm and updates state when a watched contract's instance
// executable hash changes (or first appears).
//
// Performance note: every value access on the SDK XDR types is a
// deep copy (LedgerCloseMetaV1 includes TxProcessing[] — potentially
// thousands of bytes per ledger). At this hot path we use pointer
// access exclusively — `lcm.V1`, `entry.Data.ContractData`,
// `cd.Val.Instance` — to avoid per-ledger XDR copies. An earlier
// implementation using GetV1() / GetContractData() / GetInstance()
// burned ~6 minutes of 99% CPU on a 100k-ledger sample.
func scanLCMForWasmChanges(
	lcm sdkxdr.LedgerCloseMeta,
	watch map[sdkxdr.Hash]string,
	state map[sdkxdr.Hash]*wasmContractState,
	seq uint32,
) {
	if lcm.V != 1 || lcm.V1 == nil {
		return // pre-V1 LCM (very old ledgers); no Soroban; nothing to scan
	}
	v1 := lcm.V1
	for i := range v1.TxProcessing {
		txMeta := &v1.TxProcessing[i].TxApplyProcessing
		switch {
		case txMeta.V3 != nil:
			for j := range txMeta.V3.Operations {
				changes := txMeta.V3.Operations[j].Changes
				for k := range changes {
					scanLedgerEntryChange(&changes[k], watch, state, seq)
				}
			}
		case txMeta.V4 != nil:
			for j := range txMeta.V4.Operations {
				changes := txMeta.V4.Operations[j].Changes
				for k := range changes {
					scanLedgerEntryChange(&changes[k], watch, state, seq)
				}
			}
		default:
			// V1/V2 didn't have ContractData. Skip.
			continue
		}
	}
}

// scanLedgerEntryChange checks one LedgerEntryChange for a
// watched-contract instance update. Updates state in place.
//
// Takes the change by pointer to avoid copying the (potentially
// deep) LedgerEntry tree on every call.
func scanLedgerEntryChange(
	change *sdkxdr.LedgerEntryChange,
	watch map[sdkxdr.Hash]string,
	state map[sdkxdr.Hash]*wasmContractState,
	seq uint32,
) {
	var entry *sdkxdr.LedgerEntry
	switch change.Type {
	case sdkxdr.LedgerEntryChangeTypeLedgerEntryCreated:
		entry = change.Created
	case sdkxdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		entry = change.Updated
	case sdkxdr.LedgerEntryChangeTypeLedgerEntryRestored:
		// Restored counts as "the entry exists at this hash again" —
		// treat like Created for tracking purposes.
		entry = change.Restored
	default:
		return
	}
	if entry == nil {
		return
	}

	// Type discriminator first — most LedgerEntries are Account /
	// Trustline / Offer / etc., not ContractData. Cheap reject path.
	if entry.Data.Type != sdkxdr.LedgerEntryTypeContractData {
		return
	}
	cd := entry.Data.ContractData
	if cd == nil {
		return
	}

	// Only the LedgerKeyContractInstance row carries the executable;
	// per-storage-key data rows have unrelated keys.
	if cd.Key.Type != sdkxdr.ScValTypeScvLedgerKeyContractInstance {
		return
	}

	// Match against our watch list. ContractId is *ContractId on the
	// ScAddress union when Type == ScAddressTypeScAddressTypeContract.
	if cd.Contract.Type != sdkxdr.ScAddressTypeScAddressTypeContract {
		return
	}
	if cd.Contract.ContractId == nil {
		return
	}
	contractHash := sdkxdr.Hash(*cd.Contract.ContractId)
	if _, watched := watch[contractHash]; !watched {
		return
	}

	// The Val should be an ScContractInstance carrying an Executable.
	if cd.Val.Type != sdkxdr.ScValTypeScvContractInstance {
		return
	}
	inst := cd.Val.Instance
	if inst == nil {
		return
	}
	if inst.Executable.Type != sdkxdr.ContractExecutableTypeContractExecutableWasm {
		// Stellar-asset contracts have no WASM; skip them but record
		// a placeholder hash so the timeline is unambiguous.
		recordWasmTransition(state, contractHash, "stellar-asset", seq)
		return
	}
	if inst.Executable.WasmHash == nil {
		return
	}
	hashHex := hex.EncodeToString(inst.Executable.WasmHash[:])
	recordWasmTransition(state, contractHash, hashHex, seq)
}

// recordWasmTransition advances a contract's history when its
// executable hash differs from the previously seen one. First-seen
// opens an initial range; same-hash repeats are no-ops.
func recordWasmTransition(
	state map[sdkxdr.Hash]*wasmContractState,
	contract sdkxdr.Hash,
	wasmHash string,
	seq uint32,
) {
	s, ok := state[contract]
	if !ok {
		s = &wasmContractState{}
		state[contract] = s
	}
	if s.current == wasmHash {
		return // no transition
	}
	// Close the previous open range (if any).
	if s.current != "" && len(s.ranges) > 0 {
		s.ranges[len(s.ranges)-1].ToLedger = seq - 1
	}
	// Open a new range at this ledger.
	s.ranges = append(s.ranges, wasmRange{WasmHash: wasmHash, FromLedger: seq})
	s.current = wasmHash
}
