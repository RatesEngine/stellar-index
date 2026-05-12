# Mandatory Journeys

Every journey is traced end-to-end from input to output, with code
and evidence references at each hop. Trace logs go in
[journeys-traces/](journeys-traces/) using
[journeys-traces/_template.md](journeys-traces/_template.md).

The journey set is intentionally granular (one per source, one
per oracle, one per external venue, one per page family) so each
trace is unambiguously closeable.

## Conventions

Each journey's trace must record:

- **Inputs.** What enters the system (XDR ledger, HTTP request,
  cron tick, vendor REST response).
- **Hops.** Every package boundary the data crosses, with file:line
  references.
- **Sinks.** Where data finally lands (DB row, Redis key, HTTP
  response, log line, metric, alert).
- **Failure modes.** What happens at each hop on bad input or
  upstream failure.
- **Tests.** Which test files exercise this journey end-to-end.
- **Live R1 evidence.** Where applicable, capture a live trace of
  the journey on R1.

## J01. On-Chain Trade Ingest (Soroswap)

Galexie writes LedgerCloseMeta to MinIO →
`internal/ledgerstream` reads → `internal/dispatcher` matches
event topic → `internal/sources/soroswap.Decode` extracts
`SwapEvent`+ following `SyncEvent` → `internal/pipeline.Sink`
writes Trade → `internal/storage/timescale/trades.go` inserts
into `trades_*` hypertable → continuous aggregate refresh →
served via API J05.

Validate: SwapEvent without paired SyncEvent (skip + warn?
panic? silently lose reserves?).

## J02. On-Chain Trade Ingest (Phoenix 8-event grouping)

Galexie → ledgerstream → dispatcher → Phoenix decoder groups all
8 per-field events by `(ledger, tx_hash, op_index)` → emits one
Trade per swap → sink → trades hypertable.

Validate: 7-of-8 events arriving (truncated tx). Validate:
out-of-order arrival within ledger.

## J03. On-Chain Trade Ingest (SDEX classic)

Galexie → ledgerstream → dispatcher → `internal/sources/sdex`
decodes classic operations + effects → emits Trade(s) → sink →
trades hypertable. Validate post-CAP-67 unified events vs
pre-Whisk operations+effects path (CLAUDE.md surprise list).

## J04. Oracle Observation Ingest (Reflector / Redstone / Band)

For each oracle:

- **Reflector** (3 contracts: DEX/CEX/FX): decoder → oracle
  observation table → divergence/aggregator references
- **Redstone Adapter**: `WritePrices` event grouped with op_args
  for feed_id mapping → oracle table → consumer
- **Band**: Soroban contract emits *zero events*. Dispatcher's
  ContractCallDecoder observes `relay()` / `force_relay()`
  invoke ops → decoder reads op args → oracle table.

Validate: feed_id-count mismatch in Redstone (`ErrFeedIDCountMismatch`
skips the whole event). Validate: Band invoke arg drift.

## J05. Closed-Bucket Price Path (`/v1/price`)

Client GET `/v1/price?asset=...&quote=...` → `cmd/ratesengine-api`
→ middleware → `internal/api/v1/price.go` → reads last-closed
bucket from Timescale via `internal/storage/timescale/aggregates.go`
→ Redis cache lookup via `internal/cachekeys` → builds envelope
with confidence (W10), divergence_warning (J19), triangulation
flag (J06), freeze flag → returns.

Verify ADR-0015 contract: serves the last *closed* bucket only,
not the in-progress one. Verify error envelope when asset is
unknown. Live R1 shows `/v1/price` returning 404 in 300ms — trace
the slow path.

## J06. Triangulation Path

`/v1/price?asset=XLM&quote=GBP` where no direct XLM/GBP feed
exists → triangulate via XLM/USD * USD/GBP →
`internal/aggregate/triangulate.go` → response carries
`triangulated: true`. Validate transitive divergence + provenance.

## J07. Stablecoin Fiat-Proxy Path

Trade ingested as `XLM/USDT` → aggregator maps `USDT → USD` per
ADR-0026 (late binding) → `/v1/price?asset=XLM&quote=USD`
serves the result.

Validate during simulated USDT depeg: late binding does NOT
hide the depeg — divergence_warning fires.

## J08. Chart Path (`/v1/chart`)

Client → `internal/api/v1/chart.go` → reads continuous aggregate
(`prices_1m`, `prices_5m`, `prices_1h`, etc.) → enforces ADR-0020
chart contract → returns OHLC array.

Recent feature (rc.46): `?price_type=market_cap` for fiat — extra
trace required.

