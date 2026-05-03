---
title: CDN setup for `api.ratesengine.net` (L3.14)
last_verified: 2026-05-03
status: operator runbook
---

# CDN setup for `api.ratesengine.net`

Operator runbook for closing **L3.14** in the launch-readiness
backlog. The origin-side `Cache-Control` middleware ships in code
(`internal/api/v1/middleware/cachecontrol.go` + tests); this doc
covers the **infra-side** provisioning of a CDN in front of the
API.

## What the API origin sends today

Per ADR-0018 each surface emits a different `Cache-Control` header:

| Path | Cache policy | Why |
| --- | --- | --- |
| `/v1/price`, `/v1/price/tip` | `Cache-Control: public, max-age=1` | Closed-bucket / tip — cache for ~tick-cadence; CDN absorbs hot-asset request floods |
| `/v1/history/since-inception` | `Cache-Control: public, max-age=300, s-maxage=86400` | Historical data is immutable on closed buckets — long edge-cache, short browser-cache |
| `/v1/assets/{id}` | `Cache-Control: public, max-age=60` | Asset-detail blocks; F2 fields refresh on supply-snapshot cadence |
| `/v1/sources`, `/v1/markets` | `Cache-Control: public, max-age=300` | Catalogue surfaces — change rarely |
| `/v1/account/*`, `/v1/auth/*` | `Cache-Control: no-store` | Per-caller; never cacheable |
| SSE streams (`/stream` suffix) | `Cache-Control: no-store` | Long-lived; CDN must passthrough |

The middleware classifier lives at
`internal/api/v1/middleware/cachecontrol.go::policyForPath`. If
a CDN deployment differs from the conventions here, override per
path at the CDN layer rather than patching the middleware — origin
behaviour stays universal.

## Provider choice

Three are equivalent for our needs; pick by ops familiarity, not
technical criteria:

| Provider | Strengths | Pricing (rough) | Notes |
| --- | --- | --- | --- |
| **CloudFront (AWS)** | Mature, cheap egress at scale, integrates with Route53 if hosted there | ~$0.085/GB | r2 already runs on AWS — natural choice if multi-region wants to share AWS infra |
| **Bunny CDN** | Cheap, fast TTFB in EU, simple config | ~$0.005–0.020/GB | Best price/performance for v1; manual config via web UI |
| **Cloudflare** | Free tier covers most of v1 launch; deepest DDoS posture | Free–$20/mo | Zero TLS work; good if status-page also lives here |

**Recommendation for v1: Cloudflare.** Free tier covers projected
launch traffic, the same panel handles DNS + TLS, and the routing
rules mirror what we'd build on Bunny / CloudFront if we
graduate later.

## Step-by-step (Cloudflare)

```
0. Pre-reqs
   - DNS for ratesengine.net is already in Cloudflare (move there
     first if not — separate runbook).
   - Origin is reachable at the per-region HAProxy frontends
     (api-r1.ratesengine.net etc., per multi-region-topology.md).

1. Create the proxied DNS record
   - Type: CNAME (or A if pointing at a single region pre-multi-region)
   - Name: api
   - Target: <origin host>
   - Proxy status: Proxied (orange cloud)

2. Set up an SSL/TLS mode
   - SSL/TLS → Overview → Mode: Full (strict)
   - Edge cert auto-provisions via Cloudflare; origin uses our
     existing HAProxy TLS chain.

3. Configure caching
   - Caching → Configuration → Browser Cache TTL: Respect Existing Headers
     (origin sends max-age, don't override at edge).
   - Page Rules / Cache Rules:
     - URL pattern: api.ratesengine.net/v1/history/*
       Cache Level: Cache Everything
       Edge Cache TTL: 1 day
     - URL pattern: api.ratesengine.net/v1/sources
       Cache Level: Cache Everything
       Edge Cache TTL: 5 minutes
     - URL pattern: api.ratesengine.net/v1/markets
       Cache Level: Cache Everything
       Edge Cache TTL: 5 minutes
     - URL pattern: api.ratesengine.net/v1/auth/*
       Cache Level: Bypass
     - URL pattern: api.ratesengine.net/v1/account/*
       Cache Level: Bypass
     - URL pattern: api.ratesengine.net/*/stream
       Cache Level: Bypass
       (also: WebSockets/SSE → set to no-buffer at proxy layer)

4. Lock SSE passthrough
   - Network → WebSockets: ON
   - SSE-specific: Cloudflare's auto-buffer for long-lived
     responses can break SSE. Set the per-route `cache-control`
     bypass + add a Page Rule with `Disable Performance` for
     `/*/stream` so HTTP/2 push optimisations don't intercept.
```

## Verification

After config takes effect (DNS propagation + first proxy):

```sh
# 1. Cache headers survive the edge
curl -sI https://api.ratesengine.net/v1/history/since-inception?asset=native | grep -iE "cache-control|cf-cache-status|age"
# Expect:
#   cache-control: public, max-age=300, s-maxage=86400
#   cf-cache-status: HIT (or MISS on first request, then HIT)

# 2. Auth endpoints bypass
curl -sI https://api.ratesengine.net/v1/account/me -H "Authorization: Bearer <demo-key>" | grep -iE "cache-control|cf-cache-status"
# Expect:
#   cache-control: no-store
#   cf-cache-status: BYPASS

# 3. SSE passes through
curl -sN https://api.ratesengine.net/v1/price/tip/stream?base=native&quote=fiat:USD &
# Should emit `data:` lines within 5s; Ctrl-C to close.

# 4. Origin gets a cache-key signal
# Check the origin's http_requests_total metric — historical
# endpoints should show a much lower request rate than they
# would unproxied (CDN absorbs the bulk).
```

## Rollback

If the CDN misbehaves (caching too aggressively, breaking SSE,
masking 5xx as 200), the rollback is a single DNS change:

```
DNS → api → Proxy status: DNS only (grey cloud)
```

The record stays the same; traffic just stops going through the
CDN. Origin behaviour is unchanged.

## Cross-references

- Origin middleware: `internal/api/v1/middleware/cachecontrol.go`
- Per-surface policy decisions: [ADR-0018](../adr/0018-api-consistency-surfaces.md)
- Multi-region origin layout: [multi-region-topology.md](../architecture/infrastructure/multi-region-topology.md)
- Launch-readiness row: L3.14 in [launch-readiness-backlog.md](../architecture/launch-readiness-backlog.md)
