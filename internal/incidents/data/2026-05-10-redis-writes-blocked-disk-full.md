---
title: "[SEV-2] /v1/price returning 404 on rewritten pairs — Redis writes blocked by disk-full — 2026-05-10"
date: 2026-05-10
severity: SEV-2
status: resolved
started_at: 2026-05-10T03:39:00Z
resolved_at: 2026-05-10T12:57:00Z
affected_components:
  - api
  - aggregator
postmortem:
---

# [SEV-2] /v1/price returning 404 on rewritten pairs — Redis writes blocked by disk-full

## What happened

For approximately 9 hours on 2026-05-10, `/v1/price` returned 404
`price-not-found` for any pair whose VWAP is served from the Redis
cache (rewritten / triangulated / stablecoin-proxy paths). Pairs
that fall through to the `prices_1m` CAGG directly continued to
serve normally.

Root cause: r1's root filesystem (`/dev/md1`, 49 GB) reached 100%
capacity at 03:39 UTC, dominated by 35 GB of stale logs in
`/var/log` (8.6 GB rotated syslog, 7.6 GB postgres, 5.5 GB galexie
verify history, 3.5 GB systemd journal, 2.2 GB one-time WASM-audit
stderr captures). Redis was unable to write its periodic RDB
snapshot. With the default `stop-writes-on-bgsave-error yes`
configuration, every subsequent `SET` returned `MISCONF Redis is
configured to save RDB snapshots, but it's currently unable to
persist to disk`. The aggregator's VWAP cache writes failed for
every pair, every refresh cycle (~15 WARNs per 30 s observed in
the aggregator log).

`flags.stale` did NOT fire — the aggregator was running, just
unable to publish — so the customer-visible signal was a 404 on
specific pairs (most user-noticed: `XLM/USD` via the stablecoin
proxy path) rather than a stale flag.

## Why this surface

Pairs that landed in `prices_1m` directly (CAGG-served closed-bucket
VWAPs) kept serving because the read path doesn't depend on Redis
for those. Only pairs computed at aggregator runtime and cached in
Redis — anything triangulated, anything proxied through a stablecoin
peg, the rolling-window tip surface — went dark.

## Timeline (UTC)

- **03:39** — Redis log first records `Write error saving DB on disk(rdbSaveRio): No space left on device`. Aggregator WARN cadence picks up on the next refresh tick.
- **2026-05-09 → 2026-05-10 (overnight)** — `/v1/price` 404s noticed during diagnostic audit of the X/fiat:USD coverage family.
- **12:50** — Operator on-call ran disk audit; found root FS at 100% with 35 GB of stale logs.
- **12:53** — `journalctl --vacuum-size=200M` freed 3.3 GB → root at 93%.
- **12:55** — Truncated `/var/log/syslog.1` (8.6 GB) and removed `/var/log/wasm-history-*.stderr` (2.2 GB) → root at 70%, 15 GB free.
- **12:57** — `redis-cli BGSAVE` succeeded; cleared `stop-writes-on-bgsave-error` block; aggregator WARN cadence dropped to 0.
- **12:57:20** — `/v1/price?asset=native&quote=USDC-classic` returned 200 OK with fresh observation. Customer-visible recovery.

## Customer impact

Approximately 9 hours of degraded service on rewritten / triangulated
price pairs. Direct DEX-pair queries (e.g. `?asset=native&quote=USDC-classic`)
were unaffected once the cache was rebuilt; the flagship
`?asset=native&quote=fiat:USD` query that depends on the stablecoin
proxy fallback would have stayed degraded until a future binary
rebuild deploys (rc.38 ships the proxy fix that was implemented
during the incident, fixed in #1217).

## Root cause + remediation

Two layered causes:

1. **Logrotate retention was too long (or compress wasn't set)** —
   the 8.6 GB rotated syslog.1 should have aged off / compressed
   weeks ago. Suspect `/etc/logrotate.d/rsyslog` is misconfigured.

2. **No disk-usage alert at the warning threshold** — the operator
   should have been paged at 85% used, not had to discover at 100%
   via downstream symptoms.

Operational follow-ups:

- [ ] Audit `/etc/logrotate.d/rsyslog` and `postgresql-common`
- [ ] Add Prometheus alert rule on `node_filesystem_avail_bytes /
      node_filesystem_size_bytes < 0.15` for `/`
- [ ] Move WASM-audit one-time stderr captures to a dedicated dir
- [ ] Document the recovery sequence (this incident notes done):
      `docs/operations/runbooks/redis-write-blocked-disk-full.md`

## Lessons learned

- Cache-only Redis being treated like a primary store made a
  log-volume issue cascade into a customer-visible API outage.
  Consider `stop-writes-on-bgsave-error no` for our cache-only
  Redis once the disk-monitoring alert is in place — the
  trade-off is fast recovery vs. snapshot durability across
  Redis restart, and our cache is reproducible from
  `prices_1m` on demand.
- Aggregator WARNs fired loudly but the customer-visible signal
  was 404 on specific pairs. Operator-side monitoring should
  alert on aggregator WARN rate (not just service-up status).