## J09. History / Since-Inception Path (`/v1/history/since-inception`)

Cursor pagination per ADR-0018; deep history sourced from
backfill-derived hypertable rows. Validate cursor stability
across new inserts.

## J10. Tip / Latest Observation Path (`/v1/ticker`, `/v1/trades`, `/v1/markets`)

`/v1/markets` reads `prices_1m` per recent rc.45 fix (was scanning
41M trades). Trace the join, the cache key, and the prewarm path.

## J11. Asset Detail / Metadata Path (`/v1/assets/{slug-or-id}`)

Two wire shapes via the same route per CLAUDE.md surprise list:

- slug → `GlobalAssetView` (cross-chain identity + networks[])
- canonical asset_id → `AssetDetail` (per-Stellar-asset detail)

Trace the routing decision (catalogue lookup before parse). Trace
the SEP-1 overlay (`internal/metadata`) on slow upstream domains.
Trace the F2 enrichment fields (Freighter F2 set).

## J12. SSE Streaming Path (`/v1/price/stream`)

Client GET `/v1/price/stream` (SSE) → `internal/api/streaming/handler`
→ subscribes to hub → ring-buffer drain → events ride from
`internal/api/streampublish` (publisher) → Redis pub/sub →
streaming hub → SSE write. Validate slow-consumer disconnect
+ catch-up semantics + reconnect Last-Event-ID.

## J13. API Key Auth Path

Client request with `X-API-Key: ...` → middleware →
`internal/auth/apikey_redis` (hot cache) → fallback
`internal/auth/apikey_postgres` → identity → rate limit →
authorized handler. Validate revocation (key disabled mid-flight).
Validate billing increment (`internal/usage/counter`).

## J14. SEP-10 Auth Path

Client GET `/v1/auth/sep10/challenge` → returns Stellar
transaction → client signs → POST `/v1/auth/sep10/verify` →
JWT issued → JWT-bearing requests → expiry/refresh.
`internal/auth/sep10/`. Validate: replay-attack defence on
challenge, audience check, expiry enforcement.

## J15. Supply Snapshot Path (XLM)

`cmd/ratesengine-aggregator` cron tick → XLM supply via
algorithm 1 (`internal/supply/xlm`) → snapshot write to
`asset_supply_history` (migration 0005) → `/v1/assets/native`
read.

Also: `cmd/ratesengine-ops supply snapshot` operator path.
Validate: prior-finding text drift in CLI help.

## J16. Supply Snapshot Path (Classic)

Account-entry observer (ADR-0021) → trustline observer →
claimable-balance observer → LP-reserve observer →
SAC-balance observer → tables 0010..0014 → algorithm 2
classic supply derivation (`internal/supply/classic`) → snapshot
write → asset detail read.

## J17. Supply Snapshot Path (SEP-41)

SEP-41 mint/burn/clawback decoder
(`internal/sources/sep41_supply`, ADR-0023) → table 0015 →
algorithm 3 (`internal/supply/sep41`) → snapshot write → read.

## J18. Archive Completeness / Verify-Archive Path

Daily `verify-archive-tier-a.timer` (systemd) → `cmd/ratesengine-ops
verify-archive` → `internal/archivecompleteness` → tier A
(self), tier B (against galexie), tier D (cross-region) →
metrics emission → alert rule `archive-completeness` → runbook.

## J19. Divergence Cross-Check Path

Aggregator tick → `internal/divergence/worker.go` → fetch
CoinGecko + (optionally) Chainlink references → compare.go
diff → if material, write to `divergence_observations`
(migration 0019) → `divergence_warning` flag flips on price
envelope. Trace: reference offline → no false positive.

## J20. Cross-Region Determinism Path

`scripts/dev/verify-cross-region.sh` (or `cmd/ratesengine-ops
cross-region-check`) → fetch same closed-bucket from R1, R2,
R3 → assert byte-identical or within tolerance → mismatch
classification → metric emission. Today: only R1 exists; R2/R3
endpoints are docs-only — verify the tool reports that
gracefully.

## J21. SLA Probe Path

`sla-probe.timer` → `cmd/ratesengine-sla-probe` → fetches
known endpoints with measured latency + freshness → writes
`/var/lib/node_exporter/textfile/sla.prom` → node_exporter
exposes → Prometheus scrape → `sla-probe.yml` rules → alerts.

Validate: rules in `deploy/monitoring/rules/sla-probe.yml`
reference metric names actually emitted by the probe.

## J22. R1 Smoke Test Path

