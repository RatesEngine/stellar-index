---
title: Audit 1 — Cold system findings register (CS-###)
status: in progress (pre-execution recon findings banked; systematic execution pending plan ratification)
---

# Cold system findings — register

Severity per [README](README.md). Each finding names a concrete failing
scenario or it isn't Critical/High. The **Cleared** section records flags that
were investigated and found to be non-issues — recording these prevents
re-litigating them and documents the adversarial-verification discipline (the
surface mapper's concrete flags had a ~50% false-positive rate).

## Pre-execution recon findings

### CS-001 — Live GCP service-account key in the working tree — **Medium**
- **Location:** `./rates-engine-data-validation-0603331b2417.json` (repo root).
- **Verified:** Real SA key (`type: service_account`, contains `private_key`,
  `client_email` @ `rates-engine-data-validation.iam.gserviceaccount.com`). It is
  **gitignored and NOT git-tracked** — so it is *not* in repo history and gitleaks
  (history scan) correctly never saw it. (Private-key material was NOT inspected
  or printed.)
- **Why it matters:** a live credential sitting inside the repo working tree is a
  footgun — one `.gitignore` edit, `git add -f`, stray `tar`/backup, or editor
  plugin away from exposure. The project name (`rates-engine-…`) is the *old*
  brand, suggesting a possibly-stale data-validation SA.
- **Failing scenario:** if `.gitignore` is reorganized (common during refactors)
  the key gets committed; or a "zip the repo to share" leaks it.
- **Fix:** move the key OUT of the repo directory (e.g. `~/.config/…`); confirm
  whether the SA is still used at all; **rotate the key** if there's any chance it
  was ever shared/synced; add a CI check that no `*.json` with a `private_key`
  field exists in the tree.

### CS-003 — `internal/platform/postgresstore` near-zero test coverage — **Medium (confirm in execution)**
- **Location:** `internal/platform` (+`postgresstore`): mapper reports ~2489 src
  LOC vs ~54 test LOC — the customer-account / API-key / webhook persistence store.
- **Why it matters:** this is the store behind paid accounts, key issuance, and
  webhook config — exactly where IDOR / wrong-tenant / nil-time bugs (cf. the
  rek_/sip_ prefix + 2055-last-used + magic-link nil-Now bugs already seen) live.
- **Next:** A18 execution — confirm the ratio, then read the store for tenant-
  scoping on every query + the zero-time guard class.

### CS-005 — CLAUDE.md repo-map under-counts `internal/` ~3× — **Low (doc drift)**
- **Location:** `CLAUDE.md` "Repo map" (~30 packages) vs actual ~95
  packages/subpackages (`internal/api/*`, `aggregate/*`, `sources/*`, `storage/*`
  subpkgs, `xdrjson`, `platform/postgresstore`, `dispatcher/statsflush` undocumented).
- **Why it matters:** CLAUDE.md is the AI-agent entry point + freshness-checked in
  CI, yet materially incomplete — agents (and auditors) under-scope.
- **Fix:** regenerate the map or note it's a curated subset; ideally a CI check
  that every `internal/*` package appears.

### CS-007 — ADR-0003 i128 enforcement TOOLING is claimed but absent — **Low**
- **Location:** `docs/adr/0003-i128-no-truncation.md:128` claims "Lint rule in
  `.golangci.yml` (via a small custom analyzer) flags [int64 truncation]"; the
  migration-status BIGINT refusal at 0003:116 is likewise claimed. **Verified:**
  no such analyzer in `.golangci.yml`, no `tools/`/analyzer package, no migration
  type-lint in `scripts/ci/`. Reconfirms prior `D2-10` (audit-2026-06-11).
- **Mitigation that holds:** the *runtime* discipline is real — a tree-wide scan
  for `int64(…Lo/…Hi/Int128Parts)` truncation sites found **zero**, and
  `ErrI128Overflow` guards the parse path. So this is a missing *guard-rail*, not
  an active bug: a future truncation would not be caught at CI time.
- **Fix:** build the analyzer + migration lint (launch-todo P4-6), or downgrade
  the ADR's claim to match reality. (Doc-vs-code drift, CC-7.)

### CS-006 — `internal/hashdb` shipped with zero production callers — **Info**
- **Location:** `internal/hashdb` (226 src LOC). CLAUDE.md itself admits "LIBRARY
  ONLY — currently has zero production callers; the ADR-0033 'feeder' role is
  aspirational." So this is *acknowledged*, but it's dead weight that implies a
  completeness guarantee (drift-detection-vs-upstream-rewrites) the system does
  not actually have. **Fix:** wire it or delete it; don't let an unwired library
  imply a guarantee.

## Cleared (investigated → not a finding)

- **[CLEARED] Migrations 0031/0040 down re-introduce forbidden retention.** The
  surface mapper flagged the `.down.sql` files as re-adding `add_retention_policy`
  on `trades`/`oracle_updates`. **False:** both downs are explicit NO-OPs with a
  banner comment ("intentionally a NO-OP. DO NOT re-add retention… the precise
  loss ADR-0034 [forbids]"). This is *good* defensive engineering, not a bug.
- **[CLEARED] `r1.secrets.yml` plaintext secrets in inventory.** **False:** the
  file is `$ANSIBLE_VAULT;1.1;AES256`-encrypted AND not git-tracked.
- **[CLEARED→re-scoped] GCP key "checked into repo root."** Re-scoped to CS-001:
  it's gitignored/untracked (working-tree footgun, Medium), not a committed leak.
- **[VERIFIED GOOD] A3 one-writer invariant (ADR-0031/0032).** The projector
  registry's `buildSource` source set and `pipeline/sink.go::IsProjectedEvent`'s
  event-type arms are **in sync** — every projected source (soroswap, aquarius,
  phoenix, comet, reflector, redstone, blend, blend_backstop, cctp, rozo,
  defindex, sep41_supply, sep41_transfers) appears in both (reflector via the
  oracle-config path, sep41_* via the `watchedSEP41` path). No double-write /
  silent-drop drift. (Recorded to avoid re-litigation; a deeper A3 pass can still
  fuzz the event-type↔table mapping.)

## Systematic findings (from area execution)

### A18 — Multi-tenant IDOR — **surface is clean** (1 Low)
Full end-to-end trace of every dashboard/account/admin handler → store query.
**No Critical/High cross-tenant read/write survived** (8 candidates C1-C8
adversarially refuted: key revoke, webhook PATCH/DELETE/deliveries, Redis key
path, empty-subject fail-OPEN→actually fail-closed, staff-lookup spoof, mass-
assignment, Stripe/billing, usage/audit/invites). Confirms prior audits' "auth
primitives sound."

- **CS-008 — Tenant isolation enforced in the handler layer, not the SQL — Low
  (defense-in-depth).** `postgresstore` by-id methods act by PK alone
  (`apikey_store.go:291` `Get`; `webhook_store.go` `GetWebhook`/`UpdateWebhook`/
  `DeleteWebhook`/`ListDeliveries` — all `WHERE id = $1`, no `AND account_id`).
  Isolation depends on every handler remembering to compare `AccountID` before
  acting (they all do today). **Failing scenario:** the *next* handler added on
  top of these store methods that forgets the compare = instant Critical IDOR, and
  the store's thin tests (only `apikey_store_test.go`; webhook/account/audit/token/
  billing stores untested — ties CS-003) won't catch it. **Fix:** push the owner
  predicate into the query (`WHERE id=$1 AND account_id=$2` → `ErrNotFound`), and
  add store-level "A's id + B's owner → ErrNotFound" tests. Re-run this review when
  team/invite, audit-trail, and `/v1/account/subscription` HTTP surfaces ship (the
  account-scoped store methods exist but aren't yet wired to handlers).

### A26/A30 — CF Pages Edge Functions (never previously audited)
- **CS-009 — OG image edge function: unauthenticated blind SSRF + no-timeout DoS
  via double-decoded, unescaped path segment — High** (rubric: SSRF-to-arbitrary-
  host = High; held at the boundary because it's blind + edge-sandboxed — see
  note). `web/explorer/functions/og/[[path]].js` reads the URL path, **decodes it
  up to twice** (defeating the upstream `ogImageFor` `encodeURIComponent`), passes
  it through `prettyLabel`/`code` (no HTML-escape), and string-interpolates it
  **unescaped** into HTML handed to `new ImageResponse(html)` (`workers-og`/satori).
  satori's `<img>` handler does `fetch(src)` for any `http…` src with **no allow-
  list, no SSRF guard, no timeout**. **Exploit:** `GET /og/ledgers/%3Cimg%20src=
  http://attacker%3E` → the CF edge fetches the attacker URL on render; the 24-char
  label cap is bypassed via a short redirector domain (satori follows redirects).
  Unauthenticated, attacker-chosen outbound GET from the edge + a no-timeout fetch
  = cost/resource DoS (unique URLs defeat the 60s cache). *Refuted down from "stored
  XSS":* output is `image/png` (rasterized, never browser-interpreted) — the
  same-origin OG-card content-spoof is Low. *Note:* reviewer rated **Medium** as
  blind + no internal target demonstrated; I keep it **High** for the audit because
  an unauthenticated SSRF primitive on the public edge is a real, fixable exposure
  and the no-timeout fetch is a cheap DoS. **Fix:** HTML-escape `label`/`sub`; stop
  the double-decode; ideally allow-list satori's image fetch host. (CC-6 trust
  boundary; the never-audited surface the gap analysis ranked #1.)

### A6/A8/A34 — Data-correctness at scale (the "code-correct ≠ data-correct" seam)
- **CS-010 — XLM circulating supply served == total == max (~50.0B); `xlm_sdf_
  reserve_exclusion` basis is a no-op → market cap overstated ~+58% — High.**
  Live `/v1/assets/native`: `circulating_supply = total_supply = max_supply =
  500018068120000000` stroops (= **50.0B XLM**), `supply_basis:
  xlm_sdf_reserve_exclusion`, `market_cap_usd = $9.20B`. **Airtight internal
  contradiction:** a basis that excludes SDF reserves MUST make circulating <
  total; circulating == total proves the exclusion nets to **zero** (the SDF-held
  ~18-19B is not subtracted). Every public source (CoinGecko/Stellar Expert) puts
  XLM circulating ≈31B → served circulating + market cap are overstated **~+58%**
  on the **flagship asset**. **Failing scenario:** any consumer reading XLM market
  cap / circulating supply from the API (or the explorer headline) gets a number
  ~1.58× reality. Confirms + worsens prior `06-14 Q4` (+48% one-sample). **Fix:**
  make the SDF-reserve exclusion actually subtract SDF-held balances, or stop
  labeling the basis as an exclusion; reconcile against Stellar Expert in CI.
  → **systematic reconciliation needed** (this was one sample; sweep per-asset
  supply, 24h volume, OHLC vs ground truth — Wave 2).
- **CS-011 — `/v1/assets/xlm` (slug/GlobalAssetView shape) omits supply + market
  cap entirely** (only `ticker`+`price_usd`), while `/v1/assets/native`
  (AssetDetail shape) carries them — the dual-shape split (LC-040) means the
  headline slug a CG-style user hits (`/assets/xlm`) silently lacks market cap. Low
  (coherence/completeness; cross-refs LC-040). 

### A21 — SSE / streaming (prior audits: "hub has no functional/chaos test" — confirmed exploitable)
- **CS-012 — send-on-closed-channel panic race in `Hub.Publish` → whole-process
  crash — High.** `streaming/hub.go:92` releases `t.mu` then sends off-lock
  (`hub.go:96`); `dropSubscriber` closes `sub.ch` under lock (`hub.go:171`). A
  `select` send on a *closed* channel is "ready" (chosen over `default`) → panics
  `send on closed channel`. The panic is in the publisher goroutine
  (`streampublish.pollLoop`/`redispub.Subscriber.Run`) which has **no `recover()`**
  (`middleware.Recoverer` only wraps HTTP) → **entire API process crashes.**
  **Scenario:** `/v1/price/stream` client disconnects (handler `defer cancel()` →
  `dropSubscriber` → `close`) in the µs window between Publish's snapshot-unlock
  and its send → aggregator publishes a closed bucket → panic. `closeOnce` guards
  double-*close*, not concurrent *send*. Untested (the two Hub tests avoid the
  publish-vs-disconnect race). **Fix:** make the send panic-safe (guard a per-sub
  "closed" flag read under `t.mu`, or recover in the publisher goroutines).
- **CS-013 — cleared write deadline + teardown only on `ctx.Done()` → stalled
  client leaks goroutine+conn+FD forever; no connection cap → DoS — High.**
  `handler.go:88` does `SetWriteDeadline(time.Time{})` (clears it, no rolling
  replacement); the loop exits only on `ctx.Done()`, which fires on client *close*,
  not *stall*. A non-reading / zero-window client blocks `Flush()` indefinitely; ctx
  never cancels. **No max-conn cap, no idle-stream timeout.** With anon stream
  establishment allowed (`anon_rate_limit_per_min=6000`), thousands of non-reading
  connections → FD/goroutine/memory exhaustion (health checks refused). `IdleTimeout`
  /`ReadTimeout` don't cover an in-flight SSE handler. **Fix:** rolling per-write
  deadline + per-subject/global concurrent-stream cap (`netutil.LimitListener`).
- **CS-014 — client-controlled sub-second tick × unbounded connections = DB-load
  amplification — Medium.** Per-connection producers re-run a backend query each
  tick; tick is client-set to `[1,60]s`. `observations` drives a `LatestTradePerSource`
  hypertable scan (8s-bounded per tick, but unbounded in *count*). N streams at
  `interval_seconds=1` → N scans/s on Postgres. **Fix:** floor the tick + cap streams.
- **CS-015 — rate limit gates establishment *rate*, not *concurrency* — Medium.**
  Streams hit the same per-minute bucket (good) but nothing caps how many you hold
  open; `main.go:1058` `ListenAndServe` has no `LimitListener`. Structural enabler
  for CS-013/014.
- **CS-016 — `redispub` subscriber has no restart; silent degrade to heartbeats-
  only — Low/Med.** On `Run` error (`subscriber.go:88`) `main.go:1028` only logs +
  never resubscribes; `/v1/price/stream` then serves heartbeats with no
  `price_update`s, still 200 (invisible). Mostly fires on shutdown (go-redis absorbs
  transient blips), so Low/Med. **Fix:** supervise/restart + health-surface.
- *Scope correction:* `/v1/oracle/streams` is NOT SSE (plain 8s-bounded JSON) — only
  4 real SSE endpoints. Several leak/poison candidates were cleared (normal-disconnect
  cleanup is correct; pub/sub routing is per-topic, no cross-subscriber leakage).

### Data-correctness blast-radius (CS-010 follow-up)
- USDC: `circulating == total == 39.0M` with `issuer_exclusion` basis — for USDC
  circulating==total is **plausibly legitimate** (issuer holds ~0), so not flagged.
  But the `circulating == total` pattern recurring across assets means **every
  `*_exclusion` basis needs the Wave-2 reconciliation** to confirm it actually
  subtracts (XLM's provably doesn't — CS-010). AQUA returned no supply data
  (separate completeness gap to check).

### A33 — Pricing read-paths (prior perf-incident fix re-verified intact)
- **CS-017 — dormant pairs return months-old VWAP with `stale=false` — Medium.**
  `storePriceReader.LatestPrice` (`cmd/stellarindex-api/main.go:2252`) hardcodes
  `stale=false` on any `prices_1m` hit; `LatestClosedVWAP1mForPair` resolves the
  latest closed bucket within a **400-day** window. **Scenario:** `/v1/price` for a
  direct-quoted pair whose last trade was 200 days ago → returns the 200-day-old
  bucket with `flags.stale=false` and a 200-day-old `observed_at`. Same "degraded/old
  served as fresh" pattern as the prior Redis-BGSAVE SEV; the PriceReader "stale when
  older than freshness target" contract isn't enforced on the VWAP branch (only the
  last-trade branch sets stale). Bites the ~250k dormant/delisted long-tail (not the
  XLM/USD common case, which uses priceFallback). **Fix:** flip `stale=true` when
  `now − bucket_end` exceeds the freshness target. (CC-1.)
- **CS-018 — self-peg hides a depeg on the SEP-40 wire shape — Low (by-design).**
  `/v1/oracle/lastprice|x_last_price` reach `tryStablecoinFiatProxy` via
  `priceFallback` and emit `SEP40Price` with no divergence field, so a SEP-40
  consumer (lending protocol) reads `1.0 @ now` during a USDC depeg with only
  `stale` as signal. **NOTE — the P2-4b self-peg arm itself was reviewed CORRECT:**
  it's only reached after the primary read returns ErrPriceNotFound, never overrides
  a real bucket (no exchange emits crypto:USDC/fiat:USD), and crypto:USDC/fiat:EUR
  correctly doesn't trip it. **Fix:** add a peg/divergence flag to the SEP-40 shape.
- **CS-019 — two non-sargable plan-time predicates — Low.** `RecentClosedVWAP1mForPair`
  (`aggregates.go:241`, backs /v1/oracle/prices) and `ClosedVWAP1mAtOrBefore`
  (`aggregates.go:289`, backs /v1/assets change_24h) use `bucket + INTERVAL <= …`.
  `ORDER BY bucket DESC LIMIT` rescues *execution*, but the first lacks a literal
  lower-bound → plan-time chunk enumeration (the layer-2 cost the prior fix
  neutralised elsewhere); the second back-scans ~1440 buckets. Low (not full-scan).
  **Fix:** give them the same literal-cutoff bound / sargable rewrite.
- **CS-020 — batch identity-id aborts the whole batch — Low.** One `asset==quote`
  id in `/v1/price/batch` 400s the entire request (should skip that id); per-row
  stale collapsed to an envelope OR (over-marks safe, but no per-row signal).
- *Re-verified good:* the exact prior incident bug (`LatestClosedVWAP1mForPair`
  `max()` over non-sargable `bucket+INTERVAL<=now()`) is **fixed + sargable**
  (`aggregates.go:393`); stale propagation through the fallback chain is fixed;
  price strings are big.Int/big.Rat end-to-end (only FX cross-rate is float — fine).

### A11 — ClickHouse raw lake (prior audit: "design correct, ZERO integration coverage" — confirmed)
- **CS-021 — `ledger_entries_current` versioned only by `ledger_seq` → intra-ledger
  ordering lost → stale/resurrected current state — Medium.** `tier1_schema.sql:205`
  `ReplacingMergeTree(ledger_seq)` ORDER BY `(entry_type,key_xdr)`. An entry mutated
  ≥2× in its newest ledger shares sort key AND version → FINAL keeps an
  implementation-defined row. **Scenario:** offer partially filled (`updated`) tx5
  then fully consumed (`removed`) tx9 of ledger N → FINAL may keep `updated` →
  `accountOffers`/`AssetHolders` **resurrect a deleted offer**; account balance =
  intermediate not closing. No stored total intra-ledger order (`change_index`
  resets per-tx). Manifests under cross-part interleaving (live vs backfill/heal),
  where CH's equal-version tiebreak is unspecified → untested + unprovable-safe.
  **Fix:** version by `(ledger_seq, tx_index, change_index)` or a global ordinal.
- **CS-022 — CH write buffer cap is in ledger-units, tuned for empty `Changes`;
  Phase-C entry-capture inflated the real byte ceiling — Medium.** `live_sink.go`
  caps `MaxBufferLedgers=4096`; `sink.go:437` comment still claims `Extract.Changes`
  "always empty (G12-03)," but `extract.go:108` now extracts entry-changes — the
  **highest-volume table** (~1.7B rows, base64 `entry_xdr`/`key_xdr` blobs). During
  a CH outage the buffer holds 4096 ledgers × all entry-changes = a far larger heap
  than the G12-01 cap (protecting the shared r1/Postgres host) was tuned for.
  Bounded (F-1349 genuinely fixed) but mis-stated/un-retuned/untested. **Fix:**
  byte-based cap or re-tune; fix the stale comment.
- **CS-023 — non-FINAL aggregate reads double-count un-merged parts — Low/Med.**
  `explorer_reader.go:227,266` (`OperationTypeStats`, `NetworkThroughput`) sum
  `tx_count`/`op_count` without FINAL → healed/backfilled ledgers double-count until
  merges settle (throughput/op-type charts inflate). Counting-critical paths (gate,
  completeness, supply) correctly use FINAL — these two charts are the inconsistency.
- **CS-024 — UInt32 underflow in window predicate + unescaped IN-list concat — Low.**
  `explorer_reader.go:233,277` `ledger_seq > (max)-?` wraps to ~4.29B on a small/fresh
  lake → silently empty (never on the 62M prod lake). `event_reader.go:41`
  `sqlQuoteList` concatenates IN-lists unescaped — docstring claims compile-time
  constants but `contractIDs` come from runtime config (`projector.go:330`); not
  exploitable today (strkeys can't contain quotes) but an injection footgun.
- **CS-025 — the load-bearing lake hole-heal script `ch-live-catchup.sh` is NOT in
  the repo — Medium (verifiability/supply-chain).** The `LiveSink` is best-effort
  (drops on outage) and explicitly relies on `ch-live-catchup.sh` gap-scanning
  below CH_max to refill holes (`live_sink.go:49`); the projector watermark + the
  "100% coverage" claim depend on it. Only `deploy/systemd/ch-live-catchup.service`
  exists — it calls `/usr/local/bin/ch-live-catchup.sh`, which is **not in the
  repo** (and `find-data-gaps` scans Postgres, not the CH lake). So the correctness
  of the backstop the whole tiered-completeness story rests on is **unverifiable
  from the codebase**. **Fix:** vendor the script into the repo + under test.
- *Re-verified GOOD:* core-table dedup sound (deterministic ORDER BY from LCM,
  `event_index` preserved across re-runs; counting reads use FINAL); **contiguity +
  hash-chain is actually CHECKED** (`completeness.go::SubstrateProblem` gap-scan +
  `prev_hash` chain to genesis), not assumed; F-1349 unbounded buffer genuinely
  fixed (bounded channel + commit-marker-last durability ordering);
  `explorer_reader` keyset pagination + `>2^53`-as-string + no-i128-truncation all
  correct; injection clean except CS-024's latent footgun.

### A12 — Migrations & retention — **verified GOOD (clean)**
Executed directly. All ADR-0034-forbidden retention policies are removed by head:
migration **0031** removes `trades` + `prices_1m` + `prices_15m`; **0040** removes
`oracle_updates`. **Live r1 confirms** the only active `policy_retention` job is the
*intentional* `api_usage_events` (1-year drop_after, ephemeral telemetry per 0027).
0001/0002/0003 add the retention in their UP but it's netted out by head — no drift
on a fresh migrate or on prod. PK-discriminator lint (`scripts/ci/lint-pk-discriminators`)
is active + guards the protocol-row hypertables on `event_index` (the coarse-PK fix).
No finding. (The earlier "0031/0040 down re-adds retention" mapper flag remains false.)

### A2 — Dispatcher & decoder routing
- **CS-026 — comet/aquarius/phoenix (+defindex) route on topic bytes alone →
  look-alike contract injects fabricated trades into the VWAP substrate — High
  (known/tracked ADR-0035 gap).** 9 of 13 decoders gate on contract identity
  (ADR-0035 ✓); comet/aquarius/phoenix/defindex still gate on `topic[0]` only
  (phoenix's `"swap"` String namespace is the widest collision surface). **Exploit:**
  an attacker deploys a contract emitting a Phoenix-shaped 8-event swap on a priced
  pair (XLM/USDC) at an off-market rate → `phoenix.Matches` fires → a
  `Trade{Source:"phoenix"}` enters the trades hypertable → the pricing substrate.
  Decode-shape strictness can't block it (attacker controls the body); residual
  bound is only aggregator class-policy + outlier layer. **Already tracked**
  (CLAUDE.md flags Comet; phoenix/defindex/aquarius gates pending) — but real.
  **Fix:** finish the factory/WASM-hash gates for these 4.
- **CS-027 — 3 ops-CLI paths bypass the panic-recover wrapper — Low.**
  `verifyDecoders`, `scanSorobanEvents`, `backfillRouter` call `disp.ProcessLedger`
  directly (no `recover()`); a decoder panic crashes the ops process. Production is
  protected (`pipeline/processor.go:41` recovers per-ledger). backfillRouter's
  every-prior-WASM surface is larger than live, so worth the wrapper. Low (tooling,
  crash-not-corruption, no concrete panic input since scval is panic-safe).
- *Cleared:* Band/Redstone OpArgs (arity/length/u64→int64-overflow all guarded);
  `scval` panic-safe on adversarial base64 (every `As*` type-checks; SEP-41
  i128-or-map tested; zero raw SDK `Must*` in decoders); `Dispatcher.Stats()`
  concurrency fixed (F-1317, mutex + `-race` test); production decoder-panic
  recover intact.

### A1 — Ledger ingest & streaming (the 2026-06-01 cursor incident re-examined)
- **CS-028 — cursor advances on ENQUEUE, not durable write → hard-crash silent
  loss for the census-uncovered sources — High.** `processAndPersistCursor`
  (`indexer/main.go:1340`) upserts the cursor as soon as events are pushed to the
  256-deep channel; the async 8-worker sink drains later. On **hard crash**
  (OOM/panic/power — NOT graceful SIGTERM, which flushes within 90s) up to ~1.8k
  buffered events for ledgers ≤N are lost and never re-processed (restart resumes at
  N+1). **Blast radius (the sharp part):** projected Soroban re-derives from the lake
  (safe); SDEX is caught by the substrate census (recoverable); but **supply
  observers + band + soroswap_router are NOT in the census → silent served-tier
  loss** with no detection signal. **Fix:** gate the cursor advance on sink
  durability, or add these sources to the reconcile census.
- **CS-029 — cursor GAUGE set unconditionally after a failed cursor upsert → masks
  a stalled DB cursor — Medium.** `indexer/main.go:1354`: `UpsertCursor` error is
  logged-not-returned, then `recordCursorMetric(lcm.LedgerSequence())` runs
  regardless. If the upsert stalls (PG lock/blip) while the sink still writes, the
  Prometheus gauge + `/v1/ledger/tip` show FRESH while the durable cursor is STALE →
  on restart, resume far behind = **exactly the 2026-06-01 incident, with monitoring
  hiding it.** **Fix:** set the gauge only on a successful row-affecting upsert.
- **CS-030 — archive→live seam + `TolerateTrailingMissing` can silently skip ≤100
  ledgers — Medium (edge, non-default).** Only when `live_seam_ledger>0` (off by
  default): a missing partition near `seam-1` + the SDK's buffer-drop + the tolerate
  flag converts a real archive gap into a clean "walk complete" → `[last_delivered+1,
  seam-1]` never ingested, no error. **Fix:** don't apply the tolerate flag to the
  seam's bounded archive leg.
- **CS-031 — missing cursor row falls back to `backfill_from_ledger` (stale-config
  foot-gun) — Low.** Mitigated by the safe default (0 + no cursor → hard error).
- *Cleared:* the cursor WRITE-regression (the 2026-06-01 vector) is genuinely closed
  — monotonic `WHERE EXCLUDED.last_ledger > …` guard + single writer + ascending
  delivery; read errors don't advance the cursor; back-pressure blocks (not drops);
  graceful shutdown loses nothing.

### A5 — Lending/bridge/oracle decoders — numeric core CLEAN
- **CS-032 — DefIndex recognized-but-undecoded events counted as decode ERRORS,
  not clean drops — Medium (observability).** `defindex/dispatcher_adapter.go:51`
  routes `harvest` + 9 vault-governance events through decoders whose `default` arm
  returns `ErrUnknownEvent`; the dispatcher (`dispatcher.go:922`) bumps
  `SourceDecodeErrorsTotal{defindex}` on ANY non-nil decode error (no
  `ErrUnknownEvent` special-case). So routine keeper `harvest`s + governance events
  permanently pollute the decode-error metric → masks/normalizes real schema-drift
  errors + can make a decode-error-rate alert permanently noisy. The code comment
  (`events.go:256`) claiming a clean drop-counter path is **false**. (blend/cctp/rozo
  keep classify↔decode in lockstep; defindex is the only one where recognition
  outruns decoding.) **Fix:** clean recognize-and-drop for these, like factory events.
- **CS-033 — `blend_backstop.BackstopGenesisLedger`=56.6M contradicts factory
  genesis 51.49M → gap detector under-sizes the coverage window — Low.** ~3 weeks of
  ledgers unexpected-by-the-gap-detector; live-capture-only so affects window sizing
  not decoded values.
- **CS-034 — stale doc: `blend/events.go:96` claims backstop events "NOT yet
  decoded"** but `blend_backstop` decodes all 10. Doc-drift (folds into A32).
- **CS-035 — Reflector price scale hardcoded to 14 for all 3 variants — Low
  (latent).** `NewDecoder` never passes `WithDecoderDecimals`; the SEP-40
  `decimals()` is never read on-chain. Documented as canonical, but a per-contract
  scale divergence on any one variant would silently mis-scale that feed by orders
  of magnitude. **Fix:** read decimals() per contract or add a per-variant assert.
- *Cleared (strong):* all 4 named wire-quirks correct (Reflector 3-contracts +
  local TWAP, Band E18/E9, Redstone strict zip, **Blend now 23/23 events** vs prior
  3/21); i128/u128/u256/u64 never truncated (→ `big.Int` → NUMERIC); Map-by-NAME
  everywhere; uniform u64→int64 timestamp-overflow guard (the router `deadline_ts`
  class doesn't recur here); CCTP/Rozo/DefIndex value fidelity sound.

### A6 — Supply pipeline (CS-010 ROOT CAUSE found + more)
- **CS-010 ROOT CAUSE (High, arguably Critical — headline):** the XLM SDF-exclusion
  no-op is **two bugs**: (a) **config gap** — `stellarindex_sdf_reserve_accounts` is
  never set in the r1 inventory, so `stellarindex.toml.j2:178` renders `[]` →
  `supply/xlm.go:149 if len(reserveAccounts)>0` is false → `circulating = total − 0`;
  (b) **dishonest basis label** — `xlm.go:186` stamps `Basis:
  BasisXLMSDFReserveExclusion` **unconditionally**, even with an empty reserve set,
  so the wire claims an exclusion that structurally didn't happen. **Fix:** set the
  reserve-account list + balance source in inventory (config `Validate()` already
  enforces balance-per-account), AND make `xlm.go` emit an honest basis
  (`xlm_total_only`) when the list is empty so the misconfig is self-evident.
- **CS-036 — SEP-41 mint amount capture coupled to counterparty decode — Medium.**
  The CAP-67 counterparty fix (topic-type branch) is **correct**, but
  `sep41_supply/dispatcher_adapter.go:81` still fails the whole event on a
  counterparty-decode error — dropping the mint AMOUNT — even though the supply SQL
  never reads counterparty. Any future unhandled topic shape re-introduces the exact
  99.96%-mint-undercount class, for a field the total doesn't need. **Fix:** capture
  Amount unconditionally; counterparty="" on failure.
- **CS-037 — SEP-41 lake path serves circulating == total (admin/treasury not
  excluded) — Medium.** For non-watchlisted tokens (the majority), `BasisSEP41LakeFlows`
  = Σmint−Σburn−Σclawback with no admin exclusion (flow sum lacks holder identity) →
  governance/vesting tokens with large treasury holdings have circulating + market
  cap systematically overstated. Documented limitation; worth surfacing per-asset.
- **CS-038 — no negative-circulating clamp (classic + SEP-41) — Medium (edge).**
  `classic.go:156`/`sep41.go:154` compute `circulating = total − holders` with no
  zero floor (only SEP-41 *total* is guarded); a locked-set > total (misconfig or
  snapshot-freshness skew) → **negative circulating + negative market_cap** served.
  **Fix:** clamp at zero or reject.

### A8 — Aggregation math (VWAP "oldest" bug FIXED; residuals)
- **CS-039 — VWAP partial-window truncation — Medium (monitored).** Oldest-N bug
  fixed (now `ORDER BY ts DESC LIMIT` + reverse = newest-N). Residual: window >
  `MaxTradesPerWindow` (10k) → VWAP over newest-N only but labeled as the full
  5m/1h/24h window. Detected + counted (`AggregatorWindowTruncatedTotal`).
- **CS-040 — USD-volume gate scales by quote TYPE not source `Decimals` — Medium
  (latent; ties P2-4c).** `orchestrator.go:1118` assumes 10^8 for fiat:USD, but FX
  pollers stamp 10^6 → an FX source contributing to a fiat:USD target computes USD
  volume ~100× wrong, corrupting `dropForMinUSDVolume`. No live impact (`min_usd_volume=0`
  on r1) but a latent trap the moment the gate is enabled with FX present — this is
  the risk behind keeping `min_usd_volume=0`. **Fix:** scale by per-trade source Decimals.
- **CS-041 — non-robust outlier filter — Low.** mean/stdev from the contaminated
  sample; a fat-finger inflates stdev → nothing filtered (or drags mean). Heuristic,
  volume-weighting limits impact; median/MAD would be robust.
- **CS-042 — MEV independent-cycle merge — Low.** Two independent round-trips in one
  tx emit as one multi-asset "arbitrage" (attribution imprecision; explorer feed).
- *Cleared (strong):* source-class VWAP gating correct (oracles/aggregators/routers
  excluded, unknown fails-closed, no leak); stablecoin fiat-proxy map correct;
  freeze recovery safe-direction; classic Algo2 + SEP-41 SQL signs exact big.Int;
  i128/float money discipline clean.

### A4 — DEX decoder fidelity (reinforces CS-026 per-decoder + new)
- **CS-026 detail (High):** Comet has **no contract gate at all** (pure topic bytes
  on Balancer-v1's shared `("POOL","swap")` — the ADR-0035 "one open case"); Phoenix
  + Aquarius gate on String action-topics only. Any look-alike → `trades` under a
  trusted `Source`. (Same High as CS-026; A4 pins the per-decoder specifics.)
- **CS-043 — Aquarius `Matches` comment claims a non-existent asset-allowlist —
  Medium.** `aquarius/dispatcher_adapter.go:24` comment asserts output is bounded by
  "the asset-allow-list in the decoder" — **no such allowlist exists** in code
  (`decodeTrade` decodes unconditionally). The false comment is itself a fidelity
  defect (misleads a maintainer into thinking the source is gated). **Fix:** add the
  gate the comment claims, or delete the false claim.
- **CS-044 — Phoenix 8-slot buffer silent overwrite can frankenmerge adjacent
  multi-hop swaps — Medium (drop-gated, untested).** `assign` overwrites slots
  unconditionally; if swap1 loses one field and swap2 shares the `(ledger,tx,op)`
  key, swap2's events fill swap1's missing slot → one merged trade with mixed
  economics + swap1's EventIndex. Requires a dropped field (live drop-rate ~0); no
  two-swaps-per-op test exists.
- **CS-045 — Aquarius strict 3-tuple arity drops ALL trades on a benign body-shape
  upgrade — Medium (edge).** `AsTupleN(body,3)` requires exactly 3; a future 4-tuple
  WASM (or backfill across it) → arity error → every trade that era dropped. Fails
  loud (orphan-spike), arguably correct-to-fail-closed, but a real upgrade fragility.
- **CS-046 — Soroswap swap+sync correlation captures NO sync data; it's a pure gate
  that drops the swap if the sync is absent — Medium.** `decodeSwap` never reads
  `r.Sync`; there's no reserves table. The documented "reserves come from the
  following SyncEvent" is **false** (never captured); the buffer's only effect is to
  require a sync before emitting → a swap whose sync is dropped is aged out + lost
  with zero data benefit. Live drop-rate ~0 (Uniswap-v2 always emits sync).
- *Cleared (strong):* **SDEX fully fixed** — all claim-atom types incl. `ClaimAtomTypeV0`
  (F-1233) + passive-offer dual-arm fallback, all 5 op types, both-zero dropped
  (tested); **soroswap_router `deadline_ts` overflow fixed** at the storage layer
  (`pgTimestamptzRepresentable` NULLs out-of-range); soroswap + router contract
  gating correct (multi-factory); i128 never truncated across all six; Map-by-name.

### A7 — External CEX/FX connectors — WELL-HARDENED (Chainlink uint80 fix confirmed)
- **CS-047 — Chainlink trusts operator `FeedSpec.Decimals`; never calls on-chain
  `decimals()` — Low.** `SelDecimals` selector is defined but **never called**; feeds
  fall back to `DefaultDecimals=8`. An operator adding a non-8-decimal feed (some
  ETH-denom Chainlink feeds are 18) without setting `decimals` → price mis-scaled
  10^(actual−8). Bounded: ClassOracle (excluded from VWAP), operator-gated. (Also F3:
  `updatedAt` reads low-8 bytes without checking the high 24 are zero — defense-in-depth.)
- **CS-048 — `BatchInsertTrades` skips `Trade.Validate()` — Low (latent).** The path
  external CEX trades take (`trades.go:391`) builds the multi-row INSERT with no
  per-row Validate (unlike `InsertTrade`/`InsertOracleUpdate`). Not exploitable today
  (dust floor + all external pollers emit OracleUpdate not Trade), but the canonical
  invariants (positive legs, valid pair/tx) aren't enforced on the batch path.
- **CS-049 — `decimalStringToScaledInt` has no input-length cap — Low (DoS d-in-d).**
  Per-venue CEX helpers call `big.Int.SetString` on raw vendor strings unbounded
  (unlike `canonical.FromString`'s 512 cap) → pathological field = unbounded big.Int
  alloc. Bounded by WS/read limits + requires a compromised TLS-authed vendor.
- *Also:* binance/bitstamp count `ErrDustTrade` as a decode error (metric hygiene —
  same class as CS-032; coinbase does it right). *Cleared (strong):* SSRF (all
  endpoints hardcoded const, `Endpoint` operator-only); secrets in headers not query
  + redacted on error; no `InsecureSkipVerify`; `io.LimitReader`+timeouts everywhere;
  no XXE (encoding/xml); class gating no-leak (aggregators/oracles emit OracleUpdate,
  can't reach trades-VWAP); reconnect idempotent (deterministic tx_hash + ON CONFLICT).

### A31 — API contract & spec drift (no High — undocumented admin route IS gated)
- **CS-051 — undocumented staff PII endpoint `/v1/account/admin/lookup` absent from
  OpenAPI — Medium.** Resolves an account by `?email=`/`?slug=` → cross-customer PII
  (tier, **billing email**, suspension reason, quota overrides, user list). **Auth is
  correctly enforced** (`RequireSession` 401 + `!IsStaff` 403 + audit-log +
  `Cache-Control:no-store` + full middleware/rate-limit) — NOT an auth bypass. Medium
  because a staff/PII surface must appear in the security-reviewed contract, and its
  omission is silent.
- **CS-052 — the CI contract-drift lint greps only `HandleFunc(`, misses
  `mux.Handle(` `/v1` routes — Medium (root cause).** `scripts/ci/lint-docs.sh:61`
  can't see the admin route (registered via `Handle(` to wrap `RequireSession`) in
  either direction → it slipped past the "1:1" guard. **Any future middleware-wrapped
  `Handle()` /v1 route bypasses the lint the same way.** **Fix:** extend the grep to
  `Handle(` too.
- **CS-053 — `/dashboard/*` OpenAPI security stanzas misdescribe the auth model —
  Medium.** No cookie/session `securityScheme` is defined; the dashboard ops inherit
  the global `[{}, {APIKeyAuth}]` (anon-or-bearer) while actually being session-cookie
  gated. Lie in the SAFE direction (more locked than declared) → no exposure, but
  misleads SDK codegen (emits API-key auth for cookie-gated calls) + security review.
  **Fix:** define a `cookieAuth` scheme + reference it on `/dashboard/*`.
- **CS-054 — spec header claims "spec↔handler 1:1, CI-enforced" (false) + cites the
  wrong section — Low** (doc drift; consequence of CS-051/052).
- *Cleared:* dual-shape `/assets/{slug}` is HONESTLY documented (`oneOf` +
  discriminator prose); no documented-but-unimplemented paths (all 88 resolve);
  postman + `types.ts` in sync at path level; `docs/reference` byte-identical;
  `/metrics` loopback-gated; health-family + `/account/*` stanzas accurate.

### A19 — Webhook delivery + Stripe — CLEAN (all 4 prior fixes hold)
- **CS-055 — outbound webhook HMAC has no timestamp/nonce; delivery-id unsigned →
  replay — Medium (lean Low).** `X-StellarIndex-Signature: sha256=hmac(body)` with no
  `t=` and the delivery-id header outside the signed bytes → a captured delivery
  replays with a valid signature forever; receiver dedup on the (unsigned) delivery-id
  is defeatable. Impact limited: payloads are notifications not commands (dup alert);
  mirrors GitHub's scheme. **Fix:** sign `t=<ts>.<body>` (Stripe-style).
- **CS-056 — registration error echoes the resolved internal IP → authenticated
  internal recon — Low.** `rejectInternalHost` returns "resolves to an internal
  address (<IP>)"; a manage-role user registers guessed internal hostnames and reads
  the org's private DNS→IP mappings from the differential error. **Fix:** generic
  "must be publicly routable."
- **CS-057/058 — delivery at-least-once edges — Low.** Claim leases `next_attempt_at`
  but doesn't bump `attempt_count` → a worker crashing between POST and Mark can
  re-deliver indefinitely without reaching the 15-attempt terminal; concurrent
  duplicate Stripe delivery of a fresh event can process twice (harmless — all side
  effects idempotent/absolute — but double audit row). Both inherent to lease-based
  delivery; receiver dedup is the intended mitigation.
- *Info:* stale `worker.go:10` doc claims "one worker, no SKIP LOCKED" contradicting
  the store (which DOES use FOR UPDATE SKIP LOCKED); HMAC key stored plaintext at rest
  (needed to sign; consider column encryption). *Cleared (strong):* SSRF guard holds
  end-to-end (registration + delivery-time re-resolve defeating DNS-rebinding + redirect
  not-followed + no proxy bypass + metadata-IP blocked); queue-claim race (FOR UPDATE
  SKIP LOCKED + lease); fan-out real; Stripe signature verified-before-process
  (constant-time, ±5min replay window) + dedup marks processed only on success (F-1322).

### A15 — Ops mutation CLI — no Critical/High (one-writer intact, destructive ops guarded)
- **CS-059 — `mint-key -tier operator` mints a permission-bypassing, any-IP admin
  key via the same flow as a customer key, no confirmation — Medium.** operator-tier
  bypasses all keypolicy permission + IP-allowlist checks (`keypolicy.go:66,77`); a
  single mistyped `-tier` silently escalates a customer key to full admin. **Fix:**
  a distinct confirmation/guard for operator-tier mint.
- **CS-060/061 — every minted key is `PermissionsAll:true` (CLI can't scope) + issuance
  audit is stderr-only (no persistent/tamper-evident log) — Low.** Per-endpoint scoping
  is dashboard-only; no server-side append-only mint/upgrade record.
- *Cleared (exemplary):* one-writer invariant intact (`ch-rebuild` is ADR-0034-
  sanctioned + idempotent across all 16 projected tables via ON CONFLICT; `backfill`
  drops projected events; `projector-replay` is cursor-only via `RewindCursor`);
  `trim-galexie-archive` heavily guarded (dry-run default, verify-upstream, max-files
  cap, required `--older-than-ledger`, cold-tier-required); no CLI TRUNCATE/DROP/DELETE;
  both verifiers read-only; **zero SQL/shell injection** (identifiers hardcoded, args
  argv-exec, source whitelisted); idempotency solid. Key entropy good (`sip_`+256-bit).

### A23 — Secrets & config — prior Highs CLEARED (drift now guarded by a passing test)
- **CS-062 — `auth_backend` not validated → mis-cased value silently reverts to Redis
  → dashboard revocations no-op against the runtime — Medium (security-flavored).**
  `validate.go:505` validates `auth_mode` but NOT `auth_backend`; consumption is
  case-sensitive `== "postgres"` (`main.go:1152,374`). Operator writes `"Postgres"` or
  `"postgres "` intending the PG cutover → runtime silently validates against **Redis
  legacy keys** while the dashboard writes to the PG `api_keys` table → **a key revoked
  in the dashboard keeps authenticating.** (Contrast: `auth_mode` rejects unknowns
  loudly.) **Fix:** `switch AuthBackend { case "redis","postgres": default: ErrInvalidConfig }`.
- **CS-063/064/065/066 — Low:** `history_archive_url` validation weaker than siblings
  (no `://` check → bare host/empty passes, fails later); contradictory CORS
  (`["*"]`+credentials) caught by a boot **panic** not `Validate()` (violates the
  reject-at-startup contract); `postgres_dsn` (may embed a password) echoed into one
  `ErrInvalidConfig` message → secret to local logs (redact to scheme); `redis_password_env`
  doc says "reference not the value" but code stores the value (self-inconsistent doc).
- *Cleared (strong — recurring root-cause classes now guarded):* **config-tag drift
  (prior High F-1327) is FIXED + guarded** by a passing `TestDefault_MatchesStructTags`
  (signup-verify=true, cookie_secure=true, projector persist_per_source=true all in
  lockstep); validate()-on-copy panic path doesn't exist (value receivers, no deref);
  sep41 projector foot-gun closed (persist_per_source defaults true + F-1316 comment);
  no secret in logs/metrics-labels/debug-endpoint; every `env:` secret wired through
  `ApplyEnvOverrides`. (Drift-test blind spot: map/struct-slice tags uncompared — but
  all such fields are empty=safe.)

### A10 — Timescale served tier — disciplined, no Critical/High
- **CS-067 — `ON CONFLICT DO NOTHING` makes corrective re-decodes silent no-ops —
  Medium.** Every fact-table insert (trades/oracle/soroban_events/blend_*/phoenix_*/
  cctp/rozo/defindex/…) uses DO NOTHING on the immutable-fact PK. After a decoder bug
  is fixed (this repo's own history: sep41 counterparty loss, router deadline
  overflow), **re-running the backfill over already-populated ledgers is a pure
  no-op — stale/wrong rows are NOT corrected.** Correction needs explicit
  DELETE/TRUNCATE-then-reinsert or `ch-rebuild`. "Just re-run the backfill" = false
  sense of repair. (Remediation-correctness, not steady-state loss.)
- **CS-068 — `prices_1m`/`prices_15m` caggs undercount late-arriving trades beyond
  the refresh `start_offset` — Medium (cagg gap).** Policy windows are 5min/1h and
  backfill deliberately excludes them (`CAGGsLiveForever`). **Scenario:** the indexer
  falls behind and catches up (a documented past incident) → trades inserted during
  catch-up carry ledger-close timestamps older than the window → those minute buckets
  stay empty/undercounted, and `/price` reads `prices_1m` → a post-outage hole in the
  recent-minute VWAP. Self-heals via 30-day retention; `prices_1h+` unaffected. **Fix:**
  a catch-up-aware refresh of the affected recent buckets.
- **CS-069 — `source_volume_1h` values historical XLM volume at the CURRENT vwap —
  Low.** Read-time approximation (cagg can't join prices_1m); backs the source-page
  chart only, not a money endpoint.
- *Cleared (strong):* SQL injection refuted (every dynamic identifier is compile-time
  literal or allow-listed — `RefreshContinuousAggregate` gated by `allowedCAGGViews`,
  gap-detector targets hardcoded); NUMERIC↔big.Int no truncation (amounts round-trip
  as decimal strings; only float read feeds the anomaly-baseline, not served);
  coarse-PK refuted (op_index fan-out `opIdx*1024+i` + `ErrPriceVectorOverflow` guard +
  event_index in PK); `observed_at` idempotency refuted (all stamp ledger-close time,
  deterministic → replays dedup); transaction/poison-row refuted (per-row fallback in
  sink); real-time cagg 0069 refuted (Timescale watermark UNION, no boundary double-count).

### A34 — Cross-package data-flow + resilience-test adequacy
- **CS-070 — `-tags=integration` + chaos suites are compiled in CI but NEVER
  executed → every regression guard is latent — Medium.** `ci.yml:127` runs
  `test-integration-build` (`-run nothing -count=0` = compile only); `make
  test-integration` (real testcontainers) runs in **no** workflow, no cron; chaos is
  scripts-only though `04-redis-misconf.sh` self-describes as "the CI-side guard."
  So the cascade-503, retention-absent, NUMERIC-arithmetic, and 503-mapping
  assertions only run if a human runs them locally → **a real regression in an
  integration-tested path ships CI-green** (the meta-condition enabling the test-rot
  class). **Fix:** wire `make test-integration` into a Docker-enabled nightly/CI job.
- *Cleared (strong):* **all 33 `consumer.Event` types have a sink arm** + the sink
  `default` is LOUD (`SourceInsertErrorsTotal{unhandled}` + Warn) not silent — the
  CLAUDE.md "silent drop" trap is structurally prevented; `IsProjectedEvent` ↔ registry
  lockstep is unit-tested + CI-run; canonical Amount/Asset/Pair round-trip as decimal
  strings (no int64/float on the money path; `ChangeSummary`/`usd_volume` float is
  derived-analytics, ≤2^53-safe); k6 hits only current routes (`/coins` is comment-only,
  already remediated); ops package compiles under `-tags=integration`; `migrations_test`
  actively *guards* the retention drift.

### A20 — Email / magic-link — strong posture, no Critical/High
- **CS-071 — User-Agent injected verbatim into the PLAINTEXT magic-link email →
  phishing-line injection — Medium.** `truncateUA` (`handlers.go:776`) does length-only
  truncation, **no CR/LF stripping**; the text template does no escaping. A crafted
  `User-Agent` on `POST /v1/auth/login` injects arbitrary lines ("URGENT: account
  compromised — call …") into a trusted, DKIM-signed, Stellar-Index-branded email
  (plaintext part). HTML part is safe (`html/template` escapes). **Fix:** strip CR/LF
  from UA (and prefer not echoing it).
- **CS-072 — `/v1/signup` 409 is an email-existence oracle — Medium.** A 409-vs-200
  reveals whether an address has a self-service API-key account (the dashboard login
  flow correctly always returns `{status:sent}`). Mitigated by 5/hour/IP throttle +
  narrow population. **Fix:** generic accepted-response + defer the collision.
- **CS-073/074/075 — Low:** 6-digit code non-uniform (`'0'+c%10` over base32 gives
  digits 0/5 four symbols each → ~3.8× brute edge; use `%1e6` on hash bytes);
  LoginThrottle **fails OPEN** on Redis outage (email-bomb + brute-amplification
  window — asymmetric vs the signup throttle which fails closed after 30s); session
  token = DB PK via `uuid_generate_v4` (prefer `gen_random_uuid`).
- *Cleared (strong):* magic-link token 256-bit crypto/rand + sha256 (no timing
  channel); **verify-code brute REFUTED** (5-attempt cap enforced across ALL in-flight
  tokens atomically in PG + token-count capped in Redis + constant-time compare);
  token single-use/expiry atomic (UPDATE…RETURNING / GETDEL); **SMTP header injection
  refuted** (JSON REST body, not SMTP); HTML email XSS refuted (html/template);
  zero-time "2055" refuted (IsZero guard); nil-Now panic refuted (double-default);
  session binding + HttpOnly/Secure/SameSite correct; callback open-redirect refuted
  (`/` + not-`//` check); cross-purpose token confusion refuted.

### A17 — Rate-limit / quota — core race-free; fail-open-forever FIXED
- **CS-076 — rate-limit + monthly-quota are per-KEY, not per-account → tier budget
  multiplies ×25 — Medium.** Every enforcement key is namespaced by `KeyID`; no
  per-account aggregate. A Free account (60/min ceiling) mints its 25-key max → 25×60
  = **1,500 req/min** (and 25× the monthly quota). **Fix:** add a per-account
  aggregate limiter.
- **CS-077 — tier downgrade doesn't re-clamp existing keys' `RateLimitPerMin` —
  Medium.** The tier ceiling is applied only at create; `apikey_postgres.go:148`
  returns the persisted `RateLimitPerMin` verbatim. A Pro account mints a 10k key,
  downgrades to Free (60/min) → the key keeps 10k until manually re-minted. **Fix:**
  re-clamp on downgrade (or recompute from current tier at auth time).
- **CS-078 — monthly quota is read-then-allow with no reservation → concurrent
  over-admit — Medium (soft).** `MGET`→`if used>=quota deny`, INCR later; N concurrent
  requests at `quota-1` all pass → overshoot ≈ concurrency. Doc frames quota as
  "billing fairness not security" (fails open on Redis error by design), so soft — but
  the cap is advertised as a ceiling.
- **CS-079 — dwell-time fail-closed resets on ANY single Redis success → a FLAPPING
  Redis stays fail-OPEN indefinitely — Medium.** `observeRedisSuccess` zeroes
  `redisErrorSince` on one OK round-trip, so the 30s→fail-closed only trips on
  *continuous* failure. A degraded Redis (answers 1/few-sec, errors the rest) never
  reaches `ErrThrottleUnavailable` → rate-limiting effectively disabled for the whole
  partial outage (the more common Redis failure mode). **Fix:** track failure *ratio*,
  not just continuity.
- **CS-080/081/082 — Low/residual:** 30s fully-open window per outage + MonthlyQuota
  has no dwell (fails open the whole outage — F-0050 residue); unescaped user fields
  in markets/assets cache keys → key-aliasing + cardinality amplification (public data
  only, nuisance DoS); `context.Canceled` counted as a Redis failure (metric noise);
  MonthlyQuota not tier-clamped at create; no metric on the fail-CLOSED branch;
  maxmemory eviction of `rl:` keys can reset a counter mid-window.
- *Cleared (strong):* token-bucket atomicity (single Lua `INCR`+`EXPIRE`, no
  read-modify-write race); deny/allow decoupling correct; **F-0049/F-0050 fail-open-
  to-unlimited-forever FIXED** (sustained outage fails closed after 30s dwell); oracle
  cache-key poisoning refuted (validated + `|`-delimiter-safe); usage counter atomic
  (TxPipeline); quota reset boundary + timezone correct; **anon UA-rotation bypass
  (F-1335) FIXED** (keys on resolved IP); tier clamp at create (F-1212) enforced;
  api-key cache key unforgeable (sha256, no plaintext at rest).

### A13/A14 — Completeness verdict + divergence — the "verified/complete" trust story has real holes (several High)
- **CS-083 — watermark write OVERWRITES, not `GREATEST`, → `complete=true` can be
  pinned at a STALE tip — High.** `completeness_snapshots.go:34` does a plain
  `SET watermark_ledger = EXCLUDED…` + bumps `computed_at=now()`. The 25k-chunk driver
  passes `-to chunkEnd < tip` for every non-final chunk, each writing `complete=true`
  at `tip=chunkEnd`; if the walk **stalls mid-source** (`run-compute-completeness.sh:60`
  break), the snapshot is left `complete=true, coverage_pct=1.0, fresh computed_at` at
  an old tip. (This is a hole in the "15/15 green + self-maintaining" verdict from
  earlier this session.) **Fix:** `GREATEST(...)` on watermark + only stamp complete at
  the true tip.
- **CS-084 — the `-ch` projection reconcile compares TOTALS, netting compensating
  discrepancies to Δ=0 → false `complete=true` — High.** `reconcileProjectionAggregate`
  sets `projOK = (Σexpected − Σserved == 0)`; the exact per-ledger `ReconcileCounts` is
  wired only on the **legacy Postgres** path, but production is `-ch`. A real projection
  drop in ledger L masked by a phantom overcount elsewhere in the ≤25k chunk (e.g. an
  old decoder wrote more rows/event than the re-derive) cancels → served↔lake gap
  reported complete. Data isn't lost (lake retains) → false served-tier complete, High.
  **Fix:** run the per-ledger reconcile on the `-ch` path (it exists, just unwired there).
- **CS-085 — childgate preseed reads retention-exposed Postgres `soroban_events`
  even in `-ch` mode — Medium.** A pre-window factory `deploy` trimmed from PG →
  gated decoder won't match its events in the CH re-derive → false `complete=false`
  (safe direction), but blend/soroswap **flap** `complete=false` when old deploy rows
  age out. **Fix:** CH-native preseed (acknowledged follow-up).
- **CS-087 — divergence silently PASSES when references are unavailable; no
  "no-reference" signal — High.** When CoinGecko is 429'd (its current state) + Chainlink
  unreachable, `Compare` buckets them into `Failures`; `RefreshPair` writes
  `WarningFired=false` (SuccessCount=0) and the API serves `divergence_warning=false`
  on both not-found + error paths, with no `divergence_checked`/`reference_count`
  companion. A consumer can't distinguish "verified: agrees" from "couldn't verify."
  **The flagship XLM/USD has CoinGecko as its ONLY reference** (Chainlink's feed map
  omits XLM) → with CoinGecko dead, XLM/USD divergence can **never fire, always false.**
- **CS-088 — the one live divergence alert can't observe reference outages — High.**
  `divergence_refresh_error_dominant` fires on `refresh_error > ok`, but `RefreshPair`
  returns nil even when ALL references fail (only marshal/Redis errors are non-nil) →
  emits `outcome="ok"` → alert stays green. **Divergence can go fully dark with zero
  pages** (and `divergence_observations` silently stops filling, unwatched by
  data-freshness). **Fix:** emit a distinct "no-reference" outcome + alert on it.
- **CS-089 — stale Chainlink `latestAnswer()` treated as fresh — Medium→High.**
  `chainlink.go` reads `latestAnswer()` and ignores `observedAt`; a frozen/deprecated
  feed (long-heartbeat FX) returns a stale-but-positive value passing the only guard
  (`Sign()<=0`) → masks a real divergence or fabricates one. **Fix:** `latestRoundData()`
  + `updatedAt` staleness reject.
- **CS-090 — "N/N complete" headline + both alerts are blind to a `complete=true`
  verdict at a stale tip — Medium→High.** `coverage_verdicts.go` computes complete
  purely from `sn.Complete` with no `ComputedAt`-recent / `TipLedger`-near-head check;
  `data-freshness.sh` only alarms on `computed_at>36h` (refreshed every chunk) and
  `complete=false`; there's **no `node_systemd_unit_state` alert on
  compute-completeness.service** (unlike verify-archive), so a driver `rc=1` rots
  silently. The "15/15 complete" badge can be green while a source's tip lags the
  network arbitrarily. **Fix:** alarm on `min(tip_ledger)` vs the live ledgerstream cursor.
- **CS-086 — `hashdb` unwired → the ADR-0016 R2/R3 byte-rewrite drift-detection is
  UNIMPLEMENTED anywhere — Medium (doc/trust honesty).** `verify-archive` + the CH
  substrate cover gaps + chain-breaks but NOT silent byte-rewrites of already-fetched,
  still-chain-valid objects; the ADR-0016 trust story is partly notional. R2/R3 deferred
  (latent). **Fix:** mark ADR-0016 "not yet implemented," matching ADR-0033/CLAUDE.md honesty.
- *Cleared:* `ComputeWatermark` reduction logic sound (no false-complete branch);
  `RecognitionGap` correct; env `rpc_url` split FIXED (`CHAINLINK_RPC_URL` now sets both
  ingest + divergence — caveat: unset → `cloudflare-eth.com` JS-challenge → silent fail;
  direct-TOML values can still diverge); CH `SubstrateProblem` contiguity/hash-chain
  excluded (covered by A11, sound).

### A28/A29 — CI / release / deploy — several High (mostly repo-settings + rollback)
- **CS-097 — `main` is UNPROTECTED → every CI gate is advisory, never blocks — High.**
  (Corrects a stale premise: `ci.yml` DOES run on push-to-main since F-1231 — but
  `main.protected=false`, so a red commit is already landed and the next push's
  concurrency cancels the red run.) Import-boundary, docs-drift, pk-discriminator,
  gitleaks, govulncheck all become notifications. **Root enabler.** **Fix (repo
  settings, not a file):** branch protection + required checks on `main`.
- **CS-098 — boundary gates are bypassable by editing the gate's own config in the
  same commit — High.** `lint-imports.baseline` + per-rule `allow` arrays (and the
  gitleaks/actions-pinning allowlists) are plain repo files; a violation + a baseline
  edit lands green, and on unprotected main with no required review, no one blocks it.
- **CS-099 — migrations apply BEFORE binaries and are NOT rolled back on binary
  failure → old-binary-on-new-schema — High.** `deploy-binary.yml` runs
  `migrate up` in `pre_tasks`; the rescue restores only the previous **binary**. A
  renaming/dropping migration + a failed health probe = the rolled-back old binary
  runs against a schema it doesn't understand. "Atomic rollback" doesn't cover the DB.
- **CS-100-CI (Medium):** `/decode.go` substring blanket-exempts EVERY decoder from
  the no-RPC-in-ingest rule (invariant #6 hole); actions-pinning hard-fail is a no-op
  on push-to-main; CI concurrency cancels in-flight main runs (intermediate commits
  unverified); **Postman collection NOT drift-guarded** (silent drift — types.ts IS
  guarded, CLAUDE.md stale); no artifact **signing** (SHA256SUMS = integrity not
  authenticity, unsigned, same-release); multi-binary deploy not atomic (mixed-version
  fleet); `creates:` guard can silently skip install + report SUCCESS; rollback
  corrupts state if the backup `mv` itself fails; billing-capped runs indistinguishable
  from didn't-run. *Cleared:* **the gitleaks `curl-auth-header` allowlist I added this
  session is correctly scoped (empirically tested — merges + preserves the rule, not
  over-broad)**; F-1298 input-injection fixed; known_hosts MITM fixed; `migrations_skip
  | bool` fixed; all third-party actions SHA-pinned.

### A22 — SEP-1 / currency trust — SSRF guard strong; one High enforcement gap
- **CS-100 — `org_name` shown as authoritative identity WITHOUT enforcing
  `org_verified` → issuer impersonation / phishing — High.** `home_domain` is
  attacker-controllable (any account `SetOptions`); a scam issuer sets
  `home_domain=circle.com` → `sep1-refresh` fetches Circle's toml, extracts
  `ORG_NAME="Circle Internet Financial"`, correctly computes `org_verified=false`
  (Circle's toml lists Circle's issuer, not the scammer) — **but the detail endpoint
  (`issuers.go:239`) drops `org_verified` and the explorer detail page headlines the
  spoofed `org_name` as the `<h1>` with no contradicting signal.** `org_verified` is
  consumed in exactly ONE place (the list badge); the detail page/API ignore it.
  **Fix:** surface `org_verified` on the detail endpoint + don't render an unverified
  `org_name` as authoritative.
- **CS-101 — Low/Med:** no port allow-list on the SEP-1 fetch (`host:port` accepted →
  blind public-port probe from our egress via the cron; internal blocked by the
  dialer); no `recover()` on SEP-1 parse (kills the refresh batch; would crash the API
  if `metadata.Cache` is ever wired live); NAT64 `64:ff9b::/96` absent from the
  deny-list; log-forging via raw `home_domain` `%s`. *Cleared (strong):* **SSRF core
  is excellent** (resolves host itself, blocks all private/metadata/CGNAT IPs, connects
  to the exact checked IP — no TOCTOU/rebind, `Proxy:nil`, https-only, redirect-guarded);
  parse robustness; **display_decimals-as-scale FIXED (F-1321)**; catalogue
  auto-populate refuted (embedded seed only, unique-slug enforced); org_verified
  bidirectional *logic* correct (only enforcement fails → CS-100).

### A30 — Frontend security (non-a11y) — no Critical; admin + secrets CLEARED
- **CS-102 — unvalidated issuer `home_domain` rendered as a clickable link in
  `AssetSidebar` → phishing — Medium.** `AssetSidebar.tsx:143` renders
  `https://${homeDomain}` gated only on truthiness, NOT `isSafeHomeDomain` — the one
  place that forgot the guard the issuer pages all have (`good.com@evil.example` →
  navigates to evil). Not XSS (React escapes). **Fix:** wrap in `isSafeHomeDomain`.
- **CS-103 — Low/hardening:** OG function interpolates URL into markup (image not HTML
  — content-spoof only, dup of CS-009); `runbook_url` href no scheme validation
  (operator-authored); magic-link token in URL (mitigated); prod source maps shipped
  (intentional). *Cleared (strong):* **admin data is SERVER-gated** (401/403, unhiding
  UI leaks nothing); **no secrets in bundle** (only NEXT_PUBLIC_* non-sensitive);
  XSS refuted (serializeJsonLd + markdown scheme-allowlist, no other sink); embeds
  static (no reflection, no postMessage); open-redirect guarded; cookie scope correct.

### A26 — Edge / TLS / CDN — no Critical; auth data provably off the CDN
- **CS-104 — CSP `script-src 'unsafe-inline'` neuters the XSS backstop — Medium
  (hardening).** Explorer + status + dashboard; React escaping is currently the ONLY
  thing between attacker-controlled issuer/asset strings and script execution (this
  CSP would NOT have backstopped the prior SEP-1 ORG_NAME XSS). Hard to fix under
  static-export (nonces need SSR). **CS-105 — `/embed/*` frame-ancestors override is
  SILENTLY DEFEATED by CF Pages header MERGE — Medium.** The `_headers` comment
  claiming "full replacement not merged" is **factually wrong** — CF comma-joins → the
  parent `frame-ancestors 'none'` + embed `*` intersect to **framing DENIED**, so the
  embed widgets (whose whole purpose is to be iframed) **can't be framed anywhere**.
  Fail-safe direction (no clickjack), but a **broken product feature + false security
  claim in config**.
- **CS-106 — Low:** Caddy trusts all CF CIDRs while r1 is directly internet-exposed
  (CF not yet fronting — the README's own rule says delete the block until then);
  insecure default `listen_addr=0.0.0.0`/wildcard CORS (r1 pins loopback+narrow, safe);
  CF ignores `Vary` on non-image (public-data CORS flakiness); r1 omits
  `allow_credentials` while a doc says the cookie flow needs it (verify). *Cleared
  (strong):* **auth data provably off the CDN** (`private,no-store` on all account/auth/
  dashboard + a no-store DEFAULT branch); open-redirect refuted (68 `_redirects`, host
  always fixed); CORS exact-match + Vary; XFF rightmost-untrusted walk sound; TLS 1.3/
  auto-LE; HSTS/XFO/nosniff present; dashboard `_headers` gap (prior) resolved.

### A27 — Monitoring & alerting — no High; critical alerts all fire
- **CS-107 — `timescale_cagg_stale` alert is INERT (no emitter) yet caggs are active
  on r1 → a stalled cagg → stale `/v1/price` is unmonitored — Medium.** Ties CS-068.
  **CS-108 — the 6 SLO-burn alerts link to generic `api-latency.md`/`api-5xx.md`, never
  to their dedicated `slo-*-burn-*.md` runbooks — Medium** (operator gets the wrong
  runbook mid-page).
- **CS-109 — Low:** `stellar.yml` ships 4 alerts (incl. a **page**) on the removed
  stellar-core exporter (can't fire; covered elsewhere); compression-lag inert; stale
  "check stellar-rpc" triage prose (removed 2026-04-23); 4 orphan runbooks; minio
  annotation cites a never-emitted metric name; `oracle_last_update_unix{asset}`
  cardinality latent (unguarded if a passthrough oracle lands). *Cleared (strong):*
  ALL critical alerts fire on live emitters (**backup/ZFS/primary-down/ingest-frozen/
  data-freshness** — the F-1329 ZFS+backup dead-refs are fixed + lint-guarded); rule-dir
  drift clean (1:1 alerts/records); self-defeating cascade backstopped (every exporter
  has an independent `*_exporter_down` + the healthchecks deadmansswitch); all exporters
  scraped (F-1220); price-staleness cardinality bounded to the curated pair set.

### A35 — Disaster recovery — the DR story is UNPROVEN end-to-end (several High)
- **CS-110 — a restore has NEVER been executed or drilled — High.** Backups run +
  report healthy, but the only "drills" are tabletops (narrated, simulated times), and
  the one DB drill chose fix-in-place (`drop_chunks`) over restore. **A backup verified
  only by `pgbackrest info` is unproven.** The "actually run drop_chunks on staging"
  action item is still unchecked. **Fix:** perform a real restore-to-scratch drill.
- **CS-111 — the pgBackRest repo is co-located in the same-host MinIO / ZFS pool as
  the DB it protects → single failure domain — High.** Repo lives in the local MinIO
  `backups` bucket on r1; no `repo2`/offsite, no replica. **A host or ZFS-pool loss
  takes out the primary AND every backup at once.** (Operator: confirm any out-of-band
  offsite copy — the repo/ansible provide none.) **Fix:** offsite `repo2`.
- **CS-112 — the ClickHouse lake (the ADR-0034 source of truth) has ZERO backup —
  High (re-derive caveat).** No clickhouse-backup / BACKUP / S3 export anywhere; CH
  isn't even Ansible-provisioned (the `data/clickhouse` ZFS dataset isn't codified).
  Re-derivable from `galexie-archive` in principle, but there's **no documented
  procedure** and it's a multi-day full re-decode of billions of rows — reintroducing
  the exact cost ADR-0034 exists to avoid. **Fix:** back up the lake or document +
  test the rebuild.
- **CS-113/114 — Medium:** `dr-activation.md` is marked "Drilled/ratified" but its whole
  mechanism assumes Patroni + multi-region that **doesn't exist** on single-host r1 (no
  replica → the "replication is our other safety net" fallback is fictional);
  `backup-failed.md` — the runbook opened DURING a backup incident — uses the wrong
  stanza `--stanza=main` (real = `stellarindex`), producing a "missing stanza" error at
  the worst moment. *Solid:* the Postgres restore path exists as code (Patroni bootstrap,
  PITR-correct) but is unlanded/untested on r1's single-host topology; MinIO/galexie
  recovery is documented + runnable.

### A36 — Licensing / redistribution
- **CS-115 — raw per-source CEX trades are re-served through the public API → vendor-ToS
  exposure — Medium.** `/v1/history` + `/v1/observations?source=binance` return raw,
  per-trade, **source-attributed** records (price+size+ts tagged `source=binance`),
  filterable by venue. Binance/Kraken/Coinbase market-data terms generally prohibit
  redistribution. The blended outputs (`/v1/price|vwap|twap|ohlc`) are derived + far
  more defensible. `Metadata` has **no `redistributable`/`license` field** gating raw
  endpoints. **Needs legal review before commercial launch.**
- **CS-116/117 — Medium/Low:** paid FX (`exchangeratesapi`/`polygonforex`) flows into
  served VWAP; CoinGecko free/Pro terms; **no vendor-ToS/attribution doc anywhere**. No
  copyleft *conflict* in the binary (goxdr is dual GPL/Apache → Apache elected; MPL deps
  Apache-compatible) — **but no CI SBOM/license check + no NOTICE/THIRD_PARTY file**, so
  a naive scanner will false-alarm on goxdr at the pre-flip SBOM gate. On-chain sources
  are unrestricted.

### A24/A25 — Ansible / systemd — carefully hardened overall, but concentrated Highs
- **CS-118 — deployed app services run as `root`; a hardened non-root unit exists but
  is NOT the one deployed — High.** The `.j2` templates that actually install
  (`stellarindex-{indexer,api,aggregator}.service.j2`) set `User=root` with no
  ProtectSystem/PrivateTmp/ReadWritePaths; the **internet-facing API** runs as root on
  the archive box (same host as MinIO lake + Postgres). The correctly de-privileged unit
  sits unused in `deploy/systemd/`. **Fix:** deploy the hardened units (needs CS-119 first).
- **CS-119 — the `stellarindex` OS user is NEVER created by the role — High.**
  `04-users.yml` creates only stellar/galexie/minio, yet units + a chown reference
  `stellarindex:stellarindex` → a clean apply (fresh host / DR rebuild) FAILS, and it's
  why CS-118 can't just flip to the hardened non-root units. **Fix:** add the user.
- **CS-120 — `sshd_config.j2` PasswordAuthentication gate INVERTS on a string override —
  High.** `{{ 'no' if not ssh_permit_password_auth else 'yes' }}` with the common idiom
  `-e ssh_permit_password_auth=false` (string, truthy in Jinja) → `not "false"` = False →
  renders **`PasswordAuthentication yes`** — the operator's "disable" silently ENABLES
  SSH password brute-force. The migrations_skip string-truthiness class, security-critical.
  **Fix:** `| bool`.
- **CS-121 — the standalone `configs/alertmanager/apply.sh` installs the rendered config
  `0644` world-readable — with the Discord webhook + deadman secrets in it — High.**
  `install -m 0644` (the Ansible path correctly uses `0640 root:alertmanager`). Any local
  unprivileged user reads the Discord webhook (a bearer capability → post spoofed
  all-clears / delete the webhook to blind alerting). **NOTE: this is the `apply.sh` I
  edited this session for the Slack→Discord migration — the `0644` was pre-existing (I
  changed receivers, not the install mode), but my change now routes Discord secrets
  through it. Fix: `install -m 0640`.** 
- **CS-122 — Patroni REST API defaults to `0.0.0.0:8008` with EMPTY basic auth →
  unauthenticated cluster-destroy from the /8 internal range — High.** Mutating endpoints
  (`/switchover`,`/failover`,`/restart`,`/reinitialize`=datadir-wipe) are unauth by
  default; the template omits the auth block when user/pass are empty; firewall allows
  8008 from `10/8+172.16/12+192.168/16`. (Latent — Patroni unlanded — but a foot-gun.)
- **CS-123 — Medium:** app units lack PrivateTmp/ProtectSystem even as root; secrets on
  `mc`/`redis-cli` argv during deploy (visible in `ps`; false "never on argv" comment);
  etcd plaintext HTTP no client-auth; node_exporter binds `0.0.0.0` (comment says
  loopback); SSH allow-list defaults `0.0.0.0/0`; `haproxy_prometheus_endpoint` +
  `run_minio` + `galexie` gates share the string-truthiness class (low blast radius).
  *Cleared (strong):* on-host config secrets all `0640`+`no_log`; `when:` gates safe
  (Ansible coerces "true"/"false"); Redis-Sentinel HA sound (quorum 2/3, requirepass both
  planes, ACL lockdown — F-1271); Patroni core HA sound (sync_mode, pg_rewind, DCS
  quorum) apart from CS-122; **IPv6 firewall bypass cleared** (policy drop, v4-scoped
  accepts); timer Persistent backlog cleared (coalesce + jitter + staggered); supply-chain
  tarballs all sha256-pinned; idempotency sound except CS-119. (No project Dockerfiles —
  only a dev compose, version-tag-pinned, non-prod.)

### A16 — Authn/authz + SEP-10 — strong; one High (CSRF)
- **CS-124 — CSRF on cookie-authenticated `POST /v1/dashboard/*` mutations — High.**
  The dashboard session cookie is `Secure; HttpOnly; SameSite=None`, and the mutating
  handlers authenticate on it alone — no CSRF token, no Origin/Referer check, no
  Content-Type gate. A cross-site auto-submitted form with `enctype="text/plain"`
  (CORS-safelisted → **no preflight**) hits `POST /v1/dashboard/webhooks` with the
  victim's cookie → creates a webhook pointing at `evil.com` on the victim's account →
  **all future webhook payloads exfiltrate to the attacker** (no response read needed).
  Bounds (re-verified): PATCH/DELETE are safe (non-simple → preflight blocks); `/v1/
  account/*` is safe (Bearer header, non-ambient). `SameSite=None` is also *unnecessary*
  — `stellarindex.io` + `api.stellarindex.io` are the same registrable site, so `Lax`
  would serve the legit flow while blocking cross-site. **Fix:** `SameSite=Lax` or an
  anti-CSRF control (double-submit token / Origin allow-list) on cookie-auth POSTs.
- **CS-125/126 — Low:** SEP-10 replay guard fails OPEN when Redis is absent (r1 has
  Redis so active; `ErrReplayGuardUnavailable` exists but isn't enforced in the sep10
  path → fail loud); SEP-10 `/token` (CPU-bound Ed25519 verify) sits behind only the
  shared 60/min anon-IP bucket (no dedicated throttle → distributed crypto-load).
- *Cleared (strong):* middleware ordering sound (Auth/KeyPolicy/EmailVerified/Quota all
  BEFORE RateLimit; nothing served outside the stack; Envelope404/TrailingSlash/CORS
  can't bypass Auth); **JWT alg-confusion/signature/claims sound** (fixed header compare
  rejects alg:none/RS256, constant-time sig, iss+exp enforced); challenge replay SETNX
  atomic + marked only post-verify; **API-key validation constant-time, prefix (rek_/sip_)
  cosmetic — no split-brain**; **Redis-vs-Postgres revocation split-brain EXPLICITLY
  GUARDED** (postgres backend nils accountStore → self-service 503s with a warning);
  XFF spoofing closed (F-1335); auth_mode optional downgrade fail-CLOSED (invalid key →
  401, unwired → 503, `none` → handlers still reject anonymous); cookie flags + no-store
  correct; RequireEmailVerified not bypassable.

### A32 — Docs/ADR integrity (LAST area) — well-maintained; drift in 3 clusters
- **CS-127 — CLAUDE.md's ADR-0035 claim is a FALSE SAFETY PROPERTY — High.**
  CLAUDE.md:271 says decoders "gate `Matches()` on contract identity, not topic bytes
  … Comet is **the one open case** … the other Soroban decoders use [factory-fan-out]."
  Reality: only **blend + soroswap** gate on identity; **phoenix, aquarius, AND defindex
  also gate on topic symbol only** → **four** decoders ungated, not one (ADR-0035's own
  checklist marks all four `[ ] NEEDS WORK`). The agent entry point asserts a
  mis-attribution-can't-happen property that's false (reinforces CS-026/CS-043). **Fix:**
  correct CLAUDE.md to match the ADR checklist.
- **CS-128 — 5 incident runbooks use the wrong config path `/etc/stellarindex/config.toml`
  (real: `/etc/stellarindex.toml`) → file-not-found mid-incident — High.** `cursor-stuck.md`,
  `source-stopped.md`, `insert-errors.md`, + 2 archived. Single find-replace.
- **CS-129 — `insert-errors.md` gives `kubectl exec/logs` commands on a bare-metal
  systemd fleet (self-contradicts at line 54) → `command not found` mid-incident —
  High.** Plus `db-disk-full.md` k8s PVC-expansion on the ZFS fleet.
- **CS-130 — Medium:** ADR-0003 body still claims the (nonexistent) golangci analyzer +
  BIGINT migration check despite a reality-note bolted on top → self-contradictory
  (confirms CS-007); CLAUDE.md map says MinIO adapter is in `internal/storage` (it's in
  `internal/pipeline`) + omits `internal/xdrjson`; runbook metric/alert name errors
  (`postgres_connections_high`, `cagg_last_refresh_unix` INERT, stale `stellar-rpc`
  narration); `/v1/account/admin/lookup` undocumented + the `.Handle(` drift-guard blind
  spot (confirms CS-051/052); stale "plan/planning/proposal/build" status labels on
  shipped systems (clickhouse-migration-plan, stellar-focus Unit D — which actually
  shipped, explorer plan, platform-spec — all safe-direction). **CS-131 — Low:**
  `lint-docs.sh` config round-trip regex `[a-z_]+` silently skips digit-bearing tags
  (`s3_*`,`sep10`,`sep41`).
- *Cleared (strong):* **ADR-0034 no retention drift; ADR-0015 closed-bucket enforced;
  ADR-0031/0032 one-writer lockstep; ingest-pipeline + supply-pipeline docs accurate;
  metrics (80=80) + API (byte-identical) + config (153 tags) reference all IN SYNC;
  ADR index self-consistent.** Root causes: summary docs lag authoritative sources;
  copy-paste k8s-era runbook hazards; CI guards with blind spots (`.Handle(`,
  digit-tags, frontmatter-less docs) give false green.

## ✅ EXECUTION COMPLETE — all 34 areas + cross-cutting + Audit-2 executed. Roll-up in [03-synthesis.md](03-synthesis.md).
