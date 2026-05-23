// Binary ratesengine-ops is the admin CLI for operational tasks
// that don't belong in the long-running binaries. Subcommands fall
// into a few rough buckets:
//
//   - Ingest / backfill: `backfill`, `backfill-external`,
//     `detect-gaps`, `list-cursors`.
//   - Archive integrity: `verify-archive`, `archive-completeness`,
//     `cross-region-check`, `cross-region-monitor`.
//   - Soroban discovery / WASM tracking: `discovery`, `wasm-history`,
//     `wasm-history-merge-jsonl`, `extract-wasm-from-galexie`.
//   - Supply: `supply`.
//   - Diagnostics: `rpc-probe`, `verify-decoders`, `verify-external`,
//     `hubble-check`, `hubble-soroban-events`.
//   - Doc generation: `docs-config` (regenerates the config
//     reference from struct tags; called by `make docs-config`).
//
// Subcommands land alongside the features they support; the canonical
// list is the `switch args[0]` block in main.go and the
// `ratesengine-ops --help` output.
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
	"github.com/RatesEngine/rates-engine/internal/currency"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
	"github.com/RatesEngine/rates-engine/internal/events"
	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/pipeline"
	"github.com/RatesEngine/rates-engine/internal/scval"
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
	// Default the aws-sdk-go-v2 response-checksum mode before any
	// subcommand builds an S3 datastore — MinIO responses carry no
	// checksum, so the SDK's "when_supported" default WARN-spams
	// every ledger read (verify-archive's 1.65 GB log of noise).
	pipeline.QuietS3ChecksumWarnings()

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
	case "backfill-chainlink":
		if err := backfillChainlink(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "backfill-chainlink: %v\n", err)
			os.Exit(1)
		}
	case "verify-decoders":
		if err := verifyDecoders(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "verify-decoders: %v\n", err)
			os.Exit(1)
		}
	case "scan-soroban-events":
		if err := scanSorobanEvents(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "scan-soroban-events: %v\n", err)
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
	case "discovery":
		if err := discoveryCmd(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "discovery: %v\n", err)
			os.Exit(1)
		}
	case "supply":
		if err := supplyCmd(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "supply: %v\n", err)
			os.Exit(1)
		}
	case "sep1-refresh":
		if err := sep1RefreshCmd(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "sep1-refresh: %v\n", err)
			os.Exit(1)
		}
	case "wasm-history":
		if err := wasmHistory(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "wasm-history: %v\n", err)
			os.Exit(1)
		}
	case "wasm-history-merge-jsonl":
		if err := wasmHistoryMergeJSONL(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "wasm-history-merge-jsonl: %v\n", err)
			os.Exit(1)
		}
	case "extract-wasm-from-galexie":
		if err := extractWasmFromGalexie(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "extract-wasm-from-galexie: %v\n", err)
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
	case "rehydrate-galexie-archive":
		if err := rehydrateGalexieArchive(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "rehydrate-galexie-archive: %v\n", err)
			os.Exit(1)
		}
	case "trim-galexie-archive":
		if err := trimGalexieArchive(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "trim-galexie-archive: %v\n", err)
			os.Exit(1)
		}
	case "seed-soroswap-pairs":
		if err := seedSoroswapPairs(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "seed-soroswap-pairs: %v\n", err)
			os.Exit(1)
		}
	case "seed-entry-counts":
		if err := seedEntryCounts(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "seed-entry-counts: %v\n", err)
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
	case "mint-key":
		if err := mintKey(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "mint-key: %v\n", err)
			os.Exit(1)
		}
	case "upgrade-key":
		if err := upgradeKey(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "upgrade-key: %v\n", err)
			os.Exit(1)
		}
	case "emit-incident":
		if err := emitIncident(args[1:]); err != nil {
			fmt.Fprintf(os.Stderr, "emit-incident: %v\n", err)
			os.Exit(1)
		}
	case "version", "--version", "-v", "-version":
		fmt.Println(version.String())
	case "help", "--help", "-h", "-help":
		printUsage()
	default:
		// Subcommands the usageBody flags as still-planned (cache-prime,
		// verify-invariants) land via their feature PRs and add their
		// own case here. Anything else prints help + exits 2.
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
  scan-soroban-events -config PATH -from N -to N [-topic0 STR] [-contract CID] [-limit N] [-bucket NAME]
                          In-infra analogue of hubble-soroban-events (no
                          BigQuery): stream a galexie ledger range and dump
                          EVERY Soroban contract event as JSON
                          (contract_id, decoded topic[], body map keys +
                          value types), optionally filtered to topic[0]==STR
                          and/or a single contract. Decodes by reusing the
                          dispatcher's event extraction — ground-truth for
                          "what does protocol X actually emit on-chain"
                          (e.g. discovering real contract addresses + event
                          schemas before writing/auditing a decoder). No DB
                          writes. -bucket defaults to s3_bucket_archive then
                          s3_bucket_live; -limit caps matches (default 50).
  verify-external -config PATH [-timeout DUR]
                          Start every enabled off-chain connector
                          (cfg.External.<venue>.enabled = true), drain the
                          shared sink for up to -timeout (default 60s), and
                          print per-venue first-trade/update samples. Exits
                          early once every enabled venue has emitted at
                          least one output. No DB, no Timescale, no cursors.
  verify-archive -config PATH [-bucket NAME] [-from N] [-to N] [-tier MODE] [-archive-root PATH] [-peers URLs] [-peer-samples N] [-archivist-bin BIN] [-archivist-url URL] [-archivist-timeout DUR] [-fail-on-missed] [-max-runtime DUR] [-workers N] [-resume-from-hash HEX] [-metrics-listen ADDR] [-state-file PATH] [-from-last-verified] [-safety-overlap N]
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
                          -fail-on-missed: per ADR-0017 X1.7, treat
                                       checkpointsMissed > 0 as a hard
                                       failure. Default off for the
                                       pre-bootstrap workflow; flip on
                                       after archive-completeness has
                                       been run and the cross-anchor
                                       archive is provably complete.
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
                          Current implementation scope: cross-anchor archive
                          only. Primary MinIO-bucket structural/chain-link
                          enforcement is not shipped in this snapshot.
                          Exit 0 = clean; 1 = at least one missing file remains.
  cross-region-check -regions name=URL,name=URL,... [-pairs PAIR,...] [-metric vwap|twap|ohlc] [-window DUR] [-samples N] [-to TS]
                          Hit each region's /v1/{vwap|twap|ohlc} endpoint
                          for the same closed-bucket window and assert
                          equality across the stable user-visible payload
                          (data, sources, and flags, excluding
                          per-response as_of). Per
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
  supply audit <asset> -config PATH [-cross-check <other-asset>] [-history-hours N]
                          Operator-side audit for ADR-0011 supply
                          derivation. Prints the latest snapshot
                          from asset_supply_history (total /
                          circulating / max / basis / observed_at /
                          ledger). When -cross-check is supplied,
                          fetches the counterpart's snapshot and
                          runs the SAC-wrapped cross-check from
                          PR #216 (asserts the two totals agree
                          within 1 stroop per ADR-0011). When
                          -history-hours is set, also prints the
                          recent N-hour snapshot trail so an
                          operator can spot whether divergence is
                          fresh or chronic. Asset accepts the
                          canonical wire form (native | CODE-G… |
                          C…). Cross-check pairing is operator-
                          supplied because SAC contract-id
                          derivation isn't wired in canonical yet.
  supply snapshot -config PATH [-asset <id>] [-ledger N] [-dry-run]
                          Compute a fresh supply snapshot and write
                          it to asset_supply_history. The CLI is
                          intentionally native-XLM only — classic
                          (Algorithm 2) + SEP-41 (Algorithm 3)
                          computers shipped (Tasks #55, #56) but
                          their refresh surface is the aggregator
                          goroutine path ([supply].aggregator_refresh_enabled).
                          Reserve balances come from the chained-
                          fallback reader: live LCM AccountEntry
                          observer (L2.12a) wins when populated;
                          operator-static [supply].reserve_balances_stroops
                          is the bring-up fallback. Default ledger
                          attribution is the max last_ledger across
                          all ingestion cursors; pass -ledger to
                          override. -dry-run prints without writing.
  discovery list -config PATH [-since DUR] [-limit N]
                          List SEP-41 contracts auto-detected from the
                          event stream (the dispatcher's discovery
                          hook from #225 + the indexer wire-up from
                          #230 populate discovered_assets in
                          production). Output is one row per
                          contract: contract_id, first_seen_at,
                          first_seen_event, event_count. Ordered by
                          first_seen_at DESC so the most-recent
                          arrivals show up first.
                            -since 1h    only contracts first seen
                                         within the last 1h; default
                                         empty (no filter, full table)
                            -limit 100   cap result rows; default 100
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
                              -checkpoint-dir /tmp/walk-checkpoint \
                              > soroswap-wasm-history.json
                          When -checkpoint-dir is set, each parallel
                          worker also writes its observed transitions
                          to <dir>/wasm-history-w<i>.jsonl. Recover the
                          canonical JSON from a crashed run with
                          wasm-history-merge-jsonl below.
  wasm-history-merge-jsonl -checkpoint-dir DIR -to N [-output PATH]
                          Reconstruct the canonical wasm-history JSON
                          from per-worker JSONL transition logs left
                          behind by a crashed wasm-history run with
                          -checkpoint-dir set. -to MUST match the
                          original walk's upper bound (closes the last
                          open range per contract). Output is the same
                          JSON shape wasm-history writes at end-of-run.
                          Empty-history contracts (the "ran but saw no
                          transitions" signal) are NOT emitted — the
                          JSONL only carries observed transitions.
                          Example:
                            ratesengine-ops wasm-history-merge-jsonl \
                              -checkpoint-dir /tmp/walk-checkpoint \
                              -to 62249727 \
                              -output recovered.json
  extract-wasm-from-galexie -config PATH -hashes HEX,HEX,... -output-dir DIR [-from N] [-to N] [-parallel N] [-bucket NAME]
                          Extract raw WASM bytes for one or more contract-
                          code hashes by walking the local galexie LCM
                          archive. Writes <hash>.wasm files into
                          -output-dir. Companion to wasm-history: walk the
                          history first to enumerate hashes, then run this
                          to pull bytes for the (likely-evicted from current
                          ledger state) older versions. r1's full archive is
                          the truer source than RPC getLedgerEntry —
                          works offline, doesn't depend on TTL retention.
                          Example:
                            ratesengine-ops extract-wasm-from-galexie \
                              -config /etc/ratesengine.toml \
                              -from 50457424 -to 62296694 -parallel 8 \
                              -hashes 4a64c8c8...,b400f7a8... \
                              -output-dir /var/wasm-audit
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
  backfill-chainlink -config PATH [-from-block N] [-to-block N] [-chunk-blocks N] [-sleep-ms N] [-dry-run]
                          Walk every configured Chainlink feed's
                          AnswerUpdated event log across the requested
                          block range and insert one OracleUpdate row
                          per historical round into oracle_updates.
                          Idempotent (deterministic synthesised
                          tx_hash + ON CONFLICT). ~33k eth_getLogs
                          calls / 7h wall time for all 516 mainnet
                          feeds at 5k blocks/chunk on Alchemy free
                          tier — run overnight. Example:
                            ratesengine-ops backfill-chainlink \
                              -config /etc/ratesengine.toml \
                              -from-block 15537393  # post-Merge marker
                              -sleep-ms 50          # ~20 req/s polite cap
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
  seed-soroswap-pairs -config PATH [-rpc URL] [-timeout DUR]
                          Bootstrap the soroswap_pairs registry table
                          via stellar-rpc simulateTransaction. Walks the
                          factory's all_pairs() / token_0() / token_1()
                          view functions and upserts each (pair, token0,
                          token1) tuple. Run once on first deployment;
                          live new_pair events keep the table fresh
                          afterwards (see migrations/0016_create_soroswap_pairs.up.sql).
                          ~3N+1 RPC calls at 300ms throttle, so wall-time
                          scales linearly with pair count (~3 min for 200
                          pairs). Idempotent — re-running is safe.
  seed-entry-counts -config PATH [-timeout DUR]
                          Authoritatively recompute source_entry_counts
                          (the "entries" column on /v1/diagnostics/
                          ingestion) from a full GROUP BY over trades +
                          oracle_updates. The writers keep it live going
                          forward; this one-shot folds in pre-counter
                          history + any crash drift. Run ONCE post-
                          backfill (scans every trades chunk). Idempotent
                          — SETs not ADDs, so re-running converges.
  trim-galexie-archive -config PATH -older-than-ledger N [-dry-run|-commit] [-no-verify-upstream] [-max-files N]
                          Per ADR-0027 §Step 2: DESTRUCTIVE — deletes
                          LCM files from the local hot tier
                          (galexie-archive on MinIO) whose ledger range
                          is entirely below -older-than-ledger, after
                          verifying their presence in the cold tier
                          (aws-public-blockchain). Reclaims pool
                          capacity by tiering off historical mirror.
                          Safety stack:
                            * --dry-run is the DEFAULT when neither
                              flag is set; --commit MUST be explicit.
                            * --verify-upstream is the DEFAULT; every
                              candidate is HEAD'd against cold before
                              deletion. --no-verify-upstream skips
                              this and is NOT RECOMMENDED.
                            * --max-files caps deletions per run
                              (default 100000) — a typo cannot delete
                              the full archive in one shot.
                            * --older-than-ledger is REQUIRED — no
                              implicit "trim everything below tip - N".
                            * Cold tier MUST be configured. Refuses
                              to run otherwise.
                          Rollback: ratesengine-ops
                          rehydrate-galexie-archive -from N -to N.
  rehydrate-galexie-archive -config PATH -from N -to N [-dry-run]
                          Per ADR-0027 §Step 2: copy LCM files for the
                          ledger range [-from, -to] from the configured
                          cold tier (storage.s3_cold_*; production is the
                          aws-public-blockchain bucket) back into the
                          local hot tier (storage.s3_bucket_archive on
                          MinIO). Idempotent — uses PutFileIfNotExists,
                          so files already present in hot are skipped.
                          -dry-run reports the file list + skipped vs
                          would-copy counts without writing.
                          Use cases:
                            * recover from accidental trim;
                            * pre-warm hot before a planned backfill;
                            * cold-tier integrity spot check (surfaces
                              files missing in cold via the
                              missing_in_cold counter).
                          Refuses to run if cold tier is not configured
                          (cfg.Storage.ColdTieringEnabled() == false).
  mint-key -config PATH -identifier ID -label LABEL [-tier T] [-rate-limit-per-min N] [-expires-in DUR]
                          Issue a fresh API key directly via the
                          Redis API-key store. Operator-only path
                          to bootstrap a customer's first key —
                          /v1/account/keys self-service can't be
                          hit until a pre-existing authenticated
                          subject already exists (chicken + egg).
                          Plaintext goes to stdout; audit metadata
                          (KeyID, Tier, CreatedAt) goes to stderr,
                          so a >key.txt redirect captures only
                          the secret. Plaintext is shown ONCE —
                          unrecoverable. Pipe stdout to an
                          encrypted transport (Bitwarden, vault,
                          encrypted email) immediately. Tiers:
                          apikey | sep10 | operator. Stripe webhook
                          integration (future) calls the same
                          internal/auth.RedisAPIKeyStore.Create
                          path from a small HTTP handler instead
                          of from the CLI.
                          Example:
                            ratesengine-ops mint-key \
                              -config /etc/ratesengine.toml \
                              -identifier customer-acme-corp \
                              -label 'ACME Corp - production' \
                              -tier apikey \
                              -rate-limit-per-min 1000
  upgrade-key -config PATH -key-id KID -rate-limit-per-min N
                          Lift (or lower) an existing API key's
                          per-minute rate-limit budget. Operator-
                          side path for manual paid-tier upgrades
                          before the Stripe webhook ships; same
                          internal/auth.RedisAPIKeyStore.UpdateRateLimit
                          path the future webhook will call.
                          The customer's existing plaintext key
                          keeps working — they don't need to
                          rotate to pick up the new budget;
                          effective on the next request.
                          Tier reference (matches the /signup page):
                              1000 = Starter,  10000 = Pro,
                             50000 = Business, custom = Enterprise.
                          Example:
                            ratesengine-ops upgrade-key \
                              -config /etc/ratesengine.toml \
                              -key-id kid_515c8d94191f4e93 \
                              -rate-limit-per-min 10000
  emit-incident -config PATH -slug SLUG -event {sev1|resolved}
                          Fan out one incident.sev1 or
                          incident.resolved customer webhook for
                          the named incident slug. The slug must
                          already exist in internal/incidents/data/
                          (embedded into this binary at build
                          time). F-1249 (codex audit-2026-05-12):
                          part of the SEV runbook — draft the .md,
                          merge + deploy, then emit. See
                          docs/operations/sev-playbook.md.
                          Example:
                            ratesengine-ops emit-incident \
                              -config /etc/ratesengine.toml \
                              -slug 2026-05-12-redis-blip \
                              -event sev1
  version                 Print version + build date.
  help                    This help.
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
//
//nolint:gocognit,gocyclo,funlen // ops-CLI subcommand: flag parsing + venue dispatch + insert-loop in one function is the most readable shape
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

	// Mirror the indexer's USD-volume wiring (L2.2 phase 1) so an
	// ops-driven backfill of on-chain trades populates usd_volume
	// the same way live ingest does.
	if len(cfg.Trades.USDPeggedClassicAssets) > 0 {
		spec, err := timescale.NewUSDVolumeQuoteSpec(
			cfg.Trades.USDPeggedClassicAssets,
			cfg.Supply.SACWrappers,
		)
		if err != nil {
			return fmt.Errorf("usd-volume quote spec: %w", err)
		}
		store.SetUSDVolumeQuoteSpec(spec)
	}

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
		// F-1237 (codex audit-2026-05-13): mirror the
		// indexer/aggregator wiring — bind the verified-currency
		// catalogue's CMC IDs so the poller queries by
		// `id=<numeric>` instead of the ambiguous `symbol=`
		// path. Without this the verify-external run uses the
		// ambiguous symbol path and an operator auditing CMC
		// drift sees stale-shape data even when the indexer is
		// on the safe path.
		if cat, catErr := currency.LoadEmbedded(); catErr == nil {
			p.CMCIDs = cat.CoinMarketCapIDs()
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

// verifyArchive runs one or more verification tiers against a
// galexie bucket. Per `docs/operations/galexie-backfill.md` and
// ADR-0017, each tier addresses a distinct trust failure mode:
//
//   - Tier A (chain): chain-link integrity — for each ledger N,
//     ledger[N].Header.PreviousLedgerHash == ledger[N-1].Hash.
//     Catches internal corruption, dropped ledgers, replay
//     divergence regardless of upstream trust.
//   - Tier B (checkpoint): cross-check our LCM's hash at every
//     64-ledger checkpoint against the canonical header-hash
//     in the local history-archive (`ledger-XXXXXXXX.xdr.gz`).
//     Catches single-source corruption that's still chain-link-
//     consistent.
//   - Tier D (peers): sample checkpoints within the range and
//     cross-compare history-XXXXXXXX.json across N tier-1
//     validator archives. Consensus-level cryptographic
//     agreement.
//   - Tier E (archivist): shell out to stellar-archivist for a
//     full bucket-by-bucket sha256 audit.
//
// `-tier all` runs every tier sequentially. Any tier mismatch is
// a hard stop with the diverging ledger numbers and hashes
// printed for diagnosis.
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
	failOnMissed := fs.Bool("fail-on-missed", false,
		"Treat checkpointsMissed > 0 as a hard failure (ADR-0017 X1.7). "+
			"Default off for backward compat with the operator workflow that "+
			"tolerated scattered missed checkpoints; flip to true after the "+
			"cross-anchor archive bootstrap completes (PRs #200/#202/#203).")
	maxRuntime := fs.Duration("max-runtime", 24*time.Hour,
		"Hard cap on total verification runtime. 0 = no cap (run until "+
			"completion or operator interrupt). Default 24h matches the "+
			"backward-compat behaviour but full-archive runs that exceed "+
			"the cap need 0 to avoid context-deadline-exceeded mid-walk.")
	workers := fs.Int("workers", 1,
		"Parallel chunk-walk workers. Each handles a contiguous "+
			"sub-range; cross-chunk chain integrity is stitched after "+
			"all workers complete. 1 (default) preserves the historical "+
			"single-threaded path; 4-8 speeds full-archive runs ~Nx "+
			"until disk I/O on /var/lib/minio saturates. Range [1, 16].")
	resumeFromHash := fs.String("resume-from-hash", "",
		"Expected hash (hex) of the ledger immediately before -from "+
			"(i.e. ledger -from − 1). When set, the first chunk's "+
			"FirstPrevHash must match this value or verification fails. "+
			"Used after a previous run halted partway: the operator "+
			"records the previous run's last verified ledger hash and "+
			"passes it here to prove the cross-run boundary explicitly. "+
			"Empty (default) skips the check — the implicit-overlap "+
			"proof from re-reading -from itself is usually sufficient.")
	metricsListen := fs.String("metrics-listen", "",
		"Bind address for a Prometheus /metrics endpoint scraped during "+
			"the run (e.g. 127.0.0.1:9479). Per-chunk counters + gauges "+
			"let operators dashboard the bottleneck during multi-hour "+
			"sweeps rather than guessing from log tails. Empty (default) "+
			"disables the endpoint.")
	stateFile := fs.String("state-file", "",
		"Path to a JSON state file persisting LastVerifiedLedger per "+
			"tier across runs (e.g. /var/lib/ratesengine/verify-archive-state.json). "+
			"Empty disables both reading and writing — every run is "+
			"full-from-scratch, matching the pre-incremental behaviour.")
	fromLastVerified := fs.Bool("from-last-verified", false,
		"Compute -from from the prior state's LastVerifiedLedger minus "+
			"the safety overlap window, instead of using the -from flag "+
			"value directly. Requires -state-file. Skipped (falls back "+
			"to -from) when the state file is missing or has no prior "+
			"entry for this tier.")
	safetyOverlap := fs.Uint("safety-overlap", 5000,
		"Number of ledgers to re-verify behind the prior LastVerifiedLedger "+
			"when -from-last-verified is set. Catches any anomalies that "+
			"snuck in just before the last run's high-water mark. Default "+
			"5000 ledgers ≈ 17h of chain history at 12s/ledger.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *workers < 1 || *workers > 16 {
		return fmt.Errorf("-workers must be in [1, 16] (got %d)", *workers)
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

	// Optional /metrics endpoint. Only the chunk walk emits metrics
	// today (Tiers D + E are bounded-time spot checks, not the
	// multi-hour grind that motivates live dashboarding).
	if *metricsListen != "" {
		stop, err := startVerifyArchiveMetrics(*metricsListen)
		if err != nil {
			return fmt.Errorf("metrics endpoint: %w", err)
		}
		defer stop()
		fmt.Fprintf(os.Stderr, "verify-archive: metrics on http://%s/metrics\n", *metricsListen)
	}

	// Incremental run support — when -state-file is set, read prior
	// state. -from-last-verified overrides the operator's -from with
	// (prior.LastVerifiedLedger - safety_overlap). The resume hash
	// from prior state feeds -resume-from-hash for strict cross-run
	// chain continuity (so an operator running incremental nightly
	// runs gets the same continuity proof an unbroken single-process
	// walk would have).
	priorState, stateErr := readVerifyArchiveState(*stateFile)
	if stateErr != nil {
		return stateErr
	}
	effectiveFrom := uint32(*from)
	effectiveResumeHash := *resumeFromHash
	if *fromLastVerified && *stateFile != "" {
		effectiveFrom = resolveIncrementalFrom(priorState, *tier, uint32(*from), uint32(*safetyOverlap))
		if effectiveResumeHash == "" {
			effectiveResumeHash = resolveIncrementalResumeHash(priorState, *tier)
		}
		fmt.Fprintf(os.Stderr, "verify-archive: incremental run, prior state high-water=%d → effective -from=%d (safety overlap %d)\n",
			priorState.Tiers[*tier].LastVerifiedLedger, effectiveFrom, *safetyOverlap)
	}

	// Tier A + B (LCM walk via ledgerstream). Skipped when tier=peers.
	if doChain || doCheckpoint {
		highestLedger, highestHash, err := verifyArchiveLCMWalk(cfg, bucket, effectiveFrom, uint32(*to), *maxRuntime, *workers,
			doChain, doCheckpoint, *archiveRoot, *failOnMissed, effectiveResumeHash)
		if err != nil {
			return err
		}
		// Persist incremental state on success. Tier name comes from
		// the operator's -tier flag — "chain", "checkpoint", or "all"
		// (we record under each underlying tier so a future
		// `-tier chain -from-last-verified` run reads the right one).
		if *stateFile != "" && highestLedger > 0 {
			now := time.Now().UTC()
			newState := priorState
			if doChain {
				newState = updateTierState(newState, "chain", highestLedger, highestHash, now)
			}
			if doCheckpoint {
				newState = updateTierState(newState, "checkpoint", highestLedger, "", now)
			}
			if err := writeVerifyArchiveState(*stateFile, newState); err != nil {
				return fmt.Errorf("write state %s: %w", *stateFile, err)
			}
			fmt.Fprintf(os.Stderr, "verify-archive: state advanced to ledger %d (file: %s)\n",
				highestLedger, *stateFile)
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
//
// failOnMissed: when true, a non-zero checkpointsMissed at the end
// of the walk is treated as a hard failure per ADR-0017 X1.7.
// When false (default), missed checkpoints are reported but tolerated
// — matches the pre-bootstrap operator workflow.
// verifyArchiveLCMWalk returns (highestLedger, highestLedgerHashHex,
// err). On success, highestLedger is the maximum LastSeq across all
// chunk results — used by the caller to advance the persisted
// verify-archive state. highestLedgerHashHex is hex-encoded;
// callers carry it forward as -resume-from-hash on the next run.
func verifyArchiveLCMWalk(cfg config.Config, bucket string, from, to uint32, maxRuntime time.Duration, workers int, doChain, doCheckpoint bool, archiveRoot string, failOnMissed bool, resumeFromHash string) (uint32, string, error) { //nolint:funlen,gocognit,gocyclo
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

	// maxRuntime == 0 → no cap (uncancellable parent). Operators
	// pass 0 for full-archive runs that exceed any single-day
	// budget; the binary still honours external SIGTERM via the
	// SDK's signal hooks.
	var (
		ctx    context.Context
		cancel context.CancelFunc
	)
	if maxRuntime > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), maxRuntime)
	} else {
		ctx, cancel = context.WithCancel(context.Background())
	}
	defer cancel()

	// Resolve `-to=0` to the current tip when parallel chunking was
	// asked for. `splitRange(from, 0, n)` hits the `to <= from` guard
	// and silently returns ONE chunk — `-workers N` is then dead code,
	// and what should be an N-way parallel walk degrades to a serial
	// one. Bit me on a manual `-from 2 -to 0 -workers 6` bootstrap run
	// that crawled for 22h instead of ~4h. The systemd timer's
	// `-from-last-verified` incremental mode hit the same shape on
	// every fresh-state bootstrap.
	//
	// Resolution: build a one-shot DataStore from the same DataStore
	// config the walkers will use, query FindLatestLedgerSequence,
	// adopt that as the upper bound for splitRange. Closed
	// immediately — the parallel walkers each construct their own.
	// Skipped when workers ≤ 1 (single-chunk serial walk is what
	// `to=0` is FOR; resolving tip there would defeat the live-tail
	// path) and when `to` already names an explicit upper bound.
	//
	// Fail-soft: tip resolution AND the per-chunk workers'
	// BoundedRange PrepareRange both require bucket `ListObjectsV2`
	// permission. Setups with least-privilege MinIO IAM (r1's
	// `ratesengine_reader` grants GetObject only) deny it. Rather
	// than crash the whole walk, log a clear message and demote to
	// single-chunk serial (UnboundedRange — works without List, the
	// pre-this-fix behaviour). An operator who genuinely wants the
	// parallel speedup grants `s3:ListBucket` to the reader and the
	// next walk picks it up automatically.
	if to == 0 && workers > 1 {
		const listGrantHint = "verify-archive: " +
			"falling back to single-chunk serial walk. Parallel mode " +
			"requires bucket ListObjectsV2 (BoundedRange PrepareRange " +
			"in the per-chunk workers needs it too, not just this tip " +
			"resolution); grant `s3:ListBucket` to the reader IAM to " +
			"enable -workers N parallelism."

		ds, dsErr := datastore.NewDataStore(ctx, lsCfg.DataStore)
		if dsErr != nil {
			fmt.Fprintf(os.Stderr, "verify-archive: tip resolution failed (open datastore: %v); %s\n", dsErr, listGrantHint)
			workers = 1
		} else {
			tip, tipErr := datastore.FindLatestLedgerSequence(ctx, ds)
			_ = ds.Close()
			if tipErr != nil {
				fmt.Fprintf(os.Stderr, "verify-archive: tip resolution failed (find latest ledger: %v); %s\n", tipErr, listGrantHint)
				workers = 1
			} else {
				fmt.Fprintf(os.Stderr, "verify-archive: resolved -to=0 → tip %d for %d-way parallel split\n", tip, workers)
				to = tip
			}
		}
	}

	chunks := splitRange(from, to, workers)
	progressEvery := 10 * time.Second
	startedAt := time.Now()

	if len(chunks) > 1 {
		fmt.Fprintf(os.Stderr, "verify-archive: %d workers across %d chunks of ~%d ledgers each\n",
			workers, len(chunks), (to-from+1)/uint32(workers))
	}

	results, walkErr := runVerifyChunks(
		ctx, lsCfg, chunks,
		doChain, doCheckpoint, archiveRoot,
		startedAt, progressEvery,
	)

	// Aggregate counters across chunks for the final summary. Match
	// the pre-parallel field naming so log-scrapers don't break.
	// highestLedger / highestHash are reported back to the caller so
	// it can persist incremental-run state via -state-file.
	var (
		verified          int
		mismatches        int
		checkpointsOK     int
		checkpointsMissed int
		highestLedger     uint32
		highestHashHex    string
	)
	for _, r := range results {
		verified += r.Verified
		mismatches += r.Mismatches
		checkpointsOK += r.CheckpointsOK
		checkpointsMissed += r.CheckpointsMissed
		if r.LastSeq > highestLedger {
			highestLedger = r.LastSeq
			highestHashHex = fmt.Sprintf("%x", r.LastHash[:])
		}
	}

	// Stitch cross-chunk boundary chain integrity. Skip on walkErr
	// (chunks may have aborted mid-flight; boundary check would be
	// noisy on partial results).
	var stitchErr error
	if walkErr == nil && doChain {
		stitchErr = stitchChunks(results)
	}

	// Cross-run boundary check: when -resume-from-hash is set, the
	// first chunk's FirstPrevHash must match (proves continuity with
	// a previous verification run that ended at -from − 1). Runs
	// only when no other error has fired and at least one chunk
	// processed a ledger.
	var resumeErr error
	if walkErr == nil && stitchErr == nil && doChain && resumeFromHash != "" && len(results) > 0 && results[0].Verified > 0 {
		resumeErr = checkResumeFromHash(resumeFromHash, results[0].FirstPrevHash, results[0].FirstSeq)
	}

	elapsed := time.Since(startedAt)
	fmt.Fprintf(os.Stderr, "\nverify-archive: verified %d ledgers in %s (%.0f ledgers/s, %d workers)\n",
		verified, elapsed.Round(time.Second), float64(verified)/elapsed.Seconds(), workers)
	if doCheckpoint {
		if failOnMissed {
			fmt.Fprintf(os.Stderr, "verify-archive: checkpoints matched=%d missed=%d (fail-on-missed: any miss = hard failure)\n",
				checkpointsOK, checkpointsMissed)
		} else {
			fmt.Fprintf(os.Stderr, "verify-archive: checkpoints matched=%d missed=%d (missed = archive file absent, not a failure)\n",
				checkpointsOK, checkpointsMissed)
		}
	}
	if walkErr != nil {
		return 0, "", fmt.Errorf("verification FAILED: %w", walkErr)
	}
	if stitchErr != nil {
		return 0, "", fmt.Errorf("verification FAILED: %w", stitchErr)
	}
	if resumeErr != nil {
		return 0, "", fmt.Errorf("verification FAILED: %w", resumeErr)
	}
	if verified == 0 {
		return 0, "", fmt.Errorf("verified 0 ledgers — bucket empty or range out of scope")
	}
	if doChain {
		fmt.Fprintf(os.Stderr, "verify-archive: chain-link integrity OK ✓\n")
	}
	if doCheckpoint {
		if checkpointsOK == 0 && checkpointsMissed > 0 {
			fmt.Fprintf(os.Stderr, "verify-archive: checkpoint anchor INCONCLUSIVE — %d missed, 0 matched (archive mirror may be stale)\n", checkpointsMissed)
			if failOnMissed {
				return 0, "", fmt.Errorf("verification FAILED: checkpoint anchor inconclusive — %d missed, 0 matched (with -fail-on-missed)", checkpointsMissed)
			}
		} else {
			fmt.Fprintf(os.Stderr, "verify-archive: checkpoint anchor OK ✓  (%d matched, %d missed)\n", checkpointsOK, checkpointsMissed)
		}
		if failOnMissed && checkpointsMissed > 0 {
			return 0, "", fmt.Errorf("verification FAILED: %d checkpoint(s) missing from cross-anchor archive (with -fail-on-missed per ADR-0017 X1.7)", checkpointsMissed)
		}
	}
	_ = mismatches // reserved for future exit-code semantics
	return highestLedger, highestHashHex, nil
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

// storageChange is one observation of a watched contract's
// non-Instance ContractData entry being Created/Updated/Restored.
// Captures *what changed* (key + change type) at *when* (ledger),
// without trying to interpret the value (raw key XDR is enough for
// downstream replay / classification).
//
// Used by the optional `-track-storage-rotations` mode to catch
// admin storage flips like Soroswap factory's `set_pair_wasm`
// rotation, factory parameter changes (fee_to_setter, etc.) — all
// the things wasm-history's instance-only filter ignores.
type storageChange struct {
	Ledger     uint32 `json:"ledger"`
	ChangeType string `json:"change_type"` // created | updated | restored
	KeyXDRB64  string `json:"key_xdr_b64"`
	KeyHint    string `json:"key_hint,omitempty"`   // best-effort human-readable summary
	Durability string `json:"durability,omitempty"` // persistent | temporary
}

// contractStorageHistory is the per-contract output shape for the
// storage-rotation tracker. One entry per watched contract that
// had ANY observed non-Instance ContractData change.
type contractStorageHistory struct {
	Contract string          `json:"contract"`
	Changes  []storageChange `json:"changes"`
}

// codeUpload is one observation of a `ContractCode` LedgerEntry
// being Created or Restored — i.e. someone's UploadContractWasm
// host-fn invocation deposited a new WASM blob into ledger state.
//
// Captured globally (not per-watched-contract) because the WASM
// upload is a one-shot event that any contract may later reference
// via its ExecutableHash. Tracking it lets us preserve a complete
// archive of "every WASM ever uploaded over the walked window" for
// retroactive cross-reference — companion to the on-chain
// Soroban-RPC fetch path (which only works for live, non-evicted
// hashes).
type codeUpload struct {
	Ledger     uint32 `json:"ledger"`
	WasmHash   string `json:"wasm_hash"`
	SizeBytes  int    `json:"size_bytes"`
	ChangeType string `json:"change_type"` // created | restored
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
	checkpointDir := fs.String("checkpoint-dir", "",
		"Optional directory to write per-worker JSONL transition logs into. "+
			"Each transition (one wasm-hash change for one watched contract) is "+
			"appended as one line: {contract, wasm_hash, at_ledger}. Useful for "+
			"long-running walks where the final JSON output is at risk if any "+
			"worker dies mid-flight (the JSON is only written at full completion). "+
			"Files are named <dir>/wasm-history-w<worker>.jsonl. Run "+
			"`ratesengine-ops wasm-history-merge-jsonl -checkpoint-dir <dir> -to N` "+
			"to recover the canonical wasm-history JSON from a partial run.")
	storageOut := fs.String("storage-rotations-out", "",
		"Optional path to write the per-watched-contract storage-rotation log "+
			"as a JSON document. When set, every Created/Updated/Restored "+
			"ContractData entry whose key is NOT LedgerKeyContractInstance is "+
			"recorded. Used to catch admin storage flips like Soroswap factory's "+
			"set_pair_wasm rotation that the wasm-hash-only walker doesn't see. "+
			"Empty = feature off (default).")
	codeOut := fs.String("code-uploads-out", "",
		"Optional path to write a JSON log of every ContractCode entry "+
			"(Created/Restored) observed in the walked range. Captures the "+
			"WASM-upload events themselves, independent of which contract "+
			"references the resulting hash. Output: [{ledger, wasm_hash, "+
			"size_bytes, change_type}]. Empty = feature off (default).")
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

	// Validate / prepare the optional checkpoint dir.
	if *checkpointDir != "" {
		if st, statErr := os.Stat(*checkpointDir); statErr != nil {
			return fmt.Errorf("-checkpoint-dir %q: %w", *checkpointDir, statErr)
		} else if !st.IsDir() {
			return fmt.Errorf("-checkpoint-dir %q is not a directory", *checkpointDir)
		}
		fmt.Fprintf(os.Stderr, "wasm-history: per-worker JSONL transition log → %s/wasm-history-w<i>.jsonl\n", *checkpointDir)
	}

	// Split the range into N contiguous chunks. Worker i gets
	// [from + i*size, from + (i+1)*size - 1] except the last
	// worker absorbs the remainder.
	trackStorage := *storageOut != ""
	trackCode := *codeOut != ""
	workerStates, totalScanned, err := runWasmHistoryWorkers(
		ctx, lsCfg, watch, uint32(*from), uint32(*to), int(*parallel), trackStorage, trackCode,
		uint64(*progressEvery), *checkpointDir)
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

	// Tier-2 outputs (storage rotations + code uploads) are written
	// to separate JSON files so the main wasm-history JSON shape on
	// stdout stays backward-compatible. Each tier-2 feature is opt-in
	// via its `-out` flag; when unset, no extra output is produced.
	if trackStorage {
		if err := writeStorageRotationsOutput(*storageOut, watch, workerStates); err != nil {
			return fmt.Errorf("write storage rotations: %w", err)
		}
	}
	if trackCode {
		if err := writeCodeUploadsOutput(*codeOut, workerStates); err != nil {
			return fmt.Errorf("write code uploads: %w", err)
		}
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

// writeStorageRotationsOutput merges per-worker storage-change
// slices in worker order (which is ledger order across the
// chunk-partitioned range) and writes them to path as a JSON array.
func writeStorageRotationsOutput(
	path string,
	watch map[sdkxdr.Hash]string,
	workers []workerResult,
) error {
	merged := make(map[sdkxdr.Hash][]storageChange)
	for _, w := range workers {
		for h, changes := range w.storageChanges {
			merged[h] = append(merged[h], changes...)
		}
	}
	out := make([]contractStorageHistory, 0, len(merged))
	for h, changes := range merged {
		out = append(out, contractStorageHistory{
			Contract: watch[h],
			Changes:  changes,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Contract < out[j].Contract })

	f, err := os.Create(path) //nolint:gosec // operator-supplied output path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wasm-history: wrote %d contract(s)' storage rotations to %s\n", len(out), path)
	return nil
}

// writeCodeUploadsOutput merges per-worker code-upload slices in
// worker order (= ledger order) and writes to path as a JSON array.
// Deduplicates by (ledger, hash) since the same upload can land in
// adjacent worker chunks.
func writeCodeUploadsOutput(path string, workers []workerResult) error {
	var all []codeUpload
	for _, w := range workers {
		all = append(all, w.codeUploads...)
	}
	// Sort by ledger then hash for stable output.
	sort.Slice(all, func(i, j int) bool {
		if all[i].Ledger != all[j].Ledger {
			return all[i].Ledger < all[j].Ledger
		}
		return all[i].WasmHash < all[j].WasmHash
	})
	// Dedupe (rare across worker boundaries; cheap O(n) pass).
	dedup := all[:0]
	var prev codeUpload
	for _, u := range all {
		if u.Ledger == prev.Ledger && u.WasmHash == prev.WasmHash && u.ChangeType == prev.ChangeType {
			continue
		}
		dedup = append(dedup, u)
		prev = u
	}

	f, err := os.Create(path) //nolint:gosec // operator-supplied output path
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	if err := enc.Encode(dedup); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "wasm-history: wrote %d code upload(s) to %s\n", len(dedup), path)
	return nil
}

// wasmHistoryMergeJSONL reconstructs the canonical wasm-history JSON
// output from the per-worker JSONL transition logs that
// `wasm-history -checkpoint-dir` produced. Used to recover from a
// walk that died after writing transitions to JSONL but before
// reaching its end-of-run JSON write.
//
// Required flags:
//   - -checkpoint-dir: directory containing wasm-history-w*.jsonl files.
//   - -to:             upper-bound ledger from the original walk's range.
//     Used to close the last open range per contract.
//
// Optional:
//   - -output: path to write the merged JSON to. Empty = stdout.
//
// The merge logic mirrors what `wasmHistory` does at end-of-run
// (see [mergeWasmHistories]):
//
//  1. Read every wasm-history-w*.jsonl in lexical order (which is
//     worker order — w0, w1, …).
//  2. Per contract, collect all transitions across all workers.
//  3. Sort each contract's transitions by at_ledger. Within a single
//     worker the transitions are already in ledger-ascending order;
//     across workers, sort merges them.
//  4. Collapse adjacent same-hash transitions (a worker's first
//     observation of a contract that already has the same hash from
//     the previous worker is not a real transition).
//  5. Build wasmRange[]: each transition starts a range that closes
//     at the next transition's at_ledger - 1; the last range closes
//     at -to.
//  6. Emit the same JSON shape `wasmHistory` does.
//
// Empty-history contracts (the "ran but saw nothing" signal that
// wasmHistory emits as `{"contract":"...","ranges":null}`) are NOT
// emitted by this tool because the JSONL only carries observed
// transitions. The original walk's JSON IS the canonical artefact;
// this tool's purpose is purely "recover what we did see when the
// walk crashed."
func wasmHistoryMergeJSONL(args []string) error {
	fs := flag.NewFlagSet("wasm-history-merge-jsonl", flag.ContinueOnError)
	checkpointDir := fs.String("checkpoint-dir", "",
		"Directory containing wasm-history-w*.jsonl files (required).")
	to := fs.Uint("to", 0,
		"Upper-bound ledger from the original walk's range (required). "+
			"Closes the last open range per contract.")
	output := fs.String("output", "",
		"Output path. Empty = stdout.")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *checkpointDir == "" {
		return fmt.Errorf("-checkpoint-dir is required")
	}
	if *to == 0 {
		return fmt.Errorf("-to is required (the original walk's upper bound)")
	}

	pattern := filepath.Join(*checkpointDir, "wasm-history-w*.jsonl")
	paths, err := filepath.Glob(pattern)
	if err != nil {
		return fmt.Errorf("glob %s: %w", pattern, err)
	}
	if len(paths) == 0 {
		return fmt.Errorf("no wasm-history-w*.jsonl files in %s", *checkpointDir)
	}
	sort.Strings(paths) // lexical = worker-index order
	fmt.Fprintf(os.Stderr, "wasm-history-merge-jsonl: reading %d JSONL file(s) from %s\n",
		len(paths), *checkpointDir)

	// contract → transitions in observation order across all workers.
	transitions := make(map[string][]transitionRecord)
	totalLines := 0
	for _, path := range paths {
		n, err := readTransitionJSONL(path, transitions)
		if err != nil {
			return fmt.Errorf("read %s: %w", path, err)
		}
		fmt.Fprintf(os.Stderr, "  %s: %d transition(s)\n", filepath.Base(path), n)
		totalLines += n
	}
	fmt.Fprintf(os.Stderr, "wasm-history-merge-jsonl: %d total transition lines across %d contract(s)\n",
		totalLines, len(transitions))

	// Per contract: sort by at_ledger, collapse adjacent same-hash,
	// build ranges that close at the next transition's at_ledger - 1
	// (or at -to for the last range).
	out := make([]contractHistory, 0, len(transitions))
	for contract, trs := range transitions {
		sort.Slice(trs, func(i, j int) bool { return trs[i].AtLedger < trs[j].AtLedger })
		ranges := buildRangesFromTransitions(trs, uint32(*to))
		if len(ranges) == 0 {
			continue
		}
		out = append(out, contractHistory{Contract: contract, Ranges: ranges})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Contract < out[j].Contract })

	w := io.Writer(os.Stdout)
	if *output != "" {
		f, err := os.Create(*output) //nolint:gosec // operator-supplied output path
		if err != nil {
			return err
		}
		defer func() { _ = f.Close() }()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	if err := enc.Encode(out); err != nil {
		return fmt.Errorf("encode: %w", err)
	}
	if *output != "" {
		fmt.Fprintf(os.Stderr, "wasm-history-merge-jsonl: wrote %d contract(s) to %s\n", len(out), *output)
	}
	return nil
}

// readTransitionJSONL appends every record in path's JSONL to the
// per-contract slice in `transitions`. Returns the number of lines
// successfully decoded (corrupted or partial trailing lines are
// logged + skipped — a crashed walk may have left a half-written
// last line, and "recover what we have" beats "fail outright").
func readTransitionJSONL(path string, transitions map[string][]transitionRecord) (int, error) {
	// gosec G304: path comes from -checkpoint-dir glob expansion; the
	// merge tool is itself a privileged ops command that operators run
	// against operator-chosen paths.
	f, err := os.Open(path) //nolint:gosec // intentional ops-tool file read
	if err != nil {
		return 0, err
	}
	defer func() { _ = f.Close() }()

	dec := json.NewDecoder(f)
	count := 0
	for dec.More() {
		var r transitionRecord
		if err := dec.Decode(&r); err != nil {
			fmt.Fprintf(os.Stderr,
				"wasm-history-merge-jsonl: %s: skipping malformed/truncated line near offset %d (%v)\n",
				filepath.Base(path), dec.InputOffset(), err)
			break
		}
		transitions[r.Contract] = append(transitions[r.Contract], r)
		count++
	}
	return count, nil
}

// buildRangesFromTransitions converts a per-contract sorted
// transition slice into the wasmRange shape `wasmHistory` emits.
// Adjacent same-hash transitions are collapsed (the second one is
// just a downstream worker's first re-observation of an unchanged
// hash). The last open range closes at `to`.
func buildRangesFromTransitions(trs []transitionRecord, to uint32) []wasmRange {
	if len(trs) == 0 {
		return nil
	}
	// Collapse adjacent same-hash entries. We keep the EARLIEST
	// at_ledger for each run (matching the walker's first-observation
	// semantic). Tracking the previous hash via a local string avoids
	// the trs[i-1] index expression that gosec G602 flags as a
	// slice-bound risk.
	collapsed := trs[:0]
	prevHash := ""
	for _, r := range trs {
		if len(collapsed) > 0 && r.WasmHash == prevHash {
			continue
		}
		collapsed = append(collapsed, r)
		prevHash = r.WasmHash
	}
	out := make([]wasmRange, 0, len(collapsed))
	for i, r := range collapsed {
		rng := wasmRange{WasmHash: r.WasmHash, FromLedger: r.AtLedger}
		if i+1 < len(collapsed) {
			rng.ToLedger = collapsed[i+1].AtLedger - 1
		} else {
			rng.ToLedger = to
		}
		out = append(out, rng)
	}
	return out
}

// workerResult is what each parallel worker produces: a state map
// covering its bounded range, plus the actual upper bound it reached
// (used by merge to know where this worker's open ranges should close).
type workerResult struct {
	state map[sdkxdr.Hash]*wasmContractState
	// storageChanges is populated only when -track-storage-rotations
	// is set. Keyed by watched contract hash; per-contract slice is
	// in ledger order within the worker's chunk.
	storageChanges map[sdkxdr.Hash][]storageChange
	// codeUploads is populated only when -track-code-uploads is set.
	// Global per-worker (not per-contract); merged across workers in
	// ledger order.
	codeUploads []codeUpload
	scanned     uint64
	upperEnd    uint32 // last ledger the worker actually saw (inclusive)
}

// runWasmHistoryWorkers splits [from,to] into `parallel` contiguous
// chunks and runs each in its own goroutine. Returns per-worker
// state maps in worker-order plus the total ledgers scanned.
//
// When `checkpointDir` is non-empty, each worker also writes one
// JSONL line per observed transition to
// `<checkpointDir>/wasm-history-w<i>.jsonl`. This gives crash-
// resilience for long-running walks: if a worker dies mid-flight,
// the per-worker JSONL contains every transition it saw before the
// crash. The final stdout JSON is unchanged.
func runWasmHistoryWorkers( //nolint:funlen // worker scaffolding; long function is the cleanest expression of the tier-2 fan-out
	ctx context.Context,
	lsCfg ledgerstream.Config,
	watch map[sdkxdr.Hash]string,
	from, to uint32,
	parallel int,
	trackStorage bool,
	trackCode bool,
	progressEvery uint64,
	checkpointDir string,
) ([]workerResult, uint64, error) {
	if parallel < 1 {
		parallel = 1
	}
	results := make([]workerResult, parallel)
	for i := range results {
		results[i].state = make(map[sdkxdr.Hash]*wasmContractState)
		if trackStorage {
			results[i].storageChanges = make(map[sdkxdr.Hash][]storageChange)
		}
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
			runOneWasmHistoryWorker(ctx, lsCfg, watch, &results[i], i, b,
				progressEvery, checkpointDir, trackStorage, trackCode,
				&totalScanned, startedAt, errCh)
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		return nil, totalScanned.load(), err // first error wins
	}
	return results, totalScanned.load(), nil
}

// runOneWasmHistoryWorker is the per-goroutine body of
// runWasmHistoryWorkers. Extracted so the parent function's
// cognitive complexity stays manageable. Owns one worker chunk's
// scan + optional checkpoint-log lifecycle.
func runOneWasmHistoryWorker( //nolint:funlen,gocognit // worker hot path; refactor would obscure ledger-stream lifecycle
	ctx context.Context,
	lsCfg ledgerstream.Config,
	watch map[sdkxdr.Hash]string,
	result *workerResult,
	workerIdx int,
	b rangeChunk,
	progressEvery uint64,
	checkpointDir string,
	trackStorage bool,
	trackCode bool,
	totalScanned *atomicUint64,
	startedAt time.Time,
	errCh chan<- error,
) {
	result.upperEnd = b.to
	workerScanned := uint64(0)

	// Per-worker transition log (optional). nil → no incremental writes.
	var tlog *transitionLog
	if checkpointDir != "" {
		path := filepath.Join(checkpointDir, fmt.Sprintf("wasm-history-w%d.jsonl", workerIdx))
		t, terr := newTransitionLog(path, watch)
		if terr != nil {
			errCh <- fmt.Errorf("worker %d: open checkpoint %q: %w", workerIdx, path, terr)
			return
		}
		tlog = t
		defer func() {
			if cerr := tlog.Close(); cerr != nil {
				fmt.Fprintf(os.Stderr, "wasm-history: w%d close checkpoint: %v\n", workerIdx, cerr)
			}
		}()
	}

	err := ledgerstream.Stream(ctx, lsCfg, b.from, b.to,
		func(lcm sdkxdr.LedgerCloseMeta) error {
			seq := lcm.LedgerSequence()
			scanLCMForWasmChanges(lcm, watch, result.state, seq, tlog)
			if trackStorage {
				scanLCMForStorageRotations(lcm, watch, result.storageChanges, seq)
			}
			if trackCode {
				result.codeUploads = scanLCMForCodeUploads(lcm, result.codeUploads, seq)
			}
			workerScanned++
			if progressEvery > 0 && workerScanned%progressEvery == 0 {
				total := totalScanned.add(progressEvery)
				rate := float64(total) / time.Since(startedAt).Seconds()
				fmt.Fprintf(os.Stderr, "wasm-history: w%d ledger %d, total scanned %d, %.0f ledgers/s\n",
					workerIdx, seq, total, rate)
			}
			return nil
		},
	)
	result.scanned = workerScanned
	// Add the un-counted residue. F-1239 (codex audit-2026-05-12):
	// `-progress-every 0` means "disable progress output"; the
	// previous unconditional `workerScanned % progressEvery`
	// panicked on divide-by-zero AFTER the expensive ledger walk
	// had finished. Either branch: progressEvery == 0 → add the
	// full workerScanned (nothing was counted in-loop); otherwise
	// add the residue.
	if progressEvery == 0 {
		totalScanned.add(workerScanned)
	} else {
		totalScanned.add(workerScanned % progressEvery)
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		errCh <- fmt.Errorf("worker %d [%d,%d]: %w", workerIdx, b.from, b.to, err)
	}
}

// rangeChunk is one worker's slice of the overall [from,to] range.
type rangeChunk struct{ from, to uint32 }

// splitRange divides [from,to] into n contiguous chunks. The last
// chunk absorbs any remainder so the union exactly covers [from,to].
//
// Degrades to a single chunk when n ≤ 1, the range is single-ledger,
// or n exceeds the range span (would otherwise produce zero-width
// chunks that the downstream walkers can't process).
func splitRange(from, to uint32, n int) []rangeChunk {
	if n <= 1 || to <= from {
		return []rangeChunk{{from, to}}
	}
	span := to - from + 1
	if uint32(n) > span {
		return []rangeChunk{{from, to}}
	}
	width := span / uint32(n)
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
	tlog *transitionLog,
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
					scanLedgerEntryChange(&changes[k], watch, state, seq, tlog)
				}
			}
		case txMeta.V4 != nil:
			for j := range txMeta.V4.Operations {
				changes := txMeta.V4.Operations[j].Changes
				for k := range changes {
					scanLedgerEntryChange(&changes[k], watch, state, seq, tlog)
				}
			}
		default:
			// V1/V2 didn't have ContractData. Skip.
			continue
		}
	}
}

// scanLCMForStorageRotations walks every operation's
// LedgerEntryChanges in lcm and records non-Instance ContractData
// changes for any watched contract. Mirrors scanLCMForWasmChanges
// but with the inverse Key.Type filter.
//
// "Storage rotation" here means any modification to a contract's
// custom storage entries (per-instance balance, factory parameter,
// admin pointer, etc.) that the wasm-history walker's
// instance-only filter ignores. Useful for catching admin storage
// flips like Soroswap factory's `set_pair_wasm` rotation.
func scanLCMForStorageRotations(
	lcm sdkxdr.LedgerCloseMeta,
	watch map[sdkxdr.Hash]string,
	out map[sdkxdr.Hash][]storageChange,
	seq uint32,
) {
	if lcm.V != 1 || lcm.V1 == nil {
		return
	}
	v1 := lcm.V1
	for i := range v1.TxProcessing {
		txMeta := &v1.TxProcessing[i].TxApplyProcessing
		switch {
		case txMeta.V3 != nil:
			for j := range txMeta.V3.Operations {
				changes := txMeta.V3.Operations[j].Changes
				for k := range changes {
					recordStorageChange(&changes[k], watch, out, seq)
				}
			}
		case txMeta.V4 != nil:
			for j := range txMeta.V4.Operations {
				changes := txMeta.V4.Operations[j].Changes
				for k := range changes {
					recordStorageChange(&changes[k], watch, out, seq)
				}
			}
		}
	}
}

// recordStorageChange appends one entry per non-Instance
// ContractData change for a watched contract. Captures the raw key
// XDR (base64) + a best-effort `key_hint` summary for human
// readability. Doesn't decode the value (kept raw to keep the
// scanner's hot path tight).
func recordStorageChange(
	change *sdkxdr.LedgerEntryChange,
	watch map[sdkxdr.Hash]string,
	out map[sdkxdr.Hash][]storageChange,
	seq uint32,
) {
	var entry *sdkxdr.LedgerEntry
	var changeType string
	switch change.Type {
	case sdkxdr.LedgerEntryChangeTypeLedgerEntryCreated:
		entry, changeType = change.Created, "created"
	case sdkxdr.LedgerEntryChangeTypeLedgerEntryUpdated:
		entry, changeType = change.Updated, "updated"
	case sdkxdr.LedgerEntryChangeTypeLedgerEntryRestored:
		entry, changeType = change.Restored, "restored"
	default:
		return
	}
	if entry == nil || entry.Data.Type != sdkxdr.LedgerEntryTypeContractData {
		return
	}
	cd := entry.Data.ContractData
	if cd == nil {
		return
	}
	// Inverse filter to scanLedgerEntryChange's: skip the Instance
	// row (already covered by wasm-history); we only want the
	// non-Instance custom-storage rows.
	if cd.Key.Type == sdkxdr.ScValTypeScvLedgerKeyContractInstance {
		return
	}
	if cd.Contract.Type != sdkxdr.ScAddressTypeScAddressTypeContract || cd.Contract.ContractId == nil {
		return
	}
	contractHash := sdkxdr.Hash(*cd.Contract.ContractId)
	if _, watched := watch[contractHash]; !watched {
		return
	}

	keyB64, err := sdkxdr.MarshalBase64(cd.Key)
	if err != nil {
		// Don't drop the row; emit a placeholder hint and zero-byte
		// key. Operator can still see *that* a change happened.
		keyB64 = ""
	}
	durability := "persistent"
	if cd.Durability == sdkxdr.ContractDataDurabilityTemporary {
		durability = "temporary"
	}
	out[contractHash] = append(out[contractHash], storageChange{
		Ledger:     seq,
		ChangeType: changeType,
		KeyXDRB64:  keyB64,
		KeyHint:    storageKeyHint(cd.Key),
		Durability: durability,
	})
}

// storageKeyHint returns a best-effort one-line human summary of
// an SCVal key so an operator skimming output can recognise common
// storage patterns (Symbol("ADMIN"), Vec[Symbol("PAIR"), Address],
// etc.) without round-tripping the base64-encoded XDR through a
// decoder. Returns "" when the key shape doesn't fit a simple
// pattern.
func storageKeyHint(k sdkxdr.ScVal) string {
	switch k.Type {
	case sdkxdr.ScValTypeScvSymbol:
		if k.Sym != nil {
			return fmt.Sprintf("symbol(%q)", string(*k.Sym))
		}
	case sdkxdr.ScValTypeScvVec:
		if k.Vec == nil || *k.Vec == nil {
			return "vec[]"
		}
		v := **k.Vec // ScVec is []ScVal under a double pointer
		if len(v) == 0 {
			return "vec[]"
		}
		// Common case: Vec starts with a Symbol that names the slot.
		if v[0].Type == sdkxdr.ScValTypeScvSymbol && v[0].Sym != nil {
			return fmt.Sprintf("vec[symbol(%q), ...×%d]", string(*v[0].Sym), len(v)-1)
		}
		return fmt.Sprintf("vec[×%d]", len(v))
	case sdkxdr.ScValTypeScvBytes:
		if k.Bytes != nil {
			return fmt.Sprintf("bytes[%d]", len(*k.Bytes))
		}
	case sdkxdr.ScValTypeScvU32:
		if k.U32 != nil {
			return fmt.Sprintf("u32(%d)", *k.U32)
		}
	}
	return ""
}

// scanLCMForCodeUploads walks LedgerEntryChanges in lcm looking
// for ContractCode entry Created/Restored events — i.e. raw WASM
// upload events emitted when someone calls UploadContractWasm.
//
// Captured globally (not per-watched-contract) because the upload
// is independent of which contract may later reference the
// resulting hash. Returns the (possibly extended) slice; caller
// reassigns to keep the per-worker accumulator in sync.
//
// We capture both Created (a fresh upload) and Restored (a TTL-
// extended upload restored from cold storage) for completeness.
// `Updated` is excluded — Soroban doesn't update ContractCode
// bytes (the bytes are immutable; only the entry's TTL changes).
func scanLCMForCodeUploads(
	lcm sdkxdr.LedgerCloseMeta,
	uploads []codeUpload,
	seq uint32,
) []codeUpload {
	if lcm.V != 1 || lcm.V1 == nil {
		return uploads
	}
	v1 := lcm.V1
	for i := range v1.TxProcessing {
		txMeta := &v1.TxProcessing[i].TxApplyProcessing
		switch {
		case txMeta.V3 != nil:
			for j := range txMeta.V3.Operations {
				changes := txMeta.V3.Operations[j].Changes
				for k := range changes {
					uploads = maybeAppendCodeUpload(&changes[k], uploads, seq)
				}
			}
		case txMeta.V4 != nil:
			for j := range txMeta.V4.Operations {
				changes := txMeta.V4.Operations[j].Changes
				for k := range changes {
					uploads = maybeAppendCodeUpload(&changes[k], uploads, seq)
				}
			}
		}
	}
	return uploads
}

// maybeAppendCodeUpload checks one LedgerEntryChange for a
// ContractCode Created/Restored event and appends to uploads if
// it's a match. Skips other change types and other entry types.
func maybeAppendCodeUpload(
	change *sdkxdr.LedgerEntryChange,
	uploads []codeUpload,
	seq uint32,
) []codeUpload {
	var entry *sdkxdr.LedgerEntry
	var changeType string
	switch change.Type {
	case sdkxdr.LedgerEntryChangeTypeLedgerEntryCreated:
		entry, changeType = change.Created, "created"
	case sdkxdr.LedgerEntryChangeTypeLedgerEntryRestored:
		entry, changeType = change.Restored, "restored"
	default:
		return uploads
	}
	if entry == nil || entry.Data.Type != sdkxdr.LedgerEntryTypeContractCode {
		return uploads
	}
	cc := entry.Data.ContractCode
	if cc == nil {
		return uploads
	}
	return append(uploads, codeUpload{
		Ledger:     seq,
		WasmHash:   hex.EncodeToString(cc.Hash[:]),
		SizeBytes:  len(cc.Code),
		ChangeType: changeType,
	})
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
	tlog *transitionLog,
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
		recordWasmTransition(state, contractHash, "stellar-asset", seq, tlog)
		return
	}
	if inst.Executable.WasmHash == nil {
		return
	}
	hashHex := hex.EncodeToString(inst.Executable.WasmHash[:])
	recordWasmTransition(state, contractHash, hashHex, seq, tlog)
}

