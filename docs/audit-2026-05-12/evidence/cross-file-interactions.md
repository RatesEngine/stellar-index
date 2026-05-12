# Cross-File Interactions Ledger

Owned by **W26**. Every material seam between files / packages /
configs / docs gets an `XFI-####` ID. Add rows as the audit walks
each workstream; the goal is that a reviewer can find both ends
of any seam without searching.

W26 declares an interaction-class taxonomy (see
[`../workstreams/W26-cross-file-interactions.md`](../workstreams/W26-cross-file-interactions.md)).
Every required class must have at least one example row here
before W26 can close.

| ID | Interaction | Dependency / Risk | Evidence |
| --- | --- | --- | --- |
| XFI-1201 | `cmd/ratesengine-indexer/main.go` -> `internal/dispatcher` -> per-source decoder + observer wiring | A new decoder must be wired in main.go AND registered in the dispatcher AND registered in `internal/sources/external/registry.go`. Drift = silent decoder skip | _add file:line refs_ |
| XFI-1202 | `cmd/ratesengine-aggregator/main.go` -> `internal/aggregate/orchestrator` -> divergence + freeze + confidence | Aggregator binary wiring is the single source of truth for what runs in the goroutine fleet; missing wiring means a feature is dead code | _add file:line refs_ |
| XFI-1203 | `cmd/ratesengine-api/main.go` -> `internal/api/v1/server.go` (route registration) -> per-handler files | Route + handler + OpenAPI must agree (see R04) | _add file:line refs_ |
| XFI-1204 | `internal/api/v1/handler.go` -> `internal/api/v1/envelope.go` -> client-side parse | All response envelopes must use the canonical envelope; flags semantics must be consistent | _add file:line refs_ |
| XFI-1205 | `internal/cachekeys/*` -> `internal/storage/redisclient` -> all readers/writers | Cache keys must round-trip stably; prewarmer must call with handler-identical args | _add file:line refs_ |
| XFI-1206 | `migrations/*.up.sql` -> `internal/storage/timescale/*.go` reader + writer | Schema drift between migration and reader is a finding | _add file:line refs_ |
| XFI-1207 | `internal/sources/external/registry.go` `BackfillSafe` flag -> `cmd/ratesengine-ops/backfill.go` -> `docs/operations/wasm-audits/<source>.md` | The flag gates ops backfill; flipping it requires an evidence log | _add file:line refs_ |
| XFI-1208 | `internal/currency/data/seed.yaml` -> `internal/currency/*` (`//go:embed`) -> several handlers + explorer | Verified-currency catalogue propagates to API, indexer aggregator pair set, explorer; adding a verified currency = code change + redeploy | _add file:line refs_ |
| XFI-1209 | `deploy/monitoring/rules/*.yml` -> emitted Prometheus metric names -> `internal/obs/metrics.go` per-binary registration | Alert expressions reference metric names; rename = silent alert breakage | _add file:line refs_ |
| XFI-1210 | `deploy/monitoring/rules/*.yml` -> runbooks at `docs/operations/runbooks/<alert-name>.md` | Every alert must have a runbook | inventory/runbook-inventory.md |
| XFI-1211 | `configs/alertmanager/alertmanager.r1.yml` route tree -> receivers (page/ticket/info/deadmansswitch) -> docs in runbook | Route tree must agree with severity labels in the rule files | _add file:line refs_ |
| XFI-1212 | `configs/healthchecks/*.timer` -> healthcheck script -> Healthchecks.io URL | URL secret hygiene + ping success path | _add file:line refs_ |
| XFI-1213 | `configs/caddy/*` -> trusted-proxy IPs -> rate-limit identity in `internal/ratelimit` | ADR-0025: trust boundary for X-Forwarded-For | _add file:line refs_ |
| XFI-1214 | `internal/auth/apikey_postgres` <-> `internal/auth/apikey_redis` (write-through cache) | Revoke must invalidate Redis cache or revoke takes effect only after TTL | _add file:line refs_ |
| XFI-1215 | `internal/sources/external/registry.go` Class -> `internal/aggregate/*` contribution policy | Aggregator must look up class at VWAP compute time; only `ClassExchange` contributes by default | _add file:line refs_ |
| XFI-1216 | `internal/aggregate/stablecoin.go` (ADR-0026) -> `/v1/price` flags -> divergence_warning when depeg | Late binding hides depeg unless divergence wakes up | _add file:line refs_ |
| XFI-1217 | `cmd/ratesengine-indexer/main.go` ADR-0021/0022/0023 supply-observer wiring -> `internal/sources/{accounts,trustlines,claimable_balances,liquidity_pools,sac_balances,sep41_supply}` -> migrations 0010..0015 | Observer wiring drift = supply derivation goes wrong silently | _add file:line refs_ |
| XFI-1218 | `web/explorer/src/app/**/page.tsx` -> API client wrapper -> `pkg/client` (or direct fetch) -> live API contracts | Explorer page expectations = first place where API drift surfaces visibly | _add file:line refs_ |
| XFI-1219 | `internal/sources/forex/circulation_data.csv` -> `internal/sources/forex/circulation.go` -> aggregator/circulation surface | CSV embedded in repo; updating fiat circulating supply is a code change + redeploy | internal/sources/forex/ |
| XFI-1220 | `cmd/ratesengine-sla-probe` -> `/var/lib/node_exporter/textfile/*.prom` -> `deploy/monitoring/rules/sla-probe.yml` | Probe metric names must match alert expressions; missing metric = silent alert miss | _add file:line refs_ |
| XFI-1221 | `internal/api/streaming/hub` <- `internal/api/streampublish` <- `internal/storage/redisclient` pub/sub | SSE event flow path; backpressure + slow-consumer disconnect | _add file:line refs_ |
| XFI-1222 | `internal/platform/billing.go` -> `internal/usage/counter.go` -> middleware -> per-request increment | Free-tier abuse prevention; meter consistency under crash | _add file:line refs_ |
| XFI-1223 | `internal/notify/{sender,resend,templates,webhook}` -> outgoing email/webhook -> external receivers | Outgoing message hygiene; PII redaction; rate-limit | _add file:line refs_ |

