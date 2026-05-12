# Adversarial Attack Tree

This document enumerates the abuse vectors that the auditor must
test against. Each leaf node is a concrete attack with:

- a target component
- a precondition
- the expected attacker outcome on success
- the existing defence (if any) and where to find it
- evidence / disposition after testing

For every leaf, after testing record:

- `tested:` yes/no
- `result:` defended / partial-defence / undefended
- `evidence:` `EV-####` ref
- `finding:` `F-####` ref if undefended or partial

## A. Data integrity attacks

### A1. Hostile XDR payloads

- **A1.1** SwapEvent with i128 amounts at `int64.Max + 1`.
  Defence: ADR-0003 (NUMERIC end-to-end). Test: malformed
  fixture in `test/fixtures/`.
- **A1.2** SyncEvent missing after SwapEvent.
  Defence: Soroswap decoder pairing logic. Test: drop fixture.
- **A1.3** Phoenix swap with 7 of 8 events.
  Defence: Phoenix grouping by `(ledger, tx_hash, op_index)`.
- **A1.4** Comet event with topic looking like Comet but
  emitted from a non-Blend pool contract.
  Defence: documented in CLAUDE.md; downstream filter on
  `Trade.Source = "comet"` + contract-address context.
- **A1.5** SEP-41 transfer body that's a map (`{amount, to_muxed_id}`)
  vs raw i128.
  Defence: type-test before MustI128 (CLAUDE.md surprise).
- **A1.6** Reflector update event with malformed scvec.
  Defence: scval helper bounds checks.
- **A1.7** Redstone WritePrices where `len(updated_feeds) != len(feed_ids)`.
  Defence: `ErrFeedIDCountMismatch` skips event.
- **A1.8** Band relay() args missing or shape-changed.
  Defence: ContractCallDecoder strict typing.
- **A1.9** WASM upgrade mid-backfill changes event schema
  (decoder reads field by name, then field is renamed).
  Defence: per-WASM-hash audit gates `BackfillSafe`.
- **A1.10** CAP-67 unified event with malformed `sep0011_asset` topic.
- **A1.11** Account entry mutation that flips trustline + adds
  claimable simultaneously across 3 ledgers (race in observers).

### A2. Hostile vendor responses

- **A2.1** Binance returns wrong-pair price with right symbol
  (e.g. XLMUSDT response for ETHUSDT request).
  Defence: pair check in parse.go.
- **A2.2** Coinbase returns 200 with empty body.
  Defence: parse.go null check.
- **A2.3** Coingecko returns price with `last_updated` timestamp
  in the future (clock skew or attacker).
  Defence: clock-skew guard rail.
- **A2.4** ECB returns FX with valid-looking but wrong currency
  code (USD shown as EUR).
- **A2.5** Vendor 5xx storm — overwhelms aggregator with retries.
  Defence: backoff + circuit breaker.
- **A2.6** Vendor returns 429 indefinitely.
  Defence: source disabled, divergence_warning fires.
- **A2.7** Frankfurter returns historical FX with wrong base.
- **A2.8** Polygon Forex auth-key rotated and old key still in
  Vault — silent failure.
- **A2.9** Reflector oracle contract upgraded with new field at
  same key — auditor never noticed.

### A3. Triangulation poisoning

- **A3.1** USDT depeg: stablecoin late-binding hides the depeg
  in `XLM/USD` because `USDT → USD` mapping fires unconditionally.
  Defence: divergence_warning must fire (per ADR-0026 reading).
- **A3.2** XLM/EUR triangulated via XLM/USD * USD/EUR; if
  USD/EUR vendor goes silent, fallback to ECB? FX snap?
  Defence: aggregator FX snap fallback rules.
- **A3.3** PYUSD/USD triangulated route used to compute fiat
  market cap when PYUSD itself depegs.

## B. API surface attacks

### B1. Authentication / authorization

- **B1.1** API-key enumeration via timing on
  `internal/auth/apikey_redis` lookup.
  Defence: constant-time comparison.
- **B1.2** SEP-10 challenge replay (same challenge tx submitted
  twice).
  Defence: nonce / one-time-use.
- **B1.3** SEP-10 challenge audience mismatch (user signs for
  "different.host").
  Defence: audience check.
