# A13 — cmd binaries / wiring (READ-ONLY audit)

Scope: every `.go` under `cmd/stellarindex-indexer/`,
`cmd/stellarindex-aggregator/`, `cmd/stellarindex-api/`,
`cmd/stellarindex-ops/`, `cmd/stellarindex-migrate/`,
`cmd/stellarindex-sla-probe/`.

Audit dimensions: D1 correctness (flag parsing, startup/shutdown lifecycle,
graceful drain, signal handling, fd-2 wrap / short-lived-process drain class);
D2 ADR invariants (ingest path, projector one-writer, reader wiring); X4
config→wiring (every field consumed, readers passed to v1.Options); D4
goroutine lifecycle / errgroup / ctx-cancel; D5 resource cleanup / defer-Close
ordering.

**Files read: 19** of 68 in scope (the 6 binary entrypoints + 2 helpers
[`internal/pipeline/datastore.go`, `internal/pipeline/sink.go`,
`internal/projector/registry.go`] read to verify wiring invariants).
Source read in full: indexer `main.go` (1457 L), api `main.go` (3269 L),
aggregator `main.go` (1329 L), migrate `main.go`, sla-probe `main.go`,
ops `main.go` (switch + usage), ops `backfill.go`, `projector.go`,
`ch_rebuild.go`, `ch_backfill.go`, `ledgerstream_config.go`,
`backfill_router.go` (signalContext). Targeted greps across all 68 for
i128-truncation, `os.Exit` in handlers, context/signal setup, test inventory.
Build of `./cmd/...` confirmed green.

---

## Findings

