---
title: SEP-1 stellar.toml resolution — operational reference
last_verified: 2026-05-03
status: living procedure
---

# SEP-1 stellar.toml resolution — operational reference

This doc covers the **runtime** + **on-call** concerns of SEP-1
resolution:

- HTTP failure-mode handling per home-domain
- Cache invalidation policy
- SSRF guard playbook
- Operator-facing instructions

The implementation lives in `internal/metadata/`; see that
package's [`doc.go`](../../internal/metadata/doc.go) for the
in-code overview.

## Resolution flow

```
asset request →
  v1/assets/{id} handler →
    metadata.Cache.Get(home_domain)
      ↓ Redis HIT (15-min TTL)
        return cached SEP1 struct
      ↓ Redis MISS
        singleflight gate (one fetch per home_domain at a time)
        metadata.Resolver.Resolve(home_domain)
          ↓ HTTP GET https://<home_domain>/.well-known/stellar.toml
              ↓ DNS resolve → SSRF guard (private/loopback/link-local rejected)
              ↓ TLS handshake (5s timeout)
              ↓ HTTP read (10s total budget)
              ↓ TOML parse
          ↓ on success: write to Redis with 15-min TTL, return SEP1
          ↓ on failure: return error to caller; DO NOT cache the error
```

Every parameter — TTL, timeouts, SSRF allow-list — is documented
in `cachekeys.TOMLTTL` / `metadata.ResolverConfig` and bound by the
ADR-0007 Redis cache schema (`toml:<domain>` namespace).

## Failure modes

### 404 from upstream

The home-domain server returns 404 for `/.well-known/stellar.toml`.
Common causes:

- Issuer hasn't published a SEP-1 file (most cases — many small
  issuers don't bother).
- Issuer rotated the file path (very rare; SEP-1 mandates the
  `.well-known` location).