// recordWasmTransition advances a contract's history when its
// executable hash differs from the previously seen one. First-seen
// opens an initial range; same-hash repeats are no-ops.
//
// When tlog is non-nil, the transition is also appended to the
// per-worker JSONL log (one line per transition) — the crash-
// resilient checkpoint mechanism. Same-hash repeats produce no
// log line either, since they're not transitions.
func recordWasmTransition(
	state map[sdkxdr.Hash]*wasmContractState,
	contract sdkxdr.Hash,
	wasmHash string,
	seq uint32,
	tlog *transitionLog,
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

	if tlog != nil {
		// Best-effort write — don't fail the whole walk on a log error.
		// The in-memory state remains the source of truth for the final
		// stdout JSON; the JSONL is purely for crash recovery.
		if err := tlog.append(contract, wasmHash, seq); err != nil {
			fmt.Fprintf(os.Stderr, "wasm-history: transitionlog append failed (continuing): %v\n", err)
		}
	}
}

// transitionLog is a per-worker append-only JSONL writer for
// crash-resilient walks. One line per transition observed:
//
//	{"contract": "C...", "wasm_hash": "abc...", "at_ledger": 12345}
//
// The writer is buffered (4 KiB default) and flushed every
// transition (transitions are rare relative to ledgers, so the
// flush overhead is negligible). The file is opened with O_APPEND
// so concurrent appends from multiple workers to the SAME file
// would be safe at the OS level — but each worker writes to its
// own file by convention to avoid log-line interleaving.
type transitionLog struct {
	f     *os.File
	enc   *json.Encoder
	watch map[sdkxdr.Hash]string
}

