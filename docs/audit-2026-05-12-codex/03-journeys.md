# Mandatory Journeys

Every journey must be traced from primary code evidence. A journey is
complete only when it includes happy path, degraded path, hostile path,
storage/cache behavior, observability, tests, docs truth, and findings
or explicit no-finding notes.

## Data Ingest And Market Formation

| ID | Journey | Status | Required Trace |
| --- | --- | --- | --- |
| J01 | Soroban ledger event ingest | todo | `stellar-rpc/Galexie/Hubble input -> ledgerstream/backfill -> dispatcher -> decoder -> pipeline -> Timescale -> metrics` |
| J02 | Classic operation ingest | todo | `classic operation -> source observer -> pipeline -> store -> aggregate/API surface` |
| J03 | Soroswap swap and sync | todo | `event topics -> reserves/amounts -> pair identity -> trade write -> contribution/aggregate` |
| J04 | Aquarius trade | todo | `fixture/live shape -> decoder -> amount/asset normalization -> storage` |
| J05 | Phoenix swap | todo | `contract event -> decoder -> pair/route attribution -> storage` |
| J06 | Comet trade | todo | `event decode -> source policy -> store -> downstream visibility` |
| J07 | Blend auction and lending | todo | `pool event -> auction decode -> storage -> API/web/known non-consumer boundary` |
| J08 | SDEX trade/offers | todo | `classic ledger data -> SDEX source -> store -> market surfaces` |
| J09 | Router attribution | todo | `raw route signal -> router table -> source contribution -> API/product display` |
| J10 | TVL/reserves/MEV | todo | `pool state -> derived metrics -> storage -> explorer/API` |

## Oracle, External, And Fiat Sources

| ID | Journey | Status | Required Trace |
| --- | --- | --- | --- |
| J11 | Reflector oracle update | todo | `event -> oracle decode -> storage -> price/confidence consumer` |
| J12 | Redstone oracle update | todo | `payload/event -> authenticity assumptions -> storage -> consumer` |
| J13 | Band oracle update | todo | `payload/event -> decode -> storage -> consumer` |
| J14 | Frankfurter/ECB FX quote | todo | `poll -> parse -> freshness -> storage -> API/aggregation` |
| J15 | Binance external venue | todo | `poll/backfill -> adapter -> normalized trade -> store -> aggregate` |
| J16 | Bitstamp external venue | todo | `poll/backfill -> adapter -> normalized trade -> store -> aggregate` |
| J17 | Coinbase external venue | todo | `poll/backfill -> adapter -> normalized trade -> store -> aggregate` |
| J18 | Kraken external venue | todo | `poll/backfill -> adapter -> normalized trade -> store -> aggregate` |
| J19 | CoinGecko/CoinMarketCap references | todo | `poll -> source policy -> comparison/reference use -> API or exclusion` |
| J20 | External source outage | todo | `timeout/rate limit/schema drift -> retry/fallback -> metrics -> alert` |

## Aggregation And Correctness

| ID | Journey | Status | Required Trace |
| --- | --- | --- | --- |
| J21 | Closed-bucket price | todo | `trades/oracles -> aggregate bucket -> Redis/Timescale -> /v1/price -> flags` |
| J22 | VWAP/TWAP/OHLC | todo | `raw observations -> aggregate math -> bucket boundaries -> API chart/history` |
| J23 | Volume and liquidity | todo | `source volume -> USD conversion -> aggregates -> market rankings` |
| J24 | Baseline/anomaly/freeze | todo | `baseline -> detector -> freeze state -> API confidence/flags -> alert` |
| J25 | Divergence cross-check | todo | `reference sources -> divergence calc -> storage -> API/explorer/alert` |
| J26 | Triangulation fallback | todo | `missing direct market -> route/fx/stablecoin proxy -> confidence and provenance` |
| J27 | Stablecoin fiat proxy | todo | `stablecoin mapping -> late binding -> conversion -> docs/API truth` |
| J28 | Cache miss and stale cache | todo | `Redis miss/stale -> DB fallback or error -> cache headers -> metrics` |

## API, Auth, And Client

