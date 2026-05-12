# R1 Live Probe Protocol

R1 probes are read-only runtime evidence. They verify live state; they do
not replace source review.

Capture each probe in `evidence/r1-runtime.md` or a linked transcript.
Redact secrets, tokens, keys, raw customer data, and private env values.

## Probe Set

| ID | Area | Read-Only Commands | Evidence Required |
| --- | --- | --- | --- |
| R1-01 | Service inventory | `systemctl list-units --type=service --state=running`; `systemctl --no-pager status ratesengine-api ratesengine-aggregator ratesengine-indexer` | active state, PID, memory, restart policy |
| R1-02 | Timer inventory | `systemctl list-timers --no-pager` | smoke, heartbeat, SLA, archive, supply timers |
| R1-03 | Listening ports | `ss -tlnp` | exposed ports reconciled with deploy docs |
| R1-04 | Disk and memory | `df -h`; `free -h`; `swapon --show` | storage headroom and swap pressure |
| R1-05 | API logs | `journalctl -u ratesengine-api -n 200 --no-pager` | route/status/latency patterns, secrets redacted |
| R1-06 | Smoke/health | `curl -sf http://localhost:3000/v1/healthz`; service cat for smoke timers | smoke command path and health result |
| R1-07 | Caddy/TLS | `caddy version`; recent Caddy logs; certificate dates | cert expiry and proxy errors |
| R1-08 | Postgres/Timescale | read-only `psql` settings, extensions, table counts, cagg freshness | schema and freshness state |
| R1-09 | Redis | `redis-cli INFO`; `DBSIZE`; `ACL LIST` with redaction | memory, persistence, ACL, replication |
| R1-10 | MinIO/Galexie | `mc admin info`; recent logs; bucket listing shape only | archive health and headroom |
| R1-11 | Prometheus/Alertmanager/Loki | local API targets/rules/alerts/ready checks | targets, firing alerts, rule load |
| R1-12 | SLA/heartbeat | recent journals and textfile metrics | probe success and metric freshness |

## Required Reconciliations

- R1 service list vs `deploy/systemd/**` and Ansible templates.
- R1 ports vs firewall/Caddy/Prometheus docs.
- R1 logs vs current API route inventory.
- R1 migration/schema state vs `migrations/**`.
- R1 Redis state vs cache/auth/rate-limit assumptions.
- R1 alert state vs alert rules and runbooks.
- R1 disk/memory headroom vs alert thresholds.

## Prohibited During Audit Planning

- restarting services
- writing to DB/Redis/MinIO
- editing host config
- running load or chaos tests without scheduling
- collecting secret values

If invasive validation is needed, create a finding or blocked probe
entry rather than doing it ad hoc.