**Resolver behaviour:** returns `ErrSEP1NotFound`. **Cache
behaviour:** the error is NOT cached (per design — a transient
404 during a deployment shouldn't poison the cache for 15 min).
**Handler behaviour:** asset overlay degrades cleanly — fields
that come from SEP-1 are reported as `null`, the `home_domain`
field on the asset response stays populated from the `AccountEntry`,
the `sep1_status` field is set to `"not_found"`.

**On-call action:** none if a single asset is affected; investigate
if many home-domains report `not_found` simultaneously (suggests a
network egress problem on our side, not the issuers').

### TLS / connection error

Common causes:

- Issuer's TLS cert expired
- Hosting provider DNS down
- HTTPS-redirect loop
- Cert issued for a different name (CN mismatch)

**Resolver behaviour:** returns `ErrSEP1HTTP` wrapping the network
error. **Handler behaviour:** same as 404 — overlay degrades,
`sep1_status` becomes `"unreachable"`.

**On-call action:** scoped to that asset. The `host-down` and
`scrape-failing` runbooks don't apply here — this is third-party
infrastructure.

### Timeout

`metadata.ResolverConfig.HTTPTimeout` (default 10s) is the total
read budget. Slow issuers can blow this; we don't tune it per
host because that defeats the bound.

**Resolver behaviour:** `ErrSEP1Timeout`. **Cache:** not cached.
**Alert:** `stellarindex_metadata_resolver_timeout_total` increases
beyond baseline (P3 alert, designed but not yet shipping at v1).

### TOML parse error

The fetched bytes don't parse as valid TOML. Causes seen in the wild:

- Issuer's CMS injected HTML around the TOML
- BOM byte at the start (not always handled by the TOML parser)
- Trailing garbage
- Invalid UTF-8 sequences

**Resolver behaviour:** `ErrSEP1MalformedTOML`. **On-call action:**
report the issuer; we don't try to "fix" their published file.

### SSRF rejection

The `metadata.Resolver` resolves DNS first, then checks the
resolved IP against a private-range deny-list:

- IPv4 RFC 1918: 10/8, 172.16/12, 192.168/16
- IPv4 loopback: 127/8
- IPv4 link-local: 169.254/16 (catches AWS instance metadata
  service — `169.254.169.254`)
- IPv4 multicast: 224/4
- IPv6 RFC 4193 (private): fc00::/7
- IPv6 loopback: ::1
- IPv6 link-local: fe80::/10

**Resolver behaviour:** `ErrSEP1PrivateIP`. **On-call action:**
malicious-issuer event — investigate. The `home_domain` value on
the affected asset's account is operator-supplied but came from
the chain (issuer set it via `SetOptionsOp`); the offender is the
issuer, not us. Report to the security mailing list per
[security.md](../../SECURITY.md).

## Cache invalidation

The 15-min TTL handles routine staleness. Three cases need
explicit invalidation:

### 1. Issuer publishes a corrected stellar.toml

After a fix the issuer wants visible immediately. Invalidate the
single key:

```sh
redis-cli -h <redis-master> DEL "toml:<home_domain>"
```

The next request triggers a fresh fetch. Singleflight ensures
only one fetch even if many requests pile up at once.

### 2. Asset's `home_domain` changes on chain

Issuer sets a new `home_domain` via `SetOptionsOp`. The trades
hypertable + asset metadata table observe this in the
LedgerEntryChange stream. The OLD home-domain's cache entry is
still valid (it's the toml content that hasn't changed); the
asset's link to it is what flipped.

The asset-overlay handler reads the asset's CURRENT home_domain
and looks up the cache by that key. So a domain change is
self-resolving — no operator action needed unless the operator
specifically wants to evict the OLD domain's cache entry.

### 3. Bulk eviction (post-incident)

If a CDN or DNS provider issue caused many issuers to look broken
simultaneously, we may have cached degraded responses. Evict the
whole namespace:

```sh
redis-cli -h <redis-master> --scan --pattern "toml:*" | \
  xargs -n 100 redis-cli -h <redis-master> DEL
```

Sub-second on a few hundred entries. Won't melt Redis. Subsequent
asset requests trigger fresh fetches; singleflight gates the
thundering herd.

## Operator-facing tasks

### Adding a curated issuer → home-domain mapping

The on-chain `AccountEntry.HomeDomain` isn't currently indexed in
our trades hypertable, so when an issuer's home-domain is missing
or wrong on-chain, the API has nothing to feed to the SEP-1
resolver. Operators close that gap via a curated map:

```toml
# /etc/stellarindex.toml
[metadata.issuer_home_domains]
"GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" = "centre.io"
```

The map's only job is supplying `home_domain` for the SEP-1
resolver lookup; the resolver then fetches the issuer's
`.well-known/stellar.toml` and the rest of the asset-detail
fields (name, description, image, conditions, max_supply…) come
from there. There is **no** broader "override the SEP-1 fields
themselves" knob today — fixing per-field metadata requires the
issuer to publish a corrected stellar.toml at the home-domain.

### Removing a stale mapping

Edit the same `[metadata.issuer_home_domains]` table; delete the
issuer's row; re-deploy the binary (the config is parsed at boot
in `internal/config/load.go`, not hot-reloaded).

### Tracing a specific asset's resolution

A future `stellarindex-ops sep1-trace -domain <home_domain>`
subcommand (not in `cmd/stellarindex-ops/main.go`'s switch today)
would dump the full resolution path: DNS, IP, SSRF check
result, HTTP status, parsed fields. Until it lands the manual
playbook is:

```sh
# 1. Confirm what the API sees
curl -sf https://api.stellarindex.io/v1/assets/<asset_id> | jq .

# 2. Confirm what the cache holds
redis-cli -h <redis-master> GET "toml:<home_domain>"

# 3. Bypass the cache, hit the issuer directly
curl -sfL "https://<home_domain>/.well-known/stellar.toml"

# 4. If 1 and 3 disagree but 2 looks stale, force-refresh:
redis-cli -h <redis-master> DEL "toml:<home_domain>"
```

## Metrics + alerts

The `internal/metadata` package emits these counters / gauges via
`internal/obs`:

- `stellarindex_metadata_resolver_requests_total{status}` —
  status ∈ {ok, not_found, http_error, timeout, parse_error,
  private_ip_blocked}.
- `stellarindex_metadata_cache_hits_total` /
  `stellarindex_metadata_cache_misses_total`.
- `stellarindex_metadata_resolver_duration_seconds` histogram.

Alert: `stellarindex_metadata_resolver_error_rate_high` is
designed but not yet shipping — no rule in
`deploy/monitoring/rules/` produces it today. The metadata
overlay IS wired into `/v1/assets/{id}` already (see §"Resolution
flow" above) — what's missing is the per-rate Prometheus rule
that turns the existing counters into a paged signal. Tracked
as a future hardening item.

## References

- Cache schema: [ADR-0007](../adr/0007-redis-cache-schema.md)
  (`toml:<domain>` namespace, 15-min TTL)
- Supply policy: [ADR-0011](../adr/0011-supply-algorithm.md) (uses
  SEP-1 `[[CURRENCIES]].max_supply` as the off-chain max-supply
  source)
- Package: `internal/metadata/`
- SEP-1 spec: <https://github.com/stellar/stellar-protocol/blob/master/ecosystem/sep-0001.md>
