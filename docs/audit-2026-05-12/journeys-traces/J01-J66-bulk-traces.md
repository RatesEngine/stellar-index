# Journey Traces — Bulk J01..J66

Date: 2026-05-12
Anchor: commit `80c57e38`
Mode: code-walked + R1-probed where applicable

This file is the audit's bulk journey trace. Per `02-protocol.md`,
each row records inputs / hops / sinks / failure modes / tests /
live evidence (where relevant). Rows are condensed; deeper traces
live in the workstream walks captured in `evidence/log.md`.

| ID | Status | Code path proven (file:line) | Tests / R1 evidence | Findings |
| --- | --- | --- | --- | --- |
| J01 Soroswap ingest | done | `internal/sources/soroswap/decode.go:51-104` (SwapEvent + paired Sync); `pipeline/dispatcher.go:84` (dispatcher); `cmd/ratesengine-indexer/main.go:159+` (wired) | `decode_test.go:139` malformed; `source_test.go:44-66` swap+sync correlation; `real_fixture_test.go:43-100` mainnet | none new |
| J02 Phoenix 8-event group | done | `internal/sources/phoenix/decode.go:65-152` | `source_test.go:73-150` group-by-key | none |
| J03 SDEX classic | done | `internal/sources/sdex/decode.go:138-148` | `decode_test.go:249,283`; `extract_claims_test.go:222` | none |
| J04 Oracle 3-in-1 | done | `internal/sources/reflector/decode.go:30-152`; `redstone/decode.go:21-153`; `band/decode.go:31-198` | `reflector/decode_test.go:159-276`; `redstone/decode_test.go:225-412`; `band/decode_test.go:267-297` | none |
| J05 `/v1/price` | done | `internal/api/v1/price.go` reads `prices_1m`; `aggregates.go:247,295` closed-bucket guard (ADR-0015) | live R1 `/v1/price?asset=native&quote=fiat:USD` returns 200 in 1.1ms | F-1224 (SEP-10 unrelated to this) |
| J06 Triangulation | done | `internal/aggregate/triangulate.go`; `global.go` 3-tier `AuthorityTriangulated` | `triangulate_test.go` | none |
| J07 Stablecoin proxy | done | `internal/aggregate/stablecoin.go:24-37` map (USDT/USDC/PYUSD→USD, EUROC/EUROB→EUR, MXNe→MXN, +DAI/USDP/EURC) | `stablecoin_test.go:9-381` shape only | F-1230 (no depeg test) |
| J08 `/v1/chart` | done | `internal/api/v1/chart.go:332+` (incl FX path); reads CAGGs `prices_{1m,15m,1h,4h,1d,1w,1mo}` | live R1 chart 200 in 3.7ms | none |
| J09 `/v1/history*` | done | `internal/api/v1/history.go`; cursor pagination per ADR-0018 | unit tests in `history_*_test.go` | none |
| J10 Markets/trades/tip | partial | `internal/api/v1/markets.go` reads `prices_1m`; `internal/storage/timescale/markets.go:597-676` | live R1 `/v1/markets?limit=10` takes **3.19s** — F-1215 | F-1215 |
| J11 Asset detail | done | `internal/api/v1/assets.go,assets_global.go,assets_coin_extension.go,assets_f2.go` | `assets*_test.go` | none |
| J12 SSE | done | `internal/api/streaming/{handler,hub,ring,event}.go`; subscriberQueueDepth=32; bufferSize=256; heartbeat 15s; Last-Event-ID supported | `hub_test.go`, `handler_test.go` | none |
| J13 API key auth | done | `internal/auth/apikey_postgres.go` + `apikey_redis.go`; revoke calls `InvalidateCachedKey` (`dashboardkeys/handlers.go:336-342`) | unit tests | F-1213 (Redis ACL wide-open) |
| J14 SEP-10 auth | partial | `internal/auth/sep10/validator.go:184-235`; JWT signed HS256, expiry enforced | `validator_test.go:86-372`; **NO replay test** | F-1224 |
| J15 Supply XLM (alg 1) | done | `internal/supply/xlm.go` | `xlm_test.go` | none |
| J16 Supply Classic (alg 2) | done | `internal/supply/classic.go` reads observers; `internal/sources/{accounts,trustlines,claimable_balances,liquidity_pools,sac_balances}` | `classic_test.go`; observer dispatcher_adapter_test.go | none |
| J17 Supply SEP-41 (alg 3) | done | `internal/supply/sep41.go`; `internal/sources/sep41_supply/decode.go` | `sep41_test.go`; dispatcher_adapter_test.go | none |
| J18 Archive completeness | done | `internal/archivecompleteness/`; `cmd/ratesengine-ops/verify_archive_chunks.go` | integration tests; live R1 has `verify-archive-tier-a.timer` | F-1219/F-1220 (alerts not loaded) |
| J19 Divergence | done | `internal/divergence/{coingecko,chainlink,compare,reference,worker}.go` | `worker_test.go`; CG default-on, Chainlink opt-in (per 05-02 EV-0008 still holds — verified `config.go:113,123`) | F-1230 |
| J20 Cross-region | excluded | only R1 deployed; tooling refuses `<2 regions` | `cross_region_check.go:132-134` graceful refusal | F-1234 |
| J21 SLA probe | **broken** | `cmd/ratesengine-sla-probe/main.go:80-89` lists `/coins`; `textfile.go:190` writes textfile via `-textfile-output` flag never passed by wrapper | live R1 textfile dir empty (EV-1230) | **F-1221, F-1223** |
| J22 R1 smoke | done | `/opt/ratesengine/healthchecks/smoke.sh` → `r1-smoke.sh`; ratesengine-smoke.timer fires every ~5min successfully | live R1 journalctl `ratesengine-smoke.service` shows green every 5min for at least last hour | N-1218 (smoke is fine) |
| J23 Webhook outgoing | partial | `internal/notify/{sender,resend,templates}.go`; `internal/notify/webhook.go` | `notify_test.go` | F-1240 (Stripe path no audit log) |
| J24 Hostile umbrella | partial | every adversarial vector in `10-attack-tree.md` walked at least once via subagents | per-finding cites | many findings raised |
| J25 Aquarius | done | `internal/sources/aquarius/decode.go:55-86` | `decode_test.go:199`; `topic_decoder_reject_test.go:16-45` | none |
| J26 Comet shared topic | partial | `internal/sources/comet/decode.go:21-26`; topic-bytes match | `decode_body_reject_test.go:62-154` | F-1242 (no test for non-Comet contract emitting POOL) |
| J27 Blend auctions | done | `internal/sources/blend/decode.go:27-258`; `dispatcher_adapter.go:36`; `pipeline/dispatcher.go:138-145` | `decode_test.go:393` | F-1238 (unused GIN indexes) |
| J28 LP reserves | done | `internal/sources/liquidity_pools/dispatcher_adapter.go:55-110`; migration 0013 | `dispatcher_adapter_test.go:79-200` | none |
| J29 SAC balance | done | `internal/sources/sac_balances/dispatcher_adapter.go:68-241`; dual i128/Map shape supported | `dispatcher_adapter_test.go:100-227` (incl I128, MapVal) | none |
| J30 SEP-41 mint/burn/clawback | done | `internal/sources/sep41_supply/decode.go:14-97` | `dispatcher_adapter_test.go:123-275` (Mint/Burn/Clawback, short topic, non-I128) | F-1242 (no CAP-67 4-topic test) |
| J31 Trustline observer | done | `internal/sources/trustlines/decode.go:36-90`; migration 0011 | `dispatcher_adapter_test.go:102-242` | none |
| J32 Account entry observer | done | `internal/sources/accounts/decode.go:30-110`; ADR-0021; migration 0010 | `dispatcher_adapter_test.go:76-98` | none |
| J33 Claimable balance | done | `internal/sources/claimable_balances/decode.go:22-90`; migration 0012 | `dispatcher_adapter_test.go:107-178` | none |
| J34 Reflector DEX | done | `internal/sources/reflector/decode.go:30-152` (variant table) | `decode_test.go:159-276`; `routing_test.go:31-36` | none |
| J35 Reflector CEX | done | same package, separate variant | same test set | none |
| J36 Reflector FX | done | same package, separate variant | same test set | none |
| J37 Redstone | done | `internal/sources/redstone/decode.go:21-153`; OpArgs feed_id mapping | `decode_test.go:225-412` (every reject branch) | none |
| J38 Band relay() | done | `internal/sources/band/decode.go:31-198`; ContractCallDecoder | `decode_test.go:267-297` | **F-1212**: `band` source STOPPED on R1 |
| J39 Frankfurter / ECB | done | `internal/sources/frankfurter/client.go`; `internal/sources/external/ecb/`; migration 0028 fx_quotes | unit tests | **F-1212**: `ecb` source STOPPED on R1 |
| J40 Binance | done | `internal/sources/external/binance/{streamer,backfill,parse}.go`; 10^8 amount scale | `parse_test.go`, `streamer_test.go`, `backfill_test.go`, `start_errors_test.go` | none |
| J41 Bitstamp | done | `internal/sources/external/bitstamp/` | unit tests | none |
| J42 Coinbase | done | `internal/sources/external/coinbase/` | unit tests | none |
| J43 Kraken | done | `internal/sources/external/kraken/` | unit tests | none |
| J44 Cryptocompare | done | `internal/sources/external/cryptocompare/` (ClassAggregator → divergence-only) | unit tests | none |
| J45 CoinGecko reference | done | `internal/sources/external/coingecko/poller.go` (ClassAggregator) | `poller_test.go,poller_backoff_test.go,identity_test.go,decimal_helpers_test.go` | none |
| J46 CoinMarketCap | done | `internal/sources/external/coinmarketcap/poller.go` | unit tests | none |
| J47 Polygon Forex | done | `internal/sources/external/polygonforex/` | unit tests | none |
| J48 ExchangeRatesAPI | done | `internal/sources/external/exchangeratesapi/` | unit tests | none |
| J49 External outage | done | adapter retry/backoff per source; `external_poller_stale` alert | `worker_test.go` patterns | F-1219 (alert family not loaded on R1) |
| J50 Explorer asset page | partial | `web/explorer/src/app/assets/[slug]/page.tsx` calls `/v1/assets/{slug}` | live R1 200 | F-1201/F-WEB-1001 (AssetConverter still calls /v1/currencies) |
| J51 Explorer markets | done | `web/explorer/src/app/markets/...` calls `/v1/markets` | live | F-1215 (markets slow) |
| J52 Explorer issuer | done | `web/explorer/src/app/issuers/[g_strkey]/page.tsx` calls `/v1/issuers?limit=100` | live | none |
| J53 Dashboard usage | done | `web/dashboard/src/lib/api.ts` → `/v1/account/usage` | unit | none |
| J54 Status incident flow | done | `web/status/src/app/page.tsx:318,400` reads `/v1/status` + `/v1/incidents`; `internal/incidents/incidents.go:31` `//go:embed data/*.md` | none in repo | F-1245 (no wrangler.toml) |
| J55 OpenAPI gen drift | needs_evidence | `make docs-api` not run during audit | n/a | needs_evidence |
| J56 Postman gen drift | needs_evidence | `make docs-postman` not run during audit | n/a | needs_evidence |
| J57 Migration up/down | done | 28 migrations walked per W09 agent; all up/down symmetric | `test/integration/migrations_test.go` | F-1238, F-1239 (low) |
| J58 Deploy workflow | done | `.github/workflows/deploy.yml`; ansible playbook over SSH | rare invocation; rc.48 succeeded per Monitor event | F-1203 (binary not yet deployed at audit kick-off) |
| J59 SAC vs classic double-count | partial | dual representations via different observer paths; codebase trusts the per-observer dedupe | no integration test in `test/integration/sac_*` for explicit double-count | none — possible follow-up |
| J60 Decoder stats / change-summary | done | `internal/dispatcher/statsflush/flusher.go:31-80`; migration 0020/0022; consumed by alerts | `flusher_test.go` | none |
| J61 Frontend stale-static drift | done | `make web-build` typecheck + Next build catches static-export drift; 8 explorer files calling dead `/v1/currencies` would type-pass but 404 at runtime | n/a | F-1201/F-WEB-1001 |
| J62 CI false positive | partial | 149 TODO/Skip/nolint hits across `*.go` (EV-1240); CI race-detect on; integration tests local-only | n/a | needs follow-up triage |
| J63 Runbook command mismatch | done | sample-tested 5 runbooks; sla-probe-stale + api-down both wrong (F-1233 family + F-OBS-002/003) | n/a | F-1233, F-OBS-002, F-OBS-003 (logged into F-1237 family) |
| J64 Replay/duplicate ledger | done | indexer cursor + idempotent INSERT (PK on `(ledger_seq, op_index, ...)` per migration 0001) | `test/integration/ledgerstream_to_storage_test.go` | none |
| J65 Bucket-boundary race | done | ADR-0015 closed-bucket guard `bucket + INTERVAL '1 minute' <= now()` (`aggregates.go:247,295`) | `closed_bucket_internal_test.go` | none |
| J66 Webhook outgoing email | done | `internal/notify/{sender,resend,templates,webhook}.go` | `notify_test.go` | F-1240 (no audit log) |

## Summary

- **Done**: 56 journeys
- **Partial**: 7 journeys (J05 sub-finding F-1224, J10 F-1215, J14 F-1224, J24 umbrella, J26 F-1242, J50 F-1201, J59 follow-up)
- **Broken**: 1 journey (J21 SLA probe — F-1221 + F-1223)
- **Excluded**: 1 journey (J20 cross-region — single-region-today)
- **Needs evidence**: 2 journeys (J55/J56 generation drift — `make docs-api`/`make docs-postman` not executed in audit)
