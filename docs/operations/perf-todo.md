---
title: API performance follow-ups
last_verified: 2026-05-05
status: living doc
---

# API performance follow-ups

Captured during the post-#690 perf-investigation pass. The
route-label fix in #690 stopped masking the slow-request ratio
behind constant `route="unmatched"` denominators; the SLO recording
rules then started reporting real signals.

## Current state on R1 — 2026-05-05 (post-cache rollout)

All three problem endpoints from the original write-up now have
Redis read-through caches in front of them. Cold reads still pay
the underlying DB cost; warm reads are sub-millisecond.

| Endpoint | Cold | Warm | RFP target | Notes |
|----------|-----:|-----:|-----------:|-------|
| `/v1/price` (fiat quote) | ~1 ms | ~1 ms | 200 ms | #692 short-circuit; no DB hit |
| `/v1/oracle/latest` | ~600 ms | ~0.5 ms | 200 ms | Redis 30 s TTL (#696) |
| `/v1/markets` | ~570 ms | ~0.3 ms | — | Redis 60 s TTL (#697) |
| `/v1/assets` | ~635 ms | ~0.4 ms | — | Redis 60 s TTL (#697) |
| `/v1/coins` | ~18 ms | — | — | already fast — no cache wired |

Customer impact: the moment any consumer makes more than one
request per minute they see warm reads end-to-end. The smoke
timer firing every 5 min always hits cold cache, which is what
keeps the synthetic-monitoring p99 around the cold-read times —
not a customer-experience issue.

## What's already shipped from this investigation

| PR | Effect |
|----|--------|
| **#690** | `obs.HTTPMetrics` + `obs.CaptureRoute`: fixed the route-label-always-`"unmatched"` bug that masked the slow-request ratio. |
| **#691** | `slo.yml` recording rules scope to `/v1/price + /v1/oracle/*` (the RFP target), not the entire API surface. |
| **#692** | `/v1/price` for fiat-quoted pairs short-circuits the `LatestTradesForPair` fallback (a fiat-quoted pair never has on-chain trades). 215 ms → ~1 ms. |
| **#695** | `/v1/oracle/latest` translates `native` → `[native, crypto:XLM]` (and classic credit assets to their `crypto:<TICKER>` form) so Reflector's per-ticker rows actually surface. |
| **#696** | Redis read-through cache for `/v1/oracle/latest`, 30 s TTL — 580 ms → 0.5 ms warm. |
| **#697** | Redis read-through caches for `/v1/assets` + `/v1/markets`, 60 s TTL — both ~600 ms → ~0.3 ms warm. |
| **#689** | `/v1/status` `Cache-Control: public, max-age=10, s-maxage=15` — CDN-friendly polling. |

## What's still pending

### 1. Cold-read latency on the catalogue endpoints

Even with the caches above, the FIRST request after a TTL expires
still pays the full DB cost (~600 ms for /v1/markets, /v1/assets,
/v1/oracle/latest). For high-frequency consumers this is invisible;
for low-frequency consumers (the daily-curl-from-cron use case) it
shows up.

Cold-read fix paths, in order of ambition:

1. **CDN in front of R1.** Cache-Control already emits `s-maxage=N`
   directives. When `api.ratesengine.net` lands behind Cloudflare /
   equivalent, customers see edge-cache hits for shareable URLs
   regardless of Redis TTL. **Smallest change; operator action.**
2. **Stale-while-revalidate cache.** Serve the warm value
   immediately while refreshing async. Customer never sees a cold
   read; Redis stays continuously hot. ~50 LOC each on top of the
   existing `cachedOracleReader` / `cachedAssetReader` /
   `cachedMarketsReader` wrappers in `cmd/ratesengine-api/main.go`.
3. **Materialised tables.** `markets_summary` and
   `assets_catalogue` tables maintained by the indexer on every
   trade insert; `Store.DistinctPairs` / `DistinctAssets` read
   directly. Cold reads become O(distinct rows), not O(trades).
   See the original perf-todo for the schema sketches. Multi-PR
   effort; needed only if (1) and (2) prove insufficient at
   sustained customer volumes.

### 2. `/v1/oracle/latest` cold-read TimescaleDB compressed-chunk indexing

EXPLAIN ANALYZE on R1 showed one specific compressed chunk
(`compress_hyper_11_1126_chunk`) doing a 280 ms `Seq Scan` while
every other chunk does an Index Scan in <0.1 ms. The chunk's
segment-by index appears to be missing or stale. A
`recompress_chunk('compress_hyper_11_1126_chunk', if_not_compressed=>true)`
would rebuild it. **Operator action; not safe to automate
without explicit confirmation** (chunk recompression is a write
operation; if it goes wrong it leaves the chunk in a worse state).

The Redis cache from #696 hides this from customer-facing
latency, so this is a "nice to have" rather than urgent.

### 3. Synthetic-monitoring SLO noise

The smoke timer at 5 min fires past the 30 s/60 s cache TTLs and
always sees cold reads. With nothing but synthetic traffic on R1
today, the SLO `slow-request-ratio` recording rule is dominated by
those cold reads — the `ratesengine_slo_latency_burn_*` alerts
keep firing for a real-on-R1 reason that's invisible to actual
customers (because customers polling at <1-min cadence see warm
reads).

Three angles, no consensus on which is right:

1. Lengthen smoke cadence past TTL to keep cache warm — but the
   smoke timer's job is regression detection, less frequent
   polling weakens that.
2. Lengthen cache TTLs — but compromises freshness commitments.
3. Tag the smoke probe via `User-Agent` and exclude it from the
   SLO recording rule. Cleanest semantically; the SLO measures
   customer experience, not synthetic monitoring.

(3) is the right move; punted to a follow-up when launch traffic
arrives — the noise is a pre-launch artifact and will heal as
real polling fan-out dilutes it.