- **B1.4** JWT issued by SEP-10 with no expiry / extreme expiry.
  Defence: expiry enforcement.
- **B1.5** API key revoked but still in Redis cache for TTL
  window — privilege use after revoke.
  Defence: revoke must invalidate Redis cache.
- **B1.6** Dashboard auth bypass (admin surface security).
- **B1.7** API key shared across regions — does revoke propagate?
- **B1.8** SEP-10 verify accepts a transaction signed for
  a different network (mainnet vs testnet passphrase).

### B2. Rate-limit bypass

- **B2.1** X-Forwarded-For spoofing when caller is direct
  (Caddy not in path).
  Defence: trusted-proxy list (ADR-0025).
- **B2.2** Rate-limit identity falls back to IP for
  unauthenticated traffic; user behind a CGNAT shares the
  bucket with thousands of others.
  Defence: per-key precedence + sane IP-fallback budget.
- **B2.3** Burst-then-idle pattern below detection threshold.
- **B2.4** Free-tier key cycling (sign up, abuse, sign up again).
  Defence: signup tracker + email/phone gating.

### B3. Cache poisoning / cache abuse

- **B3.1** Vary header missing → cross-key cache pollution at
  Cloudflare.
- **B3.2** Prewarm fills key with wrong args; handler reads
  with different args → permanent cache miss + DB load
  (the historical drift lesson).
- **B3.3** SEP-1 fetch with malicious `home_domain` → upstream
  HTTP fetch from arbitrary domain → SSRF.
  Defence: SSRF guard (allow-listed schemes, no internal IP
  ranges, timeout, body size cap).
- **B3.4** Asset ID with extreme length → cache key length
  blowup → memory pressure.

### B4. Pagination / cursor abuse

- **B4.1** Crafted cursor that bypasses bounds → DB scan.
- **B4.2** Cursor with malformed encoding → 500 instead of 400.
- **B4.3** Cursor stable across inserts? An attacker that
  observes pagination during high-throughput write may
  skip rows.

### B5. Streaming / SSE attacks

- **B5.1** Slow consumer hold-open of SSE → memory growth.
  Defence: ring + slow-consumer disconnect.
- **B5.2** Reconnect-with-stale-Last-Event-ID → backfill avalanche.
- **B5.3** SSE subscription to a pair with no traffic → permanent
  open connection consumes a goroutine.

### B6. Removed-route abuse

- **B6.1** `/v1/coins/*` returns 404 on R1 today, but live access
  log shows traffic — does the 404 path do an expensive lookup
  before responding? Live log shows ~300ms — investigate.
- **B6.2** `/v1/currencies/*` same as B6.1.

## C. Infrastructure attacks

### C1. Storage layer

- **C1.1** Timescale primary down — does API serve from cache or
  return 503? Runbook completeness.
- **C1.2** Timescale read replica lag — which read goes to which?
  ADR-0006/0008 answer; verify code wires it correctly.
- **C1.3** Continuous aggregate refresh stuck → stale
  `prices_1m` → wrong `/v1/markets`.
  Defence: cagg-stale alert + runbook.
- **C1.4** MinIO disk full — Galexie blocks → ingest stops.
  Live R1 shows MinIO at 78% disk — alert headroom?
- **C1.5** Galexie writer stalls but consumer doesn't notice
  (silent old data).
- **C1.6** Postgres connection pool exhaustion → all reads 5xx.
  Defence: pg-conns-saturated alert.

### C2. Cache layer

- **C2.1** Redis master down — Sentinel failover (ADR-0024)
  takes how long? API behavior during failover?
- **C2.2** Redis OOM → eviction storm.
- **C2.3** Redis disk full → write blocked → API errors.
  Runbook exists.
- **C2.4** Redis replication lag during failover → split-brain
  reads.

### C3. Network layer

- **C3.1** Cloudflare cache poisoned with stale data while origin
  is healthy.
- **C3.2** Caddy → API hop fails open / closed?
- **C3.3** Cloudflare WAF false-positive blocking
  legitimate clients.
- **C3.4** Cloudflare-to-origin certificate pinning bypass.
- **C3.5** TLS expiry on Caddy Let's Encrypt cert.