`ratesengine-smoke.timer` → `configs/healthchecks/smoke.sh` →
`scripts/dev/r1-smoke.sh` (note: live R1 reports the
`/opt/ratesengine/scripts/dev/r1-smoke.sh` path missing — file
this gap) → 13 GETs across health/catalogue/pricing/diagnostics
→ exit code = number of failures → Healthchecks.io ping.

## J23. Webhook / Notification Path

Incident or threshold trip → `internal/notify/sender.go` (Resend
+ noop adapters) → templates → outgoing email. Validate: no
PII in templates, retry policy, rate-limit on outgoing.

## J25. Aquarius decode

Galexie → ledgerstream → dispatcher → `internal/sources/aquarius`
decoder → Trade emission → sink → trades hypertable.

## J26. Comet decode (shared `("POOL", *)` topic)

Galexie → ledgerstream → dispatcher → `internal/sources/comet`
decoder. Verify: the decoder dispatches on topic[0] symbol, not
contract address; downstream filter by `Trade.Source = "comet"` +
contract context discriminates Blend backstop from arbitrary
Comet redeploys.

## J27. Blend auction path

Blend pool event → `internal/sources/blend` decoder → auction
record → `blend_auctions` table (migration 0009) → consumer
(operator/explorer) or documented non-consumer.

## J28. Liquidity-pool reserve observer

LP-reserve change → `internal/sources/liquidity_pools` →
`lp_reserve_observations` (migration 0013) → classic supply
algorithm 2 reader.

## J29. SAC balance observer (CLAUDE.md SAC wrappers + USD volume)

SAC mutation → `internal/sources/sac_balances` →
`sac_balance_observations` (migration 0014) → SAC vs classic
double-count guard at supply read time.

## J30. SEP-41 mint/burn/clawback ingest

SEP-41 contract event → `internal/sources/sep41_supply` →
`sep41_supply_events` (migration 0015) → algorithm 3 supply
read.

## J31. Trustline observer

Trustline create/modify/remove → `internal/sources/trustlines`
→ `trustline_observations` (migration 0011) → algorithm 2
classic supply.

## J32. Account entry observer (ADR-0021)

AccountEntry mutation → `internal/sources/accounts` →
`account_observations` (migration 0010) → asset-side
supply derivation.

## J33. Claimable balance observer

Claimable balance create/claim → `internal/sources/claimable_balances`
→ `claimable_observations` (migration 0012) → algorithm 2
read counts these toward circulating? Verify policy.

## J34. Reflector DEX contract observation

Reflector DEX contract event → decoder → oracle table →
divergence reference + `/v1/oracle/sep40` consumer.

## J35. Reflector CEX contract observation

Same as J34 but for the CEX-pricing contract.

## J36. Reflector FX contract observation

Same as J34 but for the FX-pricing contract. Local TWAP
computation (no on-chain `twap` per CLAUDE.md surprise).

## J37. Redstone WritePrices ingest

Redstone Adapter `WritePrices` event (topic `"REDSTONE"`) +
op-args feed_id mapping → decoder → oracle table. Trace
`ErrFeedIDCountMismatch` skip.

## J38. Band relay() observation (zero-event source)

Band `relay()` / `force_relay()` invoke op → dispatcher
ContractCallDecoder → `internal/sources/band` → oracle table.

## J39. Frankfurter / ECB FX poll

Cron tick → `internal/sources/frankfurter` (or
`internal/sources/external/ecb`) → normalised FX → `fx_quotes`
hypertable (migration 0028) → `/v1/chart` fiat:fiat reader.

## J40. Binance external venue

`internal/sources/external/binance` REST + WS streamer →
normalised Trade (10^8 amount scale) → trades hypertable →
aggregator class=ClassExchange contribution.

## J41. Bitstamp external venue

Same as J40 but Bitstamp adapter.

## J42. Coinbase external venue

Same as J40 but Coinbase adapter.

## J43. Kraken external venue

Same as J40 but Kraken adapter.

## J44. Cryptocompare reference (aggregator class)

`internal/sources/external/cryptocompare` →
`ClassAggregator` → divergence-only reference (NOT VWAP).

## J45. CoinGecko reference

`internal/sources/external/coingecko` poller → divergence
worker reference.

## J46. CoinMarketCap reference

Same as J45 but CMC adapter.

## J47. Polygon Forex (paid)

`internal/sources/external/polygonforex` → FX feeder.

## J48. ExchangeRatesAPI (paid)

`internal/sources/external/exchangeratesapi` → FX feeder.

## J49. External-source outage path

