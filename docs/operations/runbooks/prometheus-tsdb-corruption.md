---
title: Runbook — prometheus-down (TSDB corruption)
last_verified: 2026-05-10
status: ratified
severity: P1
---

# Runbook — `prometheus_down` (TSDB head-chunk corruption)

<!--
Written 2026-05-10 after r1's prometheus.service was found dead for
~18 h, exit code 2, with TSDB head-chunk corruption from the
2026-05-09 disk-full SEV-2. The Debian-shipped unit's
`Restart=on-abnormal` doesn't auto-recover from clean exit-codes,
so prometheus stayed down silently — meaning every metric-based
alert was deaf for 18 h.
-->

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `prometheus_down` (deadman switch) |
| Severity | P1 |
| Detected by | Healthchecks.io heartbeat on `prometheus.service`; alertmanager's deadmansswitch |
| Typical MTTR | 5 min (quick path) / 30 min (rebuild) |
| Impact | Zero metrics collected. Every metric-based alert is silently deaf. /v1/status returns degraded. Status page tier probes blind. |

## Symptoms

- `systemctl status prometheus` → `failed (Result: exit-code)`
- `journalctl -u prometheus` shows `level=error component=tsdb msg="Loading on-disk chunks failed" err="iterate on on-disk chunks: corruption in head chunk file ..."`
- alertmanager's `deadmansswitch` heartbeat stops firing
- `/v1/status` on the API returns `degraded` for the prometheus check
- nothing in the status page changes for hours (no fresh datapoints)

Almost always preceded by a disk-full or out-of-memory event during
a write to `/var/lib/prometheus/metrics2/chunks_head/`. Same root
cause family as `redis-write-blocked-disk-full` (see that runbook).

## Quick diagnosis (≤ 5 min)

```sh
# Is it actually down?
ssh root@136.243.90.96 systemctl status prometheus --no-pager | head -10

# What's the failure mode?
ssh root@136.243.90.96 journalctl -u prometheus -n 50 --no-pager | grep -E 'level=error|exited|corruption'

# How much disk is left? (was the corruption disk-full?)
ssh root@136.243.90.96 df -h /var/lib/prometheus
```

If `journalctl` shows `corruption in head chunk file …/chunks_head/000NNN`
go to **Mitigation A**. If it shows out-of-memory / WAL replay panic,
go to **Mitigation B**. If `df` shows > 95 % full, treat as
`redis-write-blocked-disk-full` first (free disk, then retry).

## Mitigation A — head-chunk corruption (≤ 5 min)

Prometheus auto-deletes corrupt head-chunk mmap files on next start.
A bare retry usually works.

- [ ] `systemctl start prometheus` on r1.
- [ ] `journalctl -u prometheus -f` for ~30 s — look for `Server is ready to receive web requests.`
- [ ] Verification: `curl -sS localhost:9090/-/ready` → `Prometheus Server is Ready.` within 30 s.

If the start fails again with the same corruption error: the bad
chunk wasn't auto-deleted. Manually remove it:

```sh
systemctl stop prometheus
ls -la /var/lib/prometheus/metrics2/chunks_head/  # find the file matching the journal error
mv /var/lib/prometheus/metrics2/chunks_head/000048 /tmp/prom-corrupt-000048-$(date +%Y%m%d).bak
systemctl start prometheus
```

You lose the in-memory data window covered by that chunk (typically
the last ~2 h of metrics). Persisted blocks (`/var/lib/prometheus/metrics2/01*`)
are untouched.

## Mitigation B — WAL or block-level corruption (≤ 30 min)

Less common; only happens when the disk-full landed mid-WAL-rewrite.

- [ ] `systemctl stop prometheus`
- [ ] Move the WAL aside: `mv /var/lib/prometheus/metrics2/wal /tmp/prom-wal-$(date +%Y%m%d).bak`
- [ ] `systemctl start prometheus` — Prometheus rebuilds an empty WAL.
- [ ] Verification: same as Mitigation A.

You lose the most-recent ~2 h of metrics. This is acceptable for an
operations-time-series DB; do **not** restore from backup unless
months of metrics matter (they usually don't — Prometheus is
snapshot data, not source of truth).

If even with the WAL gone the start still fails: nuke the entire
TSDB. Prometheus rebuilds from scratch, you lose all history but
get back to green.

```sh
systemctl stop prometheus
mv /var/lib/prometheus/metrics2 /var/lib/prometheus/metrics2.bak-$(date +%Y%m%d)
mkdir /var/lib/prometheus/metrics2
chown prometheus:prometheus /var/lib/prometheus/metrics2
systemctl start prometheus
```

## Permanent fix — make systemd auto-restart

The Debian-shipped unit (`/usr/lib/systemd/system/prometheus.service`)
has `Restart=on-abnormal`, which only restarts on signal/coredump,
NOT on regular exit-code failures. The repo's Ansible role
(`configs/ansible/roles/prometheus/templates/prometheus.service.j2`)
uses `Restart=on-failure` + `RestartSec=5s` — but that role expects
`/usr/local/bin/prometheus` (binary install), and r1 is running the
apt-installed binary at `/usr/bin/prometheus`.

Quick fix on r1 (no Ansible run needed):

```sh
mkdir -p /etc/systemd/system/prometheus.service.d
cat > /etc/systemd/system/prometheus.service.d/auto-restart.conf <<'EOF'
[Service]
Restart=on-failure
RestartSec=5s
EOF
systemctl daemon-reload
systemctl restart prometheus
```

This survives apt upgrades of the prometheus package (overrides
take precedence over the unit file).

## Root cause analysis

For a postmortem, capture:

- Last 200 lines of `journalctl -u prometheus` before the failure.
- `df -h /var/lib/prometheus` and `du -sh /var/lib/prometheus/*` to
  prove disk pressure.
- Check `/var/log/syslog` for the OOM killer if memory rather than
  disk.
- The corrupt chunk file (moved aside) — keep at least one for
  bug-report attachment to upstream Prometheus.

## Known false-positive patterns

- A normal `systemctl restart prometheus` (e.g. config reload via
  the wrong verb) fires the deadman briefly but recovers in ~5 s.
- Backup pruning that removes the deadman heartbeat target
  (`/etc/healthchecks/prometheus.uuid`) — alertmanager fires but
  prometheus is fine.

## Related

- `docs/operations/runbooks/redis-write-blocked-disk-full.md` —
  same disk-full SEV-2 family.
- `docs/operations/incidents/2026-05-09-disk-full-redis-blocked.md`
  — original incident that corrupted the TSDB.
- ADR-0002 (S3-compatible storage) — TSDB lives on local disk, so
  it's exposed to local-disk pressure events the rest of the
  pipeline isn't.

## Changelog

- 2026-05-10 — initial draft from r1's 18 h Prometheus outage
  caused by the 2026-05-09 disk-full SEV-2.