| # | Sev | Dim | File:Line | Finding |
|---|-----|-----|-----------|---------|
| A13-01 | Low | D4/D1 | cmd/stellarindex-ops/backfill_router.go:232 (`signalContext`) | Shared `signalContext()` helper is used by 8 subcommands (ch-backfill, ch-rebuild, ch-gate, ch-reproject, ch-supply, census-backfill, sdex-claim-audit, backfill-router) but prints the literal string `"backfill-router: signal received, flushing checkpoint + exiting..."` on SIGINT/SIGTERM regardless of caller. Cosmetic mislabel — an operator who SIGINTs `ch-rebuild` sees a "backfill-router" message. |
| A13-02 | Low | D4/D5 | cmd/stellarindex-ops/backfill_router.go:232-242 | `signalContext()` leaks its `signal.Notify` goroutine + never calls `signal.Stop(sig)`. The goroutine blocks on `<-sig` forever; deferred `cancel()` doesn't release it. Benign for one-shot CLI (process exits), but it means a second SIGINT cannot be observed by the program and the channel/handler stay registered. Idiomatic form is `signal.NotifyContext`. |
| A13-03 | Low | D1 | cmd/stellarindex-ops/{verify_recognition.go:45, verify_reconciliation.go:55, compute_completeness.go:68} | Long-running verify/completeness subcommands (30/60/120-min) use `context.WithTimeout(context.Background(), …)` with **no** SIGINT/SIGTERM handling — they cannot be cancelled gracefully; only an OS kill stops them. Acceptable (read-only, no partial-write risk) but inconsistent with the streaming subcommands that honour signals via `signalContext()`. Worth a one-line note in each docstring or a switch to `signalContext`. |
| A13-04 | Info | D4/D5 | cmd/stellarindex-api/main.go:629-1062, 1074-1081 | The API spawns ~9 background goroutines (forex, market-cap refresher, TLS probe, coverage refresher, prewarm, stream publisher, stream subscriber, customer-webhook worker, ingestion-snapshot refresher, self-prewarm) but shutdown only calls `httpSrv.Shutdown(shutdownCtx)` and relies on `defer cancel()` (LIFO, runs first) to signal them via `rootCtx`. There is **no** WaitGroup/join — the process exits without confirming these drained. All are best-effort/idempotent (no partial-write hazard documented), so this is by-design, but it means in-flight webhook POSTs / coverage refreshes can be cut mid-flight on shutdown. Contrast with the indexer (waits on sink/projector/sub-goroutine done-channels) and aggregator (`refresherWG.Wait()`). |
| A13-05 | Info | D4 | cmd/stellarindex-aggregator/main.go:551-561 | On shutdown the aggregator shuts the metrics server (10s bounded) then `refresherWG.Wait()` with **no timeout** on the wait. Every refresher goroutine selects on `rootCtx.Done()` so they unwind promptly, but a wedged `r.Tick(ctx)` / `RefreshAll` that ignores ctx (e.g. a blocked DB call without a per-call deadline) could hang shutdown indefinitely. The indexer bounds its analogous drains with `shutdownCtx` (30s); the aggregator does not. Low likelihood (Tick paths take ctx) but unbounded by construction. |
| A13-06 | Info | D5/perf | cmd/stellarindex-ops/ch_rebuild.go:171, 385-401 | `chRebuild` buffers **all** decoded events for `[from,to]` in a single in-memory `[]consumer.Event` (`buf`) before writing. The docstring claims "Windows are partition-aligned (1M) so a window's decoded set stays bounded" but the function itself streams `lo..hi` directly with no internal windowing — the bound depends entirely on the operator passing a partition-sized range. A naive `-from 1 -to 62000000` invocation would OOM. Not a correctness bug; a missing guard rail. |
| A13-07 | Info | D1 | cmd/stellarindex-sla-probe/main.go:223-227 | `-base-url` has a non-empty default (`http://localhost:3000/v1`) yet the help text + the `if *baseURL == ""` guard call it "required". The empty-check is dead (the default is never empty unless the operator explicitly passes `-base-url ""`). Harmless; the doc comment overstates the requirement. |
| A13-08 | Info | X4 | cmd/stellarindex-indexer/main.go:386-393, 483-488 | When `clickhouse_live_sink` (or `clickhouse_projector_source`) is enabled but `clickhouse_addr` is empty, the code silently falls back to the hardcoded `"127.0.0.1:9300"` default. Documented inline as intentional (struct-tag defaults aren't applied at runtime), but it means a misconfigured multi-host deploy that forgot `clickhouse_addr` writes to/reads from localhost rather than failing loud. The API's CH readers (supply/explorer) instead gate purely on `clickhouse_addr != ""` and 503 when absent — the two binaries treat an empty CH addr differently. Minor consistency gap, not a bug. |

No Critical or High findings.

---

## What is CORRECT (verified)

### D1 — correctness, lifecycle, signal handling, fd-2 drain class
- **fd-2 wrap drain-on-exit (rc.77 class) handled correctly** in both the indexer
  and ops: `main()` is a thin `os.Exit(realMain())` shim so the deferred
  `SilenceSDKChecksumWarnings()` flush runs on every return path
  (indexer main.go:81-101, ops main.go:96-113). Verified **no** `os.Exit` calls
  inside ops subcommand handlers (they return `error`/`errExitSilently` instead),
  so no handler bypasses the flush — the exact rc.77 regression is structurally
  prevented. The `errExitSilently` sentinel (main.go:87) lets a handler exit 1
  without a duplicate prefix line while still draining.
- **Flag parsing**: every binary validates `-config` required, prints usage +
  exits 2 on missing; `-version` short-circuits before any work; `-dry-run`
  contract honoured. The indexer/api/aggregator all early-return for `-version`
  and gate `-dry-run` to exit after construction+connection validation but before
  the first `go` statement (api main.go:615-627 explicitly moved the gate above
  all goroutine launches; documented as F-1350).
- **dry-run actually validates**: api + aggregator explicitly `rdb.Ping` under
  dry-run (api main.go:211-218, agg main.go:184-191) because the redis client is
  lazy — "dry-run is a liar without it." Correct.
- **Graceful shutdown, indexer**: the send-on-closed-channel hazard is handled
  with care (main.go:578-634): tracks `streamExited`, waits for the ledgerstream
  producer to return before `close(events)`, and on the drain-deadline path
  leaves the channel **open** (`safeToClose=false`) rather than risk a
  send-on-closed panic — relying on the sink's ctx.Done() arm. This is a
  genuinely subtle correctness win (G20-02). External connectors drained via
  `externalWait()` before close.
- **migrate**: DSN resolution (`-dsn` → `STELLARINDEX_POSTGRES_DSN` → fail, no
  silent default), advisory-lock serialisation noted, `ErrNoChange`/`ErrNilVersion`
  handled per subcommand, `force` gated with a non-negative-int check + DANGER
  doc. Clean.
- **sla-probe**: in-flight-at-deadline samples discarded so the probe doesn't
  self-attribute `concurrency` phantom failures (#54, main.go:341-350);
  idle-conn pool sized to worker count to avoid keep-alive churn races; per-pair
  freshness override for `/price` (structural 30-150s closed-bucket bound)
  vs `/price/tip` (RFP 30s) is correct per ADR-0015.

### D2 — ADR invariants
- **Ingest path (Invariant 6)**: indexer wires `Galexie → ledgerstream →
  dispatcher → decoders` via `pipeline.BuildDispatcher` +
  `ledgerstream.StreamArchiveThenLive`; no stellar-rpc client in the ingest path
  (the only `stellarrpc` import is in ops, for `rpc-probe`/seed diagnostics).
- **Projector one-writer (Invariant 7 / ADR-0031/0032)**: `IsProjectedEvent`
  (sink.go:269) and `buildSource` (projector/registry.go:95) enumerate the SAME
  source set; an ADR-0030 lint guard catches drift; the F-1316 sep41_transfers
  synthetic-contract loss is fixed (registry.go:152+). Indexer correctly switches
  the dispatcher events-goroutine to `SinkModeSkipProjected` only when
  `Projector.Enabled && !PersistPerSource` (main.go:450-454), so the projector is
  sole writer for projected events while sdex/external/band/supply still ride the
  events goroutine. `stellarindex-ops backfill` correctly uses `SinkModeAll`
  (projector doesn't run there) and passes `gated=nil` because projected output
  is dropped by `IsProjectedEvent` anyway (backfill.go:270-275).
- **band + soroswap-router (ContractCall, log-only)** correctly excluded from
  `IsProjectedEvent`; rebuilt via the dedicated `-contract-calls` pass in
  `ch-rebuild` (the lake-replay successor to the retired backfill-router MinIO
  walk), not the projector.
- **i128 never truncates (Invariant 1)**: grep across `cmd/` found **zero**
  `int64(parts.Lo)` / `.Hi)` truncations. (The one `.Lo`-adjacent hit is an
  unrelated `uint64` chunk-blocks flag.)
- **projector-replay**: uses `RewindCursor` (not `UpsertCursor`) — the latter's
  monotonic-forward guard (F-0020) silently no-ops a backward write, which had
  made the whole subcommand a no-op-that-printed-success (fixed 2026-06-12,
  projector.go:97-103). Correct.

### X4 — config → wiring
- **API reader wiring complete**: every reader in `v1.Options` is constructed and
  passed (Assets, Prices, History, Markets, Oracle, Supply, TokenSupply,
  **Explorer**, Volume, Change24h, ChangeSummary, Coins, Issuers, SEP41Transfers,
  Cursors, Coverage/Completeness, Protocol*, NetworkStats, SourcesStats, Lending,
  Currencies, FXHistory, GlobalPrice, …). The **new ExplorerReader** (ADR-0038)
  and TokenSupplyReader are both gated on `cfg.Storage.ClickHouseAddr != ""`,
  constructed with non-fatal dial (log-warn + leave nil → handler 503s), and
  `defer Close`d (api main.go:808-833). Matches the supply-reader posture.
- **buildExternal / startExternalConnectors**: every `cfg.<Venue>.Enabled` gate
  maps to exactly one streamer/poller spec; FX pollers, aggregator pollers
  (catalogue-derived pair set with hardcoded fallback), chainlink (empty-feedmap
  skip), CG/CMC auth-key env plumbing all present. Returns a no-op wait when no
  connectors enabled (keeps shutdown unconditional).
- **buildSource (aggregator supply refreshers)**: one refresher per watched
  classic asset + per watched SEP-41 contract + the XLM refresher, each with the
  correct per-asset `RefresherOption` (strict-freshness + per-asset stale
  threshold). Cross-check refresher correctly no-ops on empty watched-set ∩.
- **USD-volume quote spec + FX resolver** wired only when
  `trades.usd_pegged_classic_assets` non-empty; empty → nil spec → off-chain-only
  behaviour preserved (indexer main.go:187-220).
- **Supply pipeline OFF warning**: indexer loudly warns at boot when every
  `[supply]` watched-set is empty (the silent-empty-table bug class), main.go:279.

### D4 — goroutine lifecycle / errgroup / ctx-cancel
- **F-1350 defer ordering** verified in all three long-running binaries: `cancel`
  is registered **after** the store/redis Close defers so LIFO cancels ctx FIRST
  on shutdown — workers unwind before the resources they query are closed.
  Explicitly commented at indexer main.go:157-162, api main.go:221-230, agg
  main.go:176-182.
- **Indexer** joins every spawned goroutine via a `(stop, done)` pair or a
  done-channel: postgres-ping, decoder-stats flush, discovery-drop watcher,
  soroban-events sink (+ ctx-cancel safety-net unblocker), CH live-sink watcher,
  sink goroutine, projector goroutine — each with a deferred stop+`<-done`.
  Projector drain bounded by `shutdownCtx` (main.go:645-652).
- **ch-backfill** uses `errgroup.WithContext` correctly: one `g.Go` per chunk
  with proper loop-var capture, `g.Wait()`, fatal CH write cancels siblings, each
  chunk `defer sink.Close` + explicit `sink.Flush` on clean completion
  (ch_backfill.go:87-147).
- **Parallel backfill** (backfill.go:206-235): per-chunk goroutine, buffered
  `errCh` sized to chunk count, `wg.Wait()` then `close(errCh)` then drain +
  `errors.Join` — no leak, no deadlock. Each chunk owns its dispatcher, events
  channel, PersistEvents goroutine, and chunk-specific cursor row (no
  cross-chunk cursor collision).
- **soroban-events AsyncSink** has the documented ctx-cancel unblocker goroutine
  in both the indexer (main.go:359-362) and backfill (backfill.go:361-369) so a
  blocked back-pressured PushEvent can't pin the dispatcher past SIGTERM.

### D5 — resource cleanup / defer ordering
- Every binary `defer store.Close()` / `defer rdb.Close()` with the correct
  LIFO-vs-cancel ordering (above). The early-failure paths (store open fails
  before cancel registered) call `cancel()` explicitly to release the signal ctx
  (indexer main.go:149, api main.go:181, agg main.go:154/172).
- ch-rebuild / ch-backfill / projector-replay all `defer store.Close()`; CH sinks
  `defer Close`d; metrics servers `Shutdown`-bounded.

---

## Recommendations (non-blocking)
1. Rename `signalContext`'s log line to be subcommand-agnostic (or pass the
   subcommand name in) and switch it to `signal.NotifyContext` to drop the goroutine
   leak (A13-01, A13-02).
2. Either add a bounded wait for the API's background goroutines or document
   explicitly that they are fire-and-forget on shutdown (A13-04).
3. Bound `refresherWG.Wait()` with a shutdown deadline in the aggregator, mirroring
   the indexer (A13-05).
4. Add an internal window-loop (or a sanity cap / required `-window`) to `ch-rebuild`
   so a too-wide range can't OOM (A13-06).