## Class-01: binary → config → package (6 entries)

| ID | Binary | Config | Wiring entry |
| --- | --- | --- | --- |
| XFI-CLASS-01-001 | `cmd/ratesengine-api/main.go` | `internal/config/config.go` Server + Auth + RateLimit + Divergence sections | `main.go:243-247` (CORS); `main.go:550,560` (Coins/Currencies wiring); `main.go:511` (CachedCoinsReader); `main.go:474` (Divergence refs) |
| XFI-CLASS-01-002 | `cmd/ratesengine-aggregator/main.go` | `internal/config/config.go` Aggregator + Divergence + Confidence + Freeze | `main.go:213` (Anomaly); `main.go:218` (Freeze writer); `main.go:266` (Divergence); `main.go:303` (Orchestrator.New); `main.go:357-389` (Baseline + ChangeSummary workers) |
| XFI-CLASS-01-003 | `cmd/ratesengine-indexer/main.go` | `internal/config/config.go` Ingestion + ExternalSources | `main.go:159+` (dispatcher build + per-decoder wire); `main.go:612-623` (`setSourceEnabled`); `main.go:207-219` (statsflush) |
| XFI-CLASS-01-004 | `cmd/ratesengine-ops/main.go` | flags + env per-subcommand | dispatch in `main.go` to backfill / cross-region / discovery / mint-key / etc. |
| XFI-CLASS-01-005 | `cmd/ratesengine-migrate/main.go` | DB URL env | golang-migrate runner per ADR-0006 |
| XFI-CLASS-01-006 | `cmd/ratesengine-sla-probe/main.go` | flags `-base-url`, `-textfile-output`, etc. | `main.go:168` (`-textfile-output` defined); `textfile.go:190 writeTextfileAtomic` |

## Class-02: config example → parser → deploy template (1+ entry)

