---
title: Runbook — api-down
last_verified: 2026-05-03
status: draft
severity: P1
---

# Runbook — `ratesengine_api_down`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_api_down` |
| Severity | P1 (page — SEV-1) |
| Detected by | `deploy/monitoring/rules/api.yml` |
| Typical MTTR | 2–15 min |
| Impact | Complete public API outage — `/v1/price`, `/v1/history`, everything. Every customer (Freighter, Stellar.expert, the lot) sees connection errors or HAProxy's 503 page. |

## Symptoms

- `sum(up{job=~"ratesengine[_-]api"}) == 0` — every API host's `ratesengine-api`
  exporter is down for ≥ 60 s.
- `/v1/healthz` and `/v1/readyz` return non-200 (or time out) when
  HAProxy probes them on each backend.
- HAProxy reports 0/3 backends `UP` on its stats endpoint.
- The pager fires on the `severity=page` label.

## Quick diagnosis (≤ 5 min)

The API tier runs as `ratesengine-api.service` on three hosts
(`api-01..03`) behind two HAProxy hosts (`lb-01` / `lb-02`) sharing
a keepalived VIP. See [ADR-0008](../../adr/0008-ha-topology.md) §1
and [ha-plan.md §3.1](../../architecture/ha-plan.md).

```sh
# Are the units even running on each api host?
for h in api-01 api-02 api-03; do
  echo "== $h =="
  ssh root@$h "systemctl status ratesengine-api --no-pager | head -10"
done

# Why did they stop? Last 100 log lines.
ssh root@api-01 "journalctl -u ratesengine-api -n 100 --no-pager"

# HAProxy's view — which backends are UP/DOWN, and why?
ssh root@lb-01 "echo 'show servers state api_pool' | \
  socat stdio /run/haproxy/admin.sock"

# If units are RUNNING but NOT serving: probe /v1/readyz directly.
ssh root@api-01 "curl -sS http://127.0.0.1:3000/v1/readyz | jq"
```

`/v1/readyz` gates on Timescale + Redis health (handler at
`internal/api/v1/server.go::handleReadyz`; per-check
implementations in `cmd/ratesengine-api/main.go::storeChecker`
and `redisChecker`). Since wave 110 (F-1275) the handler
distinguishes critical (Postgres → 503) from non-critical
(Redis → 200 + `status="degraded"`); HAProxy only drains a
backend on the critical path. F-1277 (2026-05-13): the earlier
reference to a nonexistent `internal/api/v1/healthz.go` is
corrected. If readyz reports red:

- `timescale` red → jump to [`timescale-primary-down.md`](timescale-primary-down.md).
- `redis` red → the API serves fail-open for rate limiting and
  degraded-envelope for price; this should *not* take a host out
  of Ready. If it does, that's a bug — file it.

## Typical root causes

1. **A bad release.** A new binary fails `config.Validate()`
   (`internal/config/validate.go`) at startup and exits non-zero
   before the first request is served. systemd records the unit as
   `failed` after `StartLimitBurst=10` retries inside 5 min. The
   exit reason is in `journalctl -u ratesengine-api`.

2. **Schema migration drift.** A binary that expects a column not
   yet migrated (or migrated and dropped) fails the first query
   that touches it. `/v1/readyz` may pass (its checks are shallow);
   real traffic 500s.

3. **Credential / config-file rotation without a unit restart.**
   API reads its DSN from `/etc/ratesengine.toml` and JWT/SEP-10
   secrets from `/etc/default/ratesengine-ops` at boot. Rotating
   Redis or Postgres credentials without rolling the unit produces
   `authentication failed` log spam and unhealthy hosts.

4. **A whole API host or VIP failure.** If `api-01..03` are all on
   the same rack/PSU/switch and that fails, every backend goes
   away. The keepalived VIP failover from `lb-01` to `lb-02`
   masks an LB host failure but not a backend-pool failure.