| ID | Journey | Status | Required Trace |
| --- | --- | --- | --- |
| J29 | Public price API | todo | `request -> middleware -> handler -> store/cache -> response -> OpenAPI/client` |
| J30 | Chart/history API | todo | `params -> validation -> query -> pagination/window -> response contract` |
| J31 | Asset detail API | todo | `slug/asset parse -> registry -> metadata/SEP-1/supply -> response` |
| J32 | Markets/trades/catalogue API | todo | `query params -> cursor -> store -> response -> frontend consumer` |
| J33 | Streaming/SSE API | todo | `publisher -> Redis pub/sub -> SSE handler -> reconnect/backpressure` |
| J34 | API key auth | todo | `key -> middleware -> validator -> rate-limit identity -> authorized handler` |
| J35 | SEP-10/dashboard auth | todo | `challenge/callback -> session/JWT -> dashboard route -> logout/expiry` |
| J36 | Public Go client | todo | `client method -> endpoint URL -> error handling -> API response parity` |

## Frontend And Product

| ID | Journey | Status | Required Trace |
| --- | --- | --- | --- |
| J37 | Explorer homepage | todo | `static/server route -> API hooks -> live panels -> error/loading states` |
| J38 | Asset page | todo | `slug route -> asset detail -> chart/tabs/converter -> SEO/metadata` |
| J39 | Market page | todo | `pair route -> chart/table -> source attribution -> stale state` |
| J40 | DEX/source pages | todo | `source route -> pools/stats -> source-specific claims -> API parity` |
| J41 | Issuer/account pages | todo | `issuer/account route -> trust/supply/metadata -> empty/error states` |
| J42 | Widgets and embeds | todo | `embed route -> API client -> headers/cache -> public contract` |
| J43 | Dashboard keys/usage/admin | todo | `auth guard -> API calls -> key lifecycle -> permission boundaries` |
| J44 | Status page incident flow | todo | `incident source -> markdown parse -> route -> sitemap/robots/headers` |

## Operations, Runtime, And Recovery

| ID | Journey | Status | Required Trace |
| --- | --- | --- | --- |
| J45 | Migrations | todo | `migrate binary -> SQL files -> DB version -> rollback behavior -> tests` |
| J46 | Deploy workflow | todo | `GitHub workflow -> artifact -> host/service -> smoke checks -> rollback` |
| J47 | Docker compose dev stack | todo | `compose -> services -> health -> migrations -> local smoke` |
| J48 | Systemd service lifecycle | todo | `unit -> env/config -> binary -> logs -> restart policy` |
| J49 | Ansible archival node | todo | `playbook -> roles -> templates -> host state -> idempotency` |
| J50 | Archive completeness | todo | `archive source -> checker -> metrics/logs -> alert/runbook` |
| J51 | Verify archive chunks | todo | `ops command -> chunk source -> validation -> output/metric` |
| J52 | Cross-region determinism | todo | `region A -> comparison -> region B -> mismatch classification -> alert` |
| J53 | SLA probe | todo | `probe config -> API request -> textfile -> Prometheus rule -> runbook` |
| J54 | Supply snapshot | todo | `ledger observations -> supply compute -> snapshot write -> API/web/ops read` |
| J55 | SEP-1 refresh | todo | `asset issuer -> TOML fetch -> metadata overlay -> cache/store -> API` |
| J56 | R1 live health | todo | `SSH read-only checks -> service status -> logs/metrics -> source comparison` |

## Hostile And Failure Journeys

| ID | Journey | Status | Required Trace |
| --- | --- | --- | --- |
| J57 | Malformed Soroban event | todo | `bad topics/SCVal -> decoder error -> stats -> no corrupt write` |
| J58 | Extreme numeric values | todo | `i128/u128/decimal edge -> parse -> store/API -> no truncation` |
| J59 | Duplicate/replayed ledger data | todo | `duplicate input -> idempotent write/cursor behavior -> no double volume` |
| J60 | Partial database outage | todo | `store error -> retry/fail -> metrics -> operator visibility` |
| J61 | Redis outage | todo | `cache/pubsub failure -> fallback/error -> API/SSE behavior -> alert` |
| J62 | External API manipulation | todo | `bad outlier/stale payload -> confidence/freeze/divergence behavior` |
| J63 | Proxy/header abuse | todo | `spoofed headers -> trusted proxy logic -> auth/rate-limit identity` |
| J64 | Frontend stale/static drift | todo | `static build -> API schema change -> visible failure or guard` |
| J65 | CI false positive | todo | `workflow green -> missing local target/test -> documented risk` |
| J66 | Runbook mismatch | todo | `alert -> runbook command -> actual service/config -> mismatch finding` |
