# Adversarial Attack Tree

Every leaf must be tested or explicitly dispositioned. If a path is
undefended or only partially defended, create a finding.

## Data Integrity

| ID | Attack | Target | Expected Defense | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- | --- |
| AT-A01 | i128/u128 amount exceeds int64 | decoders, storage, API | big.Int/NUMERIC end to end | | | |
| AT-A02 | Swap event missing paired reserve event | Soroswap/Phoenix | pairing/grouping rejection | | | |
| AT-A03 | Partial grouped event set | Phoenix and similar decoders | no corrupt write | | | |
| AT-A04 | Topic collision from wrong contract/source | dispatcher/decoder | topic plus source context validation | | | |
| AT-A05 | SEP-41 body shape changes | supply decoder | typed shape checks | | | |
| AT-A06 | Malformed SCVal/scvec | scval helpers | bounds/type errors, no panic | | | |
| AT-A07 | WASM upgrade changes schema mid-backfill | decoders/backfill | WASM history and BackfillSafe gate | | | |
| AT-A08 | Duplicate/replayed ledger events | pipeline/store | idempotency and cursor safety | | | |

## Vendor And Oracle Manipulation

| ID | Attack | Target | Expected Defense | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- | --- |
| AT-B01 | Vendor returns wrong pair under valid symbol | external adapters | pair validation | | | |
| AT-B02 | Vendor returns 200 empty/malformed body | external adapters | parse error and metrics | | | |
| AT-B03 | Vendor timestamp in future or stale | aggregation | freshness/skew guard | | | |
| AT-B04 | 5xx or 429 storm | external runner | backoff/circuit breaker/no budget burn | | | |
| AT-B05 | Stablecoin depeg hidden by proxy | aggregation | divergence/freeze/confidence flags | | | |
| AT-B06 | Oracle contract upgraded with semantic drift | oracle decoders | WASM/schema audit gate | | | |

## API, Auth, Rate Limit, And Cache

| ID | Attack | Target | Expected Defense | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- | --- |
| AT-C01 | API-key timing/enumeration | auth middleware | constant-time and generic errors | | | |
| AT-C02 | Revoked key accepted from cache | auth/cache | immediate invalidation or bounded risk | | | |
| AT-C03 | SEP-10 replay/audience/network mismatch | SEP-10 auth | nonce, audience, passphrase checks | | | |
| AT-C04 | X-Forwarded-For spoofing | trusted proxy/rate limit | direct caller ignored unless trusted | | | |
| AT-C05 | Cache key argument drift | prewarm/handlers | shared key builders and tests | | | |
| AT-C06 | SEP-1 SSRF via home domain | metadata fetcher | scheme/IP/timeout/body limits | | | |
| AT-C07 | Crafted cursor forces DB scan or 500 | API pagination | bounded decode and 400 errors | | | |
| AT-C08 | Slow SSE consumer leaks memory | streaming | bounded buffers and disconnect | | | |
| AT-C09 | Removed routes are expensive | router/404 path | cheap 404/410 and docs cleanup | | | |

## Infrastructure And Runtime

| ID | Attack | Target | Expected Defense | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- | --- |
| AT-D01 | Timescale unavailable or cagg stale | API/aggregator | alerts, cache behavior, runbook | | | |
| AT-D02 | Redis failover/OOM | cache/stream/auth | fallback and alerts | | | |
| AT-D03 | MinIO/Galexie disk full or stall | ingest/archive | headroom alerts and runbook | | | |
| AT-D04 | Cloudflare/Caddy TLS/proxy failure | public API | fail closed and alert | | | |
| AT-D05 | Alertmanager/Loki/Promtail blind spot | observability | monitoring of monitoring | | | |
| AT-D06 | GitHub Action/base image compromise | supply chain | SHA pins/SBOM/signing | | | |
| AT-D07 | Root prebuilt binary trusted | release hygiene | ignored or verified artifacts | | | |

## Operator, Launch, Legal, And Privacy

| ID | Attack | Target | Expected Defense | Status | Evidence | Finding |
| --- | --- | --- | --- | --- | --- | --- |
| AT-E01 | Privileged key mint/upgrade misuse | ops commands | SSH controls and audit log | | | |
| AT-E02 | Unbounded backfill DoS | ops/backfill | range validation and operator guard | | | |
| AT-E03 | Deploy unverified artifact | release/deploy | checksum/signature validation | | | |
| AT-E04 | DNS public flip without smoke | launch | dry-run checklist and rollback | | | |
| AT-E05 | Status page green during outage | status/ops | independent health basis | | | |
| AT-E06 | API keys or PII in logs | logging/webhooks | redaction and tests | | | |
| AT-E07 | Paid-feed licence breach | legal/product | licence review and usage boundaries | | | |
| AT-E08 | GDPR deletion/export absent | auth/billing/privacy | documented customer-data path | | | |