5. **HAProxy/keepalived itself broken.** `up{job=~"ratesengine[_-]api"}` is
   scraped via Prometheus's static config pointing at each
   api host's exporter, but customer traffic flows through the
   VIP. If the VIP is down (keepalived split-brain, both LBs
   down) Prometheus may still see `up==1` on each backend while
   customers see connection refused. Cross-check against HAProxy
   logs and the keepalived state on both LBs.

## Mitigation (≤ 15 min)

- [ ] Step 1 — **declare SEV-1** in whatever incident channel you use.
      Downtime is customer-visible and a breach of our Freighter SLA.

- [ ] Step 2 — find the root cause via the diagnosis above. Do NOT
      blindly `systemctl restart ratesengine-api` on a binary that's
      crashing on startup — the restart loop will just burn the
      `StartLimitBurst` budget faster.

- [ ] Step 3 — if the **last release** is the cause: roll back per
      [`release-process.md`](../release-process.md) → Rollback. The
      API tier supports rolling rollback (drain one host at a time
      via the HAProxy admin socket; the other two carry traffic).

- [ ] Step 4 — if **config or secret** is the cause: fix the source
      (commit to `configs/` or rotate the secret in vault), run
      the relevant ansible playbook to push it out, then
      `systemctl restart ratesengine-api` on each host.

- [ ] Step 5 — if a **single host** is the cause: drain it out of
      HAProxy (`disable server api_pool/api-XX` on the admin
      socket).
      The remaining two hosts carry traffic; investigate the
      drained host without the alert pressure.

- [ ] Step 6 — if **HAProxy/keepalived** is the cause: confirm VIP
      ownership (`ip -br addr show | grep <vip>` on each LB) and
      the keepalived state (`MASTER` vs `BACKUP`). Force VIP to
      the healthy LB if split-brain.

- [ ] Verification: `up{job=~"ratesengine[_-]api"}` returns to 1 for every backend;
      `/v1/healthz` returns 200 from outside the VIP; alert clears
      within 5 min.

## Root cause analysis

Gather for the postmortem:

- `journalctl -u ratesengine-api --since "30 min ago"` from each
  affected host.
- HAProxy's backend transition log (`/var/log/haproxy.log`) for
  the same window.
- Prometheus screenshots: `up{job=~"ratesengine[_-]api"}` per instance,
  `http_requests_total`, `process_start_time_seconds` (restart
  count proxy).
- The release tag running before vs during the outage from
  `r1-deployment-state.md` and `git tag`.
- Whether this alert alone fired or it was a symptom of an upstream
  (Timescale / Redis / DC) issue.

## Known false-positive patterns

- **Single-host staging** — `for: 60s` is aggressive for a 1-host
  staging environment during a normal release rollout. Production
  runs 3 hosts so a rolling rollback never lands at 0; if you see
  this in staging during a deploy, it's expected.
- **Scrape-path breakage at the same time as a real outage.** If
  Prometheus lost network reachability to all three backends, you
  get `up == 0` identical to a real outage. Cross-check against
  HAProxy access logs (real customer traffic) and the keepalived
  state — if HAProxy is still serving 200s to clients while
  Prometheus says `up==0`, it's the scrape path, not the API.

## Related

- [`api-5xx.md`](api-5xx.md) — handlers returning errors but
  hosts healthy.
- [`api-latency.md`](api-latency.md) — slow but alive.
- [`timescale-primary-down.md`](timescale-primary-down.md),
  [`redis-master-down.md`](redis-master-down.md) — upstream
  failures that can cascade into readyz red.
- [HA plan §9](../../architecture/ha-plan.md) — degradation envelope.
- [`release-process.md`](../release-process.md) → Rollback — the
  binary-swap procedure for backing out a bad release.

## Changelog

- 2026-04-23 — initial draft. Lint-docs required a runbook for
  the page-severity alert.
- 2026-05-02 — converted from kubectl/k8s commands to systemd /
  HAProxy / journalctl, reflecting the bare-metal deployment
  ratified in ADR-0008.
