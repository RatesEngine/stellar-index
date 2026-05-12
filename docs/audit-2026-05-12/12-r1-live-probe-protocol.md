# R1 Live Probe Protocol

R1 is the only live deployment today. Probes go via SSH:

```sh
ssh -o ConnectTimeout=8 root@136.243.90.96 '<command>'
```

(hostname doesn't resolve; login as `root` not `ash` per memory).

Each probe is captured as a transcript file
`evidence/r1-probes/<topic>-<YYYYMMDD>.md`. Use the template at
`evidence/r1-probes/_template.md`.

## 1. Service inventory

```sh
systemctl list-units --type=service --state=running | grep -i ratesengine
systemctl --no-pager status ratesengine-api ratesengine-aggregator ratesengine-indexer
```

Expected: 3 active services. Capture: `Active`, `Main PID`, `Tasks`,
`Memory` (peak), `CPU` for each. Compare against
`docs/operations/r1-deployment-state.md` claims.

## 2. Timer inventory

```sh
systemctl list-timers --no-pager
```

Expected timers (from `deploy/systemd/`): smoke, heartbeat (×3),
SLA probe, verify-archive-tier-a, supply-snapshot,
archive-completeness. Capture last-run + next-run for each.

## 3. Listening ports

```sh
ss -tlnp
```

Expected (verified at audit kick-off): 22 (sshd), 53
(systemd-resolved), 80 + 443 + 2019 (caddy), 3000 (api),
3100 (loki), 5432 (postgres), 6379 (redis), 6061 (galexie),
9000 + 9001 (minio), 9090 (prometheus), 9100 (node_exporter),
9080 + 38563 (promtail), 9464 (indexer metrics), 9465
(aggregator metrics), 11726 (stellar-core).

Surprise: **stellar-core is still bound to 11726** despite
CLAUDE.md saying it was removed on 2026-04-23. Reconcile.

## 4. Disk + memory

```sh
df -h /var/lib/postgresql /var/lib/minio /var/lib/galexie
free -h
swapon --show
```

Initial measurement at kick-off:

- /var/lib/postgresql: 1.5T total, 40G used (3%)
- /var/lib/minio: 6.4T total, 4.9T used (78%)
- /var/lib/galexie: 1.5T total, 7.0G used (1%)
- Memory: 188G total, 179G used, 8.1G free, 47G shared
- Swap: 4.0G total, 4.0G used, 21M free

Investigate: MinIO at 78% — alert headroom? Memory at ~95% +
swap fully consumed — root cause? cause-of-death not necessarily
ratesengine; could be MinIO buffer or stellar-core.

## 5. Live access log analysis

```sh
journalctl -u ratesengine-api -n 200 --no-pager
```

Initial observations at kick-off:

- `/v1/coins/*` paths still being requested by `104.22.20.146`
  (Cloudflare IP) returning 200 with 700-1700ms latency.
  Investigate: route was just removed in rc.48 but R1 log shows
  still serving 200s. Either the rc.48 binary is not yet
  deployed, or the route deletion is only at the explorer/client
  side. Reconcile.
- `/v1/price` 404s with 200-300ms latency. Investigate: 404
  should be sub-10ms.
- `/v1/assets/{long-id}` calls taking 400-700ms. Investigate
  cache-miss path.

For deeper analysis:

```sh
journalctl -u ratesengine-api --since '1 hour ago' --no-pager | \
  jq -r '.path,.status,.latency_ms' | paste - - - | \
  awk '{ k=$1" "$2; c[k]++; t[k]+=$3 } END { for (k in c) print c[k], t[k]/c[k], k }' | sort -rn
```

Capture: top 20 routes by count, p95 latency per route, 4xx/5xx
rate per route.

## 6. Smoke test

```sh
# scripts/dev/r1-smoke.sh installed at /opt/ratesengine? (no per probe)
# fall back to scripts/healthchecks-installed copy:
ls -la /opt/ratesengine/ /usr/local/bin/r1-smoke.sh /etc/healthchecks/ 2>&1
# direct smoke against the loopback:
curl -sf http://localhost:3000/v1/healthz
```

The kick-off probe found `/opt/ratesengine/scripts/dev/r1-smoke.sh`
absent. The smoke timer is firing (`ratesengine-smoke.timer`
last ran 3min ago); investigate which script it actually invokes
(`systemctl cat ratesengine-smoke.service`).

## 7. Caddy + TLS

```sh
caddy version
journalctl -u caddy -n 50 --no-pager
# cert expiry:
echo | openssl s_client -servername api.ratesengine.net -connect 136.243.90.96:443 2>/dev/null | openssl x509 -noout -dates
```

Capture: cert expiry date, days until renewal, last error.

## 8. Postgres health

```sh
sudo -u postgres psql -c "SELECT pg_is_in_recovery(), pg_postmaster_start_time(), pg_database_size('rates') / 1024 / 1024 as db_mb;"
sudo -u postgres psql -c "SELECT name, setting FROM pg_settings WHERE name IN ('max_connections','shared_buffers','work_mem','wal_level','max_wal_size');"
sudo -u postgres psql -d rates -c "SELECT extname, extversion FROM pg_extension WHERE extname IN ('timescaledb', 'postgis');"
sudo -u postgres psql -d rates -c "SELECT count(*) FROM trades;"
sudo -u postgres psql -d rates -c "SELECT view_name, materialization_hypertable_name FROM timescaledb_information.continuous_aggregates;"
```

Check: extension version, hypertable counts, cagg refresh
freshness.

## 9. Redis health

```sh
redis-cli INFO server | grep -E 'redis_version|uptime_in_seconds|tcp_port'
redis-cli INFO clients
redis-cli INFO memory | grep -E 'used_memory_human|maxmemory_human|mem_fragmentation_ratio'
redis-cli INFO persistence | grep -E 'rdb_last_bgsave_status|aof_enabled'
redis-cli INFO replication
redis-cli DBSIZE
redis-cli ACL LIST
```

Check: ACL hygiene (no default-allow-all), persistence enabled
or not, replication configured.

## 10. MinIO + Galexie

```sh
mc admin info local
ls /var/lib/minio/galexie-archive/ | head
journalctl -u galexie -n 30 --no-pager
journalctl -u minio -n 30 --no-pager
```

Check: bucket policy, last galexie write, ledger gap.

## 11. Prometheus / Alertmanager / Loki

```sh
curl -sS http://localhost:9090/api/v1/targets | jq '.data.activeTargets[] | {labels, health, lastError}' | head -100
curl -sS http://localhost:9090/api/v1/rules | jq '.data.groups[] | {name, file, rules: [.rules[].name]}' | head -100
curl -sS http://localhost:9090/api/v1/alerts | jq '.data.alerts'
ls /etc/alertmanager/
curl -sS http://localhost:3100/ready
```

Check: targets healthy, rule files loaded, currently-firing
alerts, alertmanager config exists.

## 12. SLA probe + heartbeat truth

```sh
journalctl -u ratesengine-sla-probe -n 50 --no-pager
journalctl -u ratesengine-smoke -n 50 --no-pager
journalctl -u "ratesengine-heartbeat@*" -n 30 --no-pager
cat /var/lib/node_exporter/textfile/*.prom 2>/dev/null
# healthchecks.io endpoint URLs:
grep -r "hc-ping.com" /etc/healthchecks/ 2>/dev/null
```

Check: probe success, textfile output exists + recent, hc-ping
URL hygiene.

## Allowed R1 Observation Classes

These are the only kinds of commands an auditor may run against R1
without prior operator approval. Anything outside this list
requires an explicit ticket + operator hand-off, even when SSH
access exists.

| Class | Examples | Forbidden variants |
| --- | --- | --- |
| service health + versions | `systemctl status`, `<binary> --version` | `systemctl restart` (state-changing) |
| systemd unit listing | `systemctl list-units`, `list-timers`, `cat <unit>` | unit edits |
| logs (recent, redacted) | `journalctl -u <unit> -n 200 --no-pager` | `--since boot` (excessive volume); secrets in body must be redacted before commit |
| process arguments + env names | `ps -ef`, `cat /proc/<pid>/environ \| tr '\0' '\n' \| awk -F= '{print $1}'` | logging *secret values* — env *names* only |
| listening sockets + firewall state | `ss -tlnp`, `nft list ruleset` | port reconfiguration |
| disk / memory / CPU / IO | `df -h`, `free -h`, `top -b -n1`, `iostat`, `iotop` | filesystem mutation |
| database migration version + aggregate row counts | `psql -c "SELECT version FROM schema_migrations"`, `SELECT count(*) FROM trades` | `INSERT/UPDATE/DELETE/DROP` |
| Redis key shape + TTL samples | `redis-cli SCAN`, `TYPE`, `TTL`, `INFO` | `FLUSHDB`, `DEBUG SLEEP`, `CONFIG SET` |
| Prometheus target/alert state | `curl /api/v1/targets`, `/api/v1/rules`, `/api/v1/alerts` | rule mutation, silence creation |
| recent archive completeness + SLA probe outputs | `cat /var/lib/node_exporter/textfile/*.prom`, `journalctl -u archive-completeness` | invoking the probe ad hoc with non-default args |
| TLS cert state | `openssl s_client … \| openssl x509 -noout -dates` | cert renewal trigger |
| Caddy / Loki / MinIO / Galexie health | `caddy version`, `mc admin info`, version + status endpoints | config rewrite, bucket mutation |

**Hard prohibitions (never, regardless of class):**

- Any command that writes to DB / Redis / MinIO / disk
- Any command that reads private keys, vault contents, or
  customer PII
- Any command that pages oncall (test alerts, deadmansswitch
  fires)
- Any command that is destructive (force-restart of a service
  carrying live state, kill -9, `systemctl reset-failed`)
- `sudo su` to a non-root user that has key-signing capability

**Output discipline:**

- Redact secrets *before* writing to the transcript file
- State explicitly which fields were redacted (`X-API-Key: <redacted>`)
- Truncate noisy output; preserve only relevant lines

## Probe Ethics

- Read-only commands only; never write to DB / Redis / MinIO from
  a probe.
- If diagnosis requires a stop/start, file a finding requesting
  the probe instead of doing it.
- Never expose secrets in transcript files; scrub before commit.
- For invasive measurements (load test, fault injection),
  schedule with operator and use `test/chaos/` framework not ad
  hoc.
- R1 output is *runtime evidence only* — never the sole evidence
  for a code-correctness claim. Always pair with a code-side
  `EV-####` row.

## Probe Cadence During Audit

- §1, §2, §3 (inventory): once per audit.
- §4, §5, §11 (operational signals): re-probe each session.
- §6, §7 (smoke + TLS): once per session.
- §8, §9, §10 (data plane): once per audit, plus after any
  finding that touches storage.
- §12 (SLA + heartbeat): once per audit.

A complete probe transcript covering all 12 sections is required
before the audit can be marked terminal.