type transitionRecord struct {
	Contract string `json:"contract"`
	WasmHash string `json:"wasm_hash"`
	AtLedger uint32 `json:"at_ledger"`
}

func newTransitionLog(path string, watch map[sdkxdr.Hash]string) (*transitionLog, error) {
	// gosec G304: path comes from operator-controlled -checkpoint-dir
	// flag; the wasm-history subcommand is itself a privileged ops
	// tool that needs to write to operator-chosen paths.
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // intentional ops-tool file write
	if err != nil {
		return nil, err
	}
	enc := json.NewEncoder(f)
	// Default Encoder writes one line per Encode() with a trailing
	// newline — that's exactly the JSONL shape we want. No SetIndent.
	return &transitionLog{f: f, enc: enc, watch: watch}, nil
}

func (t *transitionLog) append(contract sdkxdr.Hash, wasmHash string, seq uint32) error {
	cstrkey, ok := t.watch[contract]
	if !ok {
		cstrkey = hex.EncodeToString(contract[:]) // fallback: shouldn't happen since recordWasmTransition only fires for watched contracts
	}
	return t.enc.Encode(transitionRecord{
		Contract: cstrkey,
		WasmHash: wasmHash,
		AtLedger: seq,
	})
}

func (t *transitionLog) Close() error {
	if t == nil || t.f == nil {
		return nil
	}
	return t.f.Close()
}