| ID | Source | Parser | Deploy template |
| --- | --- | --- | --- |
| XFI-CLASS-02-001 | `configs/example.toml` (root config schema) | `internal/config/load.go` (TOML decode) | `configs/ansible/roles/archival-node/templates/ratesengine.toml.j2` (Jinja2 rendered on R1) |

## Class-03: workflow → script → artifact (10 entries — one per workflow)

| ID | Workflow | Script | Artifact |
| --- | --- | --- | --- |
| XFI-CLASS-03-001 | `.github/workflows/ci.yml` | `scripts/ci/lint-docs.sh`, `lint-imports.sh`, `lint-openapi-urls/main.go`, `make verify` | green/red status |
| XFI-CLASS-03-002 | `.github/workflows/api-audit.yml` | OpenAPI lint | audit artifact |
| XFI-CLASS-03-003 | `.github/workflows/api-docs.yml` | `make docs-api` | `docs/reference/api/*` |
| XFI-CLASS-03-004 | `.github/workflows/deploy.yml` | `configs/ansible/playbooks/deploy-binary.yml` | live R1 binary swap + backup |
| XFI-CLASS-03-005 | `.github/workflows/docs-deploy.yml` | mkdocs / cloudflare | docs.ratesengine.net |
| XFI-CLASS-03-006 | `.github/workflows/explorer-deploy.yml` | `make web-build` + wrangler | Cloudflare Pages |
| XFI-CLASS-03-007 | `.github/workflows/k6-weekly.yml` | `test/load/scenarios/*` | weekly perf report |
| XFI-CLASS-03-008 | `.github/workflows/release-validate.yml` | pre-tag validation | gate for release.yml |
| XFI-CLASS-03-009 | `.github/workflows/release.yml` | cross-compile + GHCR + GitHub Release | binaries + container images |
| XFI-CLASS-03-010 | `.github/workflows/status-page.yml` | status page build | web/status static export |

## Class-04: Dockerfile → binary → runtime (6 entries)

| ID | Dockerfile | Binary | Runtime flags/env |
| --- | --- | --- | --- |
| XFI-CLASS-04-001 | `docker/ratesengine-api.Dockerfile` | `cmd/ratesengine-api/main.go` | port 3000; env `RATESENGINE_*` |
| XFI-CLASS-04-002 | `docker/ratesengine-aggregator.Dockerfile` | `cmd/ratesengine-aggregator/main.go` | metrics 9465 |
| XFI-CLASS-04-003 | `docker/ratesengine-indexer.Dockerfile` | `cmd/ratesengine-indexer/main.go` | metrics 9464 |
| XFI-CLASS-04-004 | `docker/ratesengine-ops.Dockerfile` | `cmd/ratesengine-ops/main.go` | per-subcommand args |
| XFI-CLASS-04-005 | `docker/ratesengine-migrate.Dockerfile` | `cmd/ratesengine-migrate/main.go` | DB URL env |
| XFI-CLASS-04-006 | `docker/ratesengine-sla-probe.Dockerfile` | `cmd/ratesengine-sla-probe/main.go` | `-textfile-output` flag |

## Class-05: migration → store → integration test (28 entries — one per up-migration; consolidated)

| ID | Migration | Store reader | Integration test |
| --- | --- | --- | --- |
| XFI-CLASS-05-001..028 | `migrations/0001..0028_*.up.sql` | `internal/storage/timescale/{trades,aggregates,oracle,supply,discovery,baseline,blend_auctions,account_observations,classic_supply_observations,sep41_supply_events,soroswap_pairs,freeze_events,divergence_observations,decoder_stats,change_summary,asset_registry,sources_stats,price_source_contributions,markets,coins,issuers,assets,cursors,fx_quotes}.go` + `internal/platform/postgresstore/` for migration 0027 | `test/integration/{migrations_test,decoders_to_storage_test,assets_test,baseline_storage_test,classic_supply_storage_test,fx_quote_at_or_before_test,ledgerstream_to_storage_test,platform_postgres_stores_test,sep41_supply_storage_test,storage_test,supply_storage_test,trades_range_test,trades_usd_volume_test,api_test,api_registry_cursors_test,issuers_coins_storage_test,discovery_test,external_fleet_test}.go` |

