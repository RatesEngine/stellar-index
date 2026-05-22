# r1 ↔ Ansible drift audit — 2026-05-22

Full configuration-drift audit of production host **r1**
(`ratesengine-archival-r1`, 136.243.90.96) against the
`configs/ansible/roles/archival-node` role. Motivation: many r1
changes have been applied live without updating Ansible; the role
must be a faithful disaster-recovery rebuild source.

Read-only audit. Reconciliation tracked in the checklist at the
bottom.

## Verdict summary

| Surface | Status |
| --- | --- |
| `galexie.toml.j2`, `minio.env.j2`, `pg_hba.conf.j2` | IN-SYNC |
| `stellar-core.cfg.j2` → `captive-core-galexie.cfg` | IN-SYNC |
| all `files/*.sh` wrapper scripts | IN-SYNC (md5-verified) |
| `galexie*`, `minio` systemd unit templates | IN-SYNC |
| 14 of ~17 Prometheus rule files | IN-SYNC |
| `prometheus.r1.yml`, `caddy/Caddyfile.api`, `promtail.r1.yml` | IN-SYNC |
| `nftables.conf.j2` | **DRIFTED** — template won't open 80/443 (Caddy) |
| `postgresql.conf.j2` | **DRIFTED** — `shared_preload_libraries` lacks `timescaledb` |
| `ratesengine.toml.j2` | **DRIFTED** — template far behind live config |
| `sshd_config.j2` | **NEVER APPLIED** — r1 runs stock Ubuntu sshd |
| `ratesengine-indexer/aggregator.service.j2` | **DRIFTED** — wrong `EnvironmentFile` path |
| `ratesengine-api.service` | **R1-ONLY** — no role template at all |
| `captive-core-galexie-backfill.cfg` | **R1-ONLY** — no template/task |
| `verify-archive-tier-a`, `archive-completeness`, `supply-snapshot` units | **R1-ONLY** — only in `deploy/systemd/` |
| Tier-D verify-archive cron | **ANSIBLE-ONLY** — declared, never applied to r1 |
| `/etc/default/ratesengine` | **R1-ONLY** — role never creates the env file the services actually load |
| `galexie-archive.yml` Prometheus rule | **REPO-ONLY** — never deployed to r1 |
| `stellar-rpc.{cfg,service}.j2` | ANSIBLE-ONLY-STALE — inert (gated off); rpc removed 2026-04-23 |

## Detail — DRIFTED / R1-ONLY items

### 1. `sshd_config.j2` — never applied (SECURITY)
r1's `/etc/ssh/sshd_config` is the stock Ubuntu default:
`PermitRootLogin without-password`, `X11Forwarding yes`, no
`Ciphers/KexAlgorithms/MACs` hardening, no `MaxAuthTries`, no
`LoginGraceTime`, no `ClientAliveInterval`. The role's hardened
template has never been applied. `sshd_config.d/` exists, empty.

### 2. `ratesengine-api.service` — no role template
The role's `tasks/14-ratesengine-services.yml` templates indexer +
aggregator only. No `ratesengine-api.service.j2`. A rebuild
produces no public API server. (`deploy/systemd/ratesengine-api.service`
exists as an operator-copied artifact.)

### 3. EnvironmentFile mismatch
Role unit templates reference `/etc/default/ratesengine-ops`; the
running indexer/aggregator/api units on r1 load
`/etc/default/ratesengine` — which the role never creates. That
file carries `RATESENGINE_POSTGRES_DSN`, `AWS_*`, `MASSIVE_API_KEY`,
`COINGECKO_DEMO_API_KEY`, `CHAINLINK_RPC_URL`. A rebuild loses
those connector keys.

### 4. `postgresql.conf.j2` — `shared_preload_libraries`
r1 = `'timescaledb,pg_stat_statements'`; template =
`'pg_stat_statements'`. A rebuild without TimescaleDB preloaded
breaks every continuous aggregate + Timescale background job.
(`max_locks_per_transaction=4096` IS in sync.)
WAL sizing: template hardcodes 8GB/2GB; r1 `postgresql.auto.conf`
`ALTER SYSTEM`-overrides to 2GB/512MB (the running values).

### 5. `ratesengine.toml.j2` — far behind live config
Live r1 `/etc/ratesengine.toml` has, beyond the template:
13-source `enabled_sources` (template default 3),
`backfill_from_ledger`, the `s3_cold_*` block, and whole sections
the template lacks — `[oracle.*]`, `[external.*]` (+ CEX/FX
sub-tables), `[aggregate]`, `[trades]`, `[anomaly.phase2]`,
`[divergence.chainlink]`, `[api]` `trusted_proxy_cidrs` +
`prometheus_url`, expanded `[supply]`.

### 6. `captive-core-galexie-backfill.cfg` — no template
r1 has a second captive-core cfg (PEER_PORT 11727 +
`[HISTORY.localmirror]`). The role renders only the live one. No
`captive_peer_port_backfill` var, no task.

### 7. `nftables.conf.j2` — does not open 80/443
Live r1 firewall permits Caddy (80/443). The template only emits
`public_allow_ports` (SSH + optional 11625). A rebuild firewalls
off the API.

### 8. r1-only timer units
`verify-archive-tier-a`, `archive-completeness`, `supply-snapshot`
service+timer pairs live only in `deploy/systemd/` (operator-
copied), not the role. `verify-archive-tier-a.service` on r1 also
carries drop-ins (`MAX_RUNTIME=0`, `TimeoutStartSec=17h`,
`CPUWeight/IOWeight=20`) tracked nowhere. The role declares a
Tier-D cron that was never applied to r1.

### 9. `galexie-archive.yml` Prometheus rule
Present in `configs/prometheus/rules.r1/` but absent from r1's
loaded rules dir — the #31 galexie-archive tip-lag page alert is
not active.

### 10. Lower-priority / cosmetic
- healthchecks `sla-probe.sh` (r1 stale: concurrency 2 vs repo
  default 1) + `smoke.sh` (r1 stale: pre-F-1302, no `/fail`
  fan-out).
- `rules.r1/api.yml` (r1 lacks the Stripe-sync alert),
  `storage.yml`/`sla-probe.yml` comment drift, `alertmanager.r1.yml`
  comment drift, `loki` `log_level` debug-vs-warn.
- `stellar-rpc.{cfg,service}.j2` stale-but-inert — safe to delete.

## Reconciliation checklist

- [ ] §4 `postgresql.conf.j2` → add `timescaledb` to preload
- [ ] §3 add `/etc/default/ratesengine` env-file template + align unit `EnvironmentFile`
- [ ] §2 add `ratesengine-api.service.j2` + install task
- [ ] §5 backport live `ratesengine.toml` sections
- [ ] §6 add backfill captive-core cfg template + task + var
- [ ] §7 `nftables.conf.j2` → open 80/443
- [ ] §8 add verify-archive-tier-a / archive-completeness / supply-snapshot units (+ drop-ins) to the role
- [ ] §9 deploy `galexie-archive.yml` rule to r1
- [ ] §1 sshd — ROLE IS CORRECT (tasks/12-hardening.yml templates a hardened
      sshd_config). Drift is one-way: r1 itself never hardened. A rebuild
      WOULD be hardened. Action = separate r1-side security task (harden live
      sshd), needs operator sign-off — NOT an Ansible-fidelity fix.
- [ ] §10 cosmetic: refresh healthchecks scripts, rule comment drift
- [ ] delete stale `stellar-rpc.*.j2`