// ─── scan-soroban-events ─────────────────────────────────────────

// scanSorobanEvents streams a bounded galexie ledger range and dumps
// every Soroban contract event (optionally filtered to a topic[0]
// string and/or a single contract) as one JSON object per line:
//
//	{ledger, contract_id, tx_hash, op_index, topics:[...], body:{...}}
//
// It is the in-infra analogue of hubble-soroban-events (which needs
// BigQuery): the authoritative "what does contract X actually emit
// on-chain" answer, used to discover real contract addresses + event
// schemas before writing or auditing a decoder. No DB writes — a
// catch-all dispatcher.Decoder reuses the dispatcher's LCM→event
// extraction so we never reimplement SCVal/LCM parsing.
func scanSorobanEvents(args []string) error { //nolint:funlen,gocognit,gocyclo // linear diagnostic; splitting reduces readability
	fs := flag.NewFlagSet("scan-soroban-events", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	from := fs.Uint("from", 0, "First ledger sequence (inclusive, required)")
	to := fs.Uint("to", 0, "Last ledger sequence (inclusive, required)")
	topic0 := fs.String("topic0", "", "Only events whose decoded topic[0] String/Symbol == this (default: any)")
	contract := fs.String("contract", "", "Only events from this contract C-strkey (default: any)")
	limit := fs.Int("limit", 50, "Stop after this many matching events")
	bucketOverride := fs.String("bucket", "", "Override bucket (default: s3_bucket_archive, then s3_bucket_live)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" || *from == 0 || *to == 0 || *to < *from {
		return fmt.Errorf("-config, -from, and -to are required; -to must be >= -from")
	}
	if *limit <= 0 {
		return fmt.Errorf("-limit must be > 0")
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
		return fmt.Errorf("no bucket: set -bucket or storage.s3_bucket_archive / s3_bucket_live")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()

	col := &scanCollector{
		topic0:   *topic0,
		contract: *contract,
		limit:    *limit,
		cancel:   cancel,
		out:      json.NewEncoder(os.Stdout),
	}
	disp := dispatcher.New(col)

	fmt.Fprintf(os.Stderr,
		"scan-soroban-events: bucket=%s ledgers=%d..%d topic0=%q contract=%q limit=%d\n",
		bucket, *from, *to, *topic0, *contract, *limit)

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

	var totalLedgers int
	streamErr := ledgerstream.Stream(ctx, lsCfg, uint32(*from), uint32(*to),
		func(lcm sdkxdr.LedgerCloseMeta) error {
			totalLedgers++
			if _, perr := disp.ProcessLedger(lcm, cfg.Stellar.Passphrase()); perr != nil {
				fmt.Fprintf(os.Stderr, "scan-soroban-events: ledger %d: %v\n",
					lcm.LedgerSequence(), perr)
			}
			return nil
		},
	)
	// Hitting -limit cancels ctx on purpose — that's success, not error.
	if streamErr != nil && !errors.Is(streamErr, context.Canceled) {
		return fmt.Errorf("stream: %w", streamErr)
	}
	if col.firstErr != nil {
		return col.firstErr
	}
	fmt.Fprintf(os.Stderr,
		"scan-soroban-events: scanned %d ledgers, %d matching events emitted\n",
		totalLedgers, col.matched)
	return nil
}

// scanCollector is the catch-all dispatcher.Decoder backing
// scan-soroban-events. It emits no consumer.Event; Decode renders
// the event and Matches applies the optional topic0/contract filter.
type scanCollector struct {
	topic0   string // "" = any
	contract string // "" = any
	limit    int
	matched  int
	cancel   context.CancelFunc
	out      *json.Encoder
	firstErr error
}

func (c *scanCollector) Name() string { return "scan" }

func (c *scanCollector) Matches(ev events.Event) bool {
	if c.matched >= c.limit {
		return false
	}
	if c.contract != "" && ev.ContractID != c.contract {
		return false
	}
	if c.topic0 != "" {
		if len(ev.Topic) == 0 {
			return false
		}
		t0, err := scval.Parse(ev.Topic[0])
		if err != nil || scalarString(t0) != c.topic0 {
			return false
		}
	}
	return true
}

func (c *scanCollector) Decode(ev events.Event) ([]consumer.Event, error) {
	topics := make([]any, 0, len(ev.Topic))
	for _, t := range ev.Topic {
		topics = append(topics, renderB64SCVal(t, 3))
	}
	rec := map[string]any{
		"ledger":      ev.Ledger,
		"contract_id": ev.ContractID,
		"tx_hash":     ev.TxHash,
		"op_index":    ev.OperationIndex,
		"topics":      topics,
		"body":        renderB64SCVal(ev.Value, 3),
	}
	if err := c.out.Encode(rec); err != nil && c.firstErr == nil {
		c.firstErr = err
		if c.cancel != nil {
			c.cancel()
		}
	}
	c.matched++
	if c.matched >= c.limit && c.cancel != nil {
		c.cancel()
	}
	return nil, nil
}

// scalarString returns the text of a String/Symbol ScVal, else "".
func scalarString(sv sdkxdr.ScVal) string {
	if s, err := scval.AsString(sv); err == nil {
		return s
	}
	if s, err := scval.AsSymbol(sv); err == nil {
		return s
	}
	return ""
}

// renderB64SCVal parses a base64 SCVal and renders it to a
// JSON-friendly value, recursing up to depth. Maps render with their
// keys as the JSON object keys (the field names we're hunting), each
// value as a short "type=sample" — enough to reconstruct an event
// schema without dumping unbounded nested data.
func renderB64SCVal(b64 string, depth int) any {
	sv, err := scval.Parse(b64)
	if err != nil {
		return "<unparseable:" + err.Error() + ">"
	}
	return renderSCVal(sv, depth)
}

func renderSCVal(sv sdkxdr.ScVal, depth int) any { //nolint:gocyclo,gocognit,funlen // flat type switch; one arm per SCVal kind
	switch sv.Type {
	case sdkxdr.ScValTypeScvBool:
		return sv.MustB()
	case sdkxdr.ScValTypeScvVoid:
		return nil
	case sdkxdr.ScValTypeScvString:
		return "str:" + string(sv.MustStr())
	case sdkxdr.ScValTypeScvSymbol:
		return "sym:" + string(sv.MustSym())
	case sdkxdr.ScValTypeScvU32:
		return uint32(sv.MustU32())
	case sdkxdr.ScValTypeScvI32:
		return int32(sv.MustI32())
	case sdkxdr.ScValTypeScvU64:
		return uint64(sv.MustU64())
	case sdkxdr.ScValTypeScvI64:
		return int64(sv.MustI64())
	case sdkxdr.ScValTypeScvU128:
		if a, err := scval.AsAmountFromU128(sv); err == nil {
			return "u128:" + a.String()
		}
		return "u128:?"
	case sdkxdr.ScValTypeScvI128:
		if a, err := scval.AsAmountFromI128(sv); err == nil {
			return "i128:" + a.String()
		}
		return "i128:?"
	case sdkxdr.ScValTypeScvAddress:
		if s, err := scval.AsAddressStrkey(sv); err == nil {
			return "addr:" + s
		}
		return "addr:?"
	case sdkxdr.ScValTypeScvBytes:
		b := []byte(sv.MustBytes())
		n := len(b)
		if n > 16 {
			b = b[:16]
		}
		return fmt.Sprintf("bytes[%d]:%x", n, b)
	case sdkxdr.ScValTypeScvVec:
		if depth <= 0 {
			return "vec[..]"
		}
		vec, err := scval.AsVec(sv)
		if err != nil {
			return "vec:?"
		}
		out := make([]any, 0, len(vec))
		for i, e := range vec {
			if i >= 16 {
				out = append(out, "...")
				break
			}
			out = append(out, renderSCVal(e, depth-1))
		}
		return out
	case sdkxdr.ScValTypeScvMap:
		if depth <= 0 {
			return "map{..}"
		}
		entries, err := scval.AsMap(sv)
		if err != nil {
			return "map:?"
		}
		m := make(map[string]any, len(entries))
		for _, e := range entries {
			k := scalarString(e.Key)
			if k == "" {
				k = fmt.Sprintf("<%s>", e.Key.Type.String())
			}
			m[k] = renderSCVal(e.Val, depth-1)
		}
		return m
	default:
		return sv.Type.String()
	}
}