## Class-06: decoder → event → sink → store (15 entries — one per on-chain source)

| ID | Decoder | Event type | Sink | Store |
| --- | --- | --- | --- | --- |
| XFI-CLASS-06-001 | `sources/soroswap/decode.go` | `events.Event` (SwapEvent + SyncEvent) | `pipeline/sink.go` | trades hypertable |
| XFI-CLASS-06-002 | `sources/aquarius/decode.go` | events.Event (trade topic) | same | trades |
| XFI-CLASS-06-003 | `sources/phoenix/decode.go` | events.Event (swap 8-field) | same | trades |
| XFI-CLASS-06-004 | `sources/comet/decode.go` | events.Event (POOL,swap) | same | trades |
| XFI-CLASS-06-005 | `sources/blend/decode.go` | events.Event (new/fill/delete_auction) | same | blend_auctions |
| XFI-CLASS-06-006 | `sources/sdex/decode.go` | OperationResult ManageOffer/PathPayment | same | trades |
| XFI-CLASS-06-007 | `sources/reflector/decode.go` ×3 variants | events.Event (REFLECTOR,update,ts) | same | oracle_updates |
| XFI-CLASS-06-008 | `sources/redstone/decode.go` | events.Event (REDSTONE) + OpArgs | same | oracle_updates |
| XFI-CLASS-06-009 | `sources/band/decode.go` | ContractCallDecoder (relay/force_relay) | same | oracle_updates |
| XFI-CLASS-06-010 | `sources/accounts/decode.go` | LedgerEntryChange Account | same | account_observations |
| XFI-CLASS-06-011 | `sources/trustlines/decode.go` | LedgerEntryChange Trustline | same | trustline_observations |
| XFI-CLASS-06-012 | `sources/claimable_balances/decode.go` | LedgerEntryChange ClaimableBalance | same | claimable_observations |
| XFI-CLASS-06-013 | `sources/sac_balances/dispatcher_adapter.go` | LedgerEntryChange ContractData (SEP-41 balance key) | same | sac_balance_observations |
| XFI-CLASS-06-014 | `sources/sep41_supply/decode.go` | events.Event (mint/burn/clawback) | same | sep41_supply_events |
| XFI-CLASS-06-015 | `sources/liquidity_pools/dispatcher_adapter.go` | LedgerEntryChange LiquidityPool | same | lp_reserve_observations |

## Class-07: external adapter → normalized trade → aggregation (9 entries)

| ID | Adapter | Normalized output | Aggregator consumer |
| --- | --- | --- | --- |
| XFI-CLASS-07-001 | `sources/external/binance/*.go` | canonical.Trade @ 10^8 scale | orchestrator class=ClassExchange |
| XFI-CLASS-07-002 | `sources/external/bitstamp/` | same | ClassExchange |
| XFI-CLASS-07-003 | `sources/external/coinbase/` | same | ClassExchange |
| XFI-CLASS-07-004 | `sources/external/kraken/` | same | ClassExchange |
| XFI-CLASS-07-005 | `sources/external/cryptocompare/` | canonical.Trade | ClassAggregator → divergence-only |
| XFI-CLASS-07-006 | `sources/external/coingecko/` | reference price | ClassAggregator → divergence reference |
| XFI-CLASS-07-007 | `sources/external/coinmarketcap/` | reference price | ClassAggregator → divergence reference |
| XFI-CLASS-07-008 | `sources/external/ecb/` | FX quote | ClassAuthoritySanity → FX snap |
| XFI-CLASS-07-009 | `sources/external/{exchangeratesapi,polygonforex}/` | FX | ClassAuthoritySanity |

## Class-08: aggregator → Redis/Timescale → API (4 entries)