Vendor 5xx storm / 429 / timeout / schema drift →
adapter retry/backoff → circuit breaker → metric
`external_poller_stale` → alert → runbook.

## J50. Explorer asset page (`/assets/[slug]`)

Browser request → static-export Next page → API client → 
`/v1/assets/{slug}` → either GlobalAssetView (slug) or
AssetDetail (asset_id) → render → SEO metadata.

## J51. Explorer markets page

Browser → markets list page → `/v1/markets` (reads `prices_1m`
post rc.45) → table render → cursor pagination.

## J52. Explorer issuer page

Browser → `/issuers/[g_strkey]` → issuer + assets aggregation →
sum 24h USD volume across assets.

## J53. Dashboard usage page

Authenticated browser → `/api/usage` → `internal/usage/counter`
→ usage display + billing context.

## J54. Status page incident flow

Markdown file in `docs/operations/incidents/` →
`internal/incidents` parser → `/v1/incidents` → status page
fetch → render incidents list.

## J55. OpenAPI generation drift

`make docs-api` → regenerates `openapi/rates-engine.v1.yaml`
or `docs/reference/api/*` → diff vs checked-in. Non-empty
diff = stale at audit time.

## J56. Postman collection generation

`make docs-postman` → `examples/postman/rates-engine.postman_collection.json`
diff. Non-empty = stale.

## J57. Migration up/down (one trace per migration)

`cmd/ratesengine-migrate up` → applies all 28 migrations →
captures schema → `migrate down` reverses → idempotency proof.

## J58. Deploy workflow

`gh workflow run deploy.yml -f region=r1 -f version=v…` →
Ansible playbook → SSH → stage → backup → install →
restart → health probe → automatic rollback on failure.

## J59. SAC vs classic supply double-count guard

For `USDC` (which has SAC + classic representations):
classic supply read + SAC supply read; verify the
deduplication at read time, not at write time.

## J60. Decoder stats + change summary

dispatcher → `internal/dispatcher/statsflush` → `decoder_stats_5m`
table (0020); aggregator change-summary worker → `change_summary_5m`
(0022) → consumer (alerts? API? operator?).

## J61. Frontend stale-static drift

Explorer built before an API schema change → static export
contains old shape → user sees broken render. Verify build
guard (typecheck + build catches drift).

## J62. CI false-positive scenario

Skipped test, build-tag missing, env-required test → CI green
but coverage hole. Trace: `t.Skip` greps + build-tag inventory.

## J63. Runbook command mismatch

Alert fires → operator runs runbook command → command
references unit name / port / env var that no longer exists.
Sample-test 5 random runbooks against current code/config.

## J64. Replay / duplicate ledger data

Indexer reprocesses ledger range → cursor + idempotent write →
no double-counted volume in `trades` or aggregates.

## J65. Bucket-boundary race

SSE event for closed bucket arrives after handler computed
envelope flags → next-bucket read or current-bucket extension?
Verify ADR-0015 contract under concurrent timing.

## J66. Webhook outgoing (re-numbered from old J23)

Threshold trip → `internal/notify/sender` → outgoing email/webhook
→ retry policy → no PII in body → rate limit.

## J24. Hostile / Adversarial Paths

For each adversarial vector in [10-attack-tree.md](10-attack-tree.md),
trace what happens end-to-end and capture in evidence:

- malformed event payloads (i128 overflow, nested map abuse)
- unsupported assets (collision with verified slug)
- stale cache or Redis miss avalanche
- missing trusted proxy header (X-Forwarded-For spoofing)
- empty divergence references
- missing supply watcher config
- partial infra outage (Redis down, MinIO down, Timescale
  read-replica lag, Cloudflare cache poisoning)
- vendor 5xx storm
- API-key enumeration
- SEP-10 challenge replay
- WASM upgrade mid-backfill
- log-injection via user-controlled fields
- prometheus rule recursion / runaway queries
- billing meter bypass (free tier abuse, usage rollover)

## Operator Subset

Operator-driven journeys live above as J11 (asset detail is
operator-relevant), J15-J18 (supply + archive), J20-J22
(cross-region + SLA + smoke).

## R1 Live Evidence Requirement

For every journey marked **served live on R1** (J05, J08-J12,
J13, J14, J15, J18, J20, J21, J22), capture at least one live
trace transcript via SSH probe, recorded in
`evidence/r1-probes/<journey-id>-<YYYYMMDD>.md`.

## Trace Storage

Each journey trace file: `journeys-traces/J##-<short-name>.md`,
populated from [journeys-traces/_template.md](journeys-traces/_template.md).