### C4. Galexie / MinIO chain

- **C4.1** Upstream history-archive corruption silently
  accepted by Galexie.
  Defence: hashdb drift detector (ADR-0017).
- **C4.2** MinIO bucket policy too permissive (object listable
  by anonymous).
- **C4.3** Bucket replication mid-write (multi-process write
  warning per Galexie docstring + ADR-0002).

### C5. Observability blind spots

- **C5.1** Loki down — who notices?
- **C5.2** Promtail down on R1 → no logs.
- **C5.3** Prometheus WAL corruption → metric history loss.
- **C5.4** Alertmanager misconfigured → alerts go to /dev/null.
  Runbook: alertmanager-bad-config.
- **C5.5** Healthchecks.io ping URL leaked in repo or logs.

## D. Supply-chain attacks

### D1. Dependency chain

- **D1.1** Upstream Go module compromised between releases
  (post-archive of stellar/go monorepo).
- **D1.2** GitHub Action pinned by major version; transitive
  Action compromise (e.g. action-x@v9 published a malicious
  v9.99.99).
  Defence: pin by SHA?
- **D1.3** Docker base image pulled by tag (`golang:1.x`) →
  sudden new behavior on image rebuild.
- **D1.4** pnpm lockfile unstable across CI runs.

### D2. Internal supply chain

- **D2.1** `.discovery-repos/*` checkout accidentally imported.
  Defence: lint-imports.sh boundary + W04 grep.
- **D2.2** Pre-built binary at repo root committed; reviewer
  trusts it.
- **D2.3** GHCR image signing not enforced; pulled image
  could be substituted.

## E. Operator / human attacks

### E1. Privileged commands

- **E1.1** `cmd/ratesengine-ops mint-key` invoked maliciously.
  Defence: who can SSH to R1; audit log.
- **E1.2** `cmd/ratesengine-ops upgrade-key` privilege change
  not audit-logged.
- **E1.3** `cmd/ratesengine-ops backfill` against arbitrary
  range — denial-of-service via huge backfill.
- **E1.4** Direct DB access via `psql` outside ops binary.

### E2. Release path

- **E2.1** `release.yml` trigger — who can create tags?
  Branch protection on `main`?
- **E2.2** GHCR push credentials in workflow secret.
- **E2.3** Deploy workflow with unverified binary (skip
  SHA256SUMS).
- **E2.4** Rollback workflow disables monitoring while running.

### E3. Public-flip path

- **E3.1** DNS flip without smoke test passing.
- **E3.2** Monitoring deactivated during flip.
- **E3.3** Cloudflare cache flush during flip → origin overload.

## F. Brand / customer attacks

### F1. Wrong-data serving

- **F1.1** Asset detail returns wrong issuer for verified slug
  (catalogue collision).
- **F1.2** SEP-1 overlay returns attacker-controlled fields if
  upstream toml is malicious.
- **F1.3** Chart endpoint returns price spikes from outlier
  trades (outlier filter bypass).

### F2. Reputational

- **F2.1** Status page reports green during real outage.
  Defence: status page should not derive from the same
  unhealthy infrastructure.
- **F2.2** Public blog post claim diverges from shipped
  behavior.

### F3. Privacy

- **F3.1** Logs include API key on auth failure.
  Defence: log-field allowlist; redact secrets.
- **F3.2** Webhook payload leaks PII.
- **F3.3** Audit-log accessible without admin auth.

## G. Compliance / legal

- **G1.1** Paid CMC feed redistributed without licence.
- **G1.2** Cookie consent missing on explorer.
- **G1.3** GDPR data export / deletion path absent.
- **G1.4** Customer-data residency not declared.

## H. Time-based attacks

- **H1.1** Server clock skew → wrong bucket boundary.
- **H1.2** Bucket-rollover edge case — SSE event for the closed
  bucket arrives after handler has computed envelope.
- **H1.3** Daylight-savings transition mishandling for fiat
  market hours.

## Outcome Tracking

| Vector | Tested | Result | Evidence | Finding |
| --- | --- | --- | --- | --- |
| _populated as the auditor walks the tree_ | | | | |

Each `undefended` or `partial-defence` row triggers a finding.