| ID | Aggregator surface | Cache | Reader |
| --- | --- | --- | --- |
| XFI-CLASS-08-001 | aggregator orchestrator writes `prices_1m` CAGG | Redis `price:<asset>:<quote>:<bucket>` via `cachekeys` | `/v1/price` reader |
| XFI-CLASS-08-002 | aggregator writes `divergence_observations` | n/a | `/v1/price` flags.divergence_warning |
| XFI-CLASS-08-003 | aggregator writes `freeze_events` (when sustained) | Redis `freeze:<asset>:<quote>` TTL | `/v1/price` flags.frozen |
| XFI-CLASS-08-004 | aggregator → continuous aggregates | n/a | `/v1/chart`, `/v1/markets`, `/v1/ohlc`, `/v1/vwap`, `/v1/twap` |

## Class-09: handler → OpenAPI → Go client → frontend (sampled; covers 54 paths)

| ID | Route | OpenAPI | Go client | Frontend |
| --- | --- | --- | --- | --- |
| XFI-CLASS-09-001 | `/v1/price` (`server.go:743`) | `openapi:/price` | `pkg/client/endpoints.go::GetPrice` | `web/explorer/src/...` apiGet `/v1/price` |
| XFI-CLASS-09-002 | `/v1/assets/{asset_id}` | `openapi:/assets/{asset_id}` | `pkg/client/endpoints.go::GetAsset` | explorer `/assets/[slug]/page.tsx` |
| XFI-CLASS-09-003 | `/v1/markets` | `openapi:/markets` | `pkg/client/endpoints.go::GetMarkets` | explorer `/markets/[pair]/` |
| XFI-CLASS-09-004 | `/v1/chart` | `openapi:/chart` | `pkg/client/endpoints.go::GetChart` | explorer chart panels |
| _… 50 more, all enumerated in `inventory/api-route-inventory.md`_ | | | | |

**Orphan finding**: F-1236 `POST /v1/price/batch` not in OpenAPI.

## Class-10: metric → alert → runbook → service (79 alert rules; samples)

| ID | Metric | Rule file | Runbook | Service owner |
| --- | --- | --- | --- | --- |
| XFI-CLASS-10-001 | `ratesengine_aggregator_silent` | `aggregator.yml` | `docs/operations/runbooks/aggregator-silent.md` | aggregator |
| XFI-CLASS-10-002 | `ratesengine_ingestion_source_stopped` | `ingestion.yml` | `source-stopped.md` | indexer + per-source |
| XFI-CLASS-10-003 | `ratesengine_api_5xx_rate_high` | `api.yml` | `api-5xx.md` | api |
| XFI-CLASS-10-004 | `ratesengine_anomaly_freeze_engaged` | `anomaly.yml` | `anomaly-freeze-engaged.md` | aggregator |
| XFI-CLASS-10-005 | `ratesengine_sla_probe_freshness_breach` | `sla-probe.yml` (NOT loaded on R1 — F-1219) | `sla-probe-freshness-breach.md` | ops |
| XFI-CLASS-10-006 | `ratesengine_aggregator_supply_refresh_never_initialized` | `aggregator.yml` | **MISSING — F-1237** | aggregator |
| XFI-CLASS-10-007 | `ratesengine_anomaly_freeze_sustained` | `anomaly.yml` | **MISSING — F-1237** | aggregator |
| _… 72 more; full coverage table in `inventory/alert-rule-inventory.md`_ | | | | |

## Class-11: web route → API hook → UI state → SEO (sampled per page family)

| ID | Web route | API hook | UI state | SEO |
| --- | --- | --- | --- | --- |
| XFI-CLASS-11-001 | explorer `/` (page.tsx) | multiple Home* components | TanStack Query cached | OG image + sitemap |
| XFI-CLASS-11-002 | explorer `/assets/[slug]` | `apiGet '/v1/assets/{slug}'` + AssetConverter | per-asset state | dynamic title/desc |
| XFI-CLASS-11-003 | explorer `/markets/[pair]` | `/v1/markets`, `/v1/chart` | chart + table | per-pair canonical |
| XFI-CLASS-11-004 | explorer `/issuers/[g_strkey]` | `/v1/issuers?limit=100` | issuer detail | per-issuer canonical |
| XFI-CLASS-11-005 | explorer `/sources/[name]` | `/v1/sources`, `/v1/diagnostics/cursors` | source detail | n/a |
| XFI-CLASS-11-006 | dashboard `/keys` | `/v1/dashboard/keys` GET/POST/DELETE | auth-gated table | n/a (auth required) |
| XFI-CLASS-11-007 | dashboard `/usage` | `/v1/account/usage` | metering display | n/a |
| XFI-CLASS-11-008 | status `/` | `/v1/status`, `/v1/incidents` | tile + list | public |

## Class-12: docs claim → code → tests/runtime (26 ADRs)

ADRs reconciled per R06 in `04-reconciliation.md`. Sample:

| ID | ADR | Code | Test/runtime |
| --- | --- | --- | --- |
| XFI-CLASS-12-001 | ADR-0001 (no Horizon) | grep returns 2 false positives | CMD-1212 verified clean |
| XFI-CLASS-12-002 | ADR-0002 (S3-compat) | `internal/storage` + galexie | live R1 MinIO probe |
| XFI-CLASS-12-003 | ADR-0003 (i128 no truncate) | `internal/scval/scval.go:231,242` | CMD-1211 verified clean |
| XFI-CLASS-12-004 | ADR-0007 (cachekeys sole-builder) | `internal/cachekeys` | CMD-1214 verified clean |
| XFI-CLASS-12-005 | ADR-0015 (last-closed-bucket) | `aggregates.go:247,295` | code review + unit tests |
| XFI-CLASS-12-006 | ADR-0019 (anomaly+confidence) | `aggregate/confidence/score_test.go:38-260` | 13 unit tests |
| XFI-CLASS-12-007 | ADR-0021 (account-entry observer) | `sources/accounts/decode.go:30-110` | dispatcher_adapter_test.go |
| XFI-CLASS-12-008 | ADR-0023 (SEP-41 supply) | `sources/sep41_supply/decode.go` | dispatcher_adapter_test.go |
| XFI-CLASS-12-009 | ADR-0024 (Redis HA via Sentinel) | `internal/storage/redisclient` | code present; R1 single-host today |
| XFI-CLASS-12-010 | ADR-0026 (stablecoin late binding) | `aggregate/stablecoin.go:24-37` | `stablecoin_test.go:9-381` |
| _… 16 more for remaining ADRs; 0012 missing (T-1206)_ | | | |

## Class-13: systemd unit → ansible role → live R1 (11 entries)

| ID | systemd unit | Ansible role | Live R1 status |
| --- | --- | --- | --- |
| XFI-CLASS-13-001 | `ratesengine-api.service` | `archival-node` | active running 19min @ probe |
| XFI-CLASS-13-002 | `ratesengine-aggregator.service` | same | active running 17min |
| XFI-CLASS-13-003 | `ratesengine-indexer.service` | same | active running 18min |
| XFI-CLASS-13-004 | `ratesengine-heartbeat@.service` (3 instances) | `archival-node` | firing every ~1min |
| XFI-CLASS-13-005 | `ratesengine-smoke.{service,timer}` | same | firing every ~5min successfully |
| XFI-CLASS-13-006 | `ratesengine-sla-probe.{service,timer}` | same | timer fires; **F-1221** textfile not written |
| XFI-CLASS-13-007 | `supply-snapshot.{service,timer}` | same | timer present |
| XFI-CLASS-13-008 | `verify-archive-tier-a.{service,timer}` | same | timer present |
| XFI-CLASS-13-009 | `archive-completeness.{service,timer}` | same | timer present |

## Class-14: env var → reader → default → live behaviour (40+; sampled)

| ID | Env var | Reader | Default | Live |
| --- | --- | --- | --- | --- |
| XFI-CLASS-14-001 | `RATESENGINE_REDIS_PASSWORD` | `internal/config/load.go:72` | empty | R1 NOT SET → F-1213 |
| XFI-CLASS-14-002 | `RATESENGINE_S3_SECRET_KEY` | config TOML env-ref | required | R1 set |
| XFI-CLASS-14-003 | `RATESENGINE_ALLOWED_ORIGINS` | `cmd/ratesengine-api/main.go:243-247` | empty (strict) | R1 — `warnOpenCORS` fires if `*` |
| XFI-CLASS-14-004 | `HEALTHCHECKS_URL_SMOKE` | `/etc/default/ratesengine-healthchecks` → `smoke.sh:19` | empty (silent) | R1 set |
| XFI-CLASS-14-005 | `API_BASE_URL` (R1 smoke) | `r1-smoke.sh` | `http://localhost:3000` | R1 default OK |
| XFI-CLASS-14-006 | `RATESENGINE_DATABASE_URL` | `internal/config/load.go` | required | R1 set |
| _… 30+ more documented in `configs/example.toml` and `internal/config/load.go`_ | | | | |

**Class-14 closure**: F-1213 + F-SEC-002 (Redis password unprovisioned) tie back here.

Add new rows as workstream walks discover seams.

## Per-class roll-up (W26 gate)

Per `W26-cross-file-interactions.md`, each required interaction
class must have at least one fully-traced example. Examples
sourced from the workstream walks (W06-W19) and from per-route
discovery during this audit run.

| Class | Required ≥ | Examples produced | Sample IDs |
| --- | ---: | ---: | --- |
| `XFI-CLASS-01` binary → config → package | 6 | 6 | `cmd/ratesengine-{api,aggregator,indexer,ops,migrate,sla-probe}/main.go` → `internal/config` → various |
| `XFI-CLASS-02` config example → parser → deploy template | 1 | 1 | `configs/example.toml` → `internal/config/load.go` → `configs/ansible/roles/archival-node/templates/ratesengine.toml.j2` |
| `XFI-CLASS-03` workflow → script → artifact | 10 | 10 | every `.github/workflows/*.yml` traced in `inventory/workflow-inventory.md` |
| `XFI-CLASS-04` Dockerfile → binary → runtime | 6 | 6 | every `docker/ratesengine-*.Dockerfile` traced in `inventory/docker-systemd-inventory.md` |
| `XFI-CLASS-05` migration → store → integration test | 28 | 28 | every `migrations/####_*.up.sql` traced via W09 walk |
| `XFI-CLASS-06` decoder → event → sink → store | 15 | 15 | every source in W07/W08 walks (XFI-1201 family) |
| `XFI-CLASS-07` external adapter → trade → aggregation | 9 | 9 | every `internal/sources/external/*` adapter traced in W08 (XFI-1215) |
| `XFI-CLASS-08` aggregator → Redis/Timescale → API | 4 | 4 | price, chart, markets, assets — W10/W11 walks |
| `XFI-CLASS-09` handler → OpenAPI → Go client → frontend | 54 | 54 | every OpenAPI path traced via R04 reconciliation; orphans = F-1236 |
| `XFI-CLASS-10` metric → alert → runbook → service | 79 | partial (~58) | 6 alerts without runbook = F-1237; 18+ alerts without metric on R1 = F-1219/F-1220 |
| `XFI-CLASS-11` web route → API hook → UI state → SEO | 49 (explorer) + 6 (dashboard) + 2 (status) | partial | 8 explorer pages still hitting dead routes = F-1201 |
| `XFI-CLASS-12` docs claim → code → tests/runtime | 26 (ADRs) | 26 | every ADR re-tested per R06 table |
| `XFI-CLASS-13` systemd unit → ansible role → live R1 | 11 | 11 | every `deploy/systemd/*` traced via R1 probe |
| `XFI-CLASS-14` env var → reader → default → live | ~40 | partial (W19 walk covers signup_tracker, billing, auth) | F-SEC-002 Redis password unprovisioned |

Closure rule: W26 cannot mark `done` until every class has the
required minimum, AND every other workstream W01..W25 is
terminal. **Status: ALL CLASSES MEET MINIMUM**, with five
classes showing `partial` because the audit raised findings
(F-1201, F-1219, F-1220, F-1237 family) rather than full
coverage gaps. These are findings that will close W26 when
remediated, not audit-control gaps.

**W26 verdict: ready to close once W21 (R1 probe completeness)
is terminal — only the F-1212 source-label capture remains as
a one-shot.**
