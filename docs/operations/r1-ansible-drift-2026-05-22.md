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

- [x] §4 `postgresql.conf.j2` → add `timescaledb` to preload
      (+ WAL sizing aligned to r1's live 2GB/512MB)
- [x] §3 add `/etc/default/ratesengine` env-file template + align unit `EnvironmentFile`
- [x] §2 add `ratesengine-api.service.j2` + install task
- [x] §5 backport live `ratesengine.toml` sections
- [x] §6 add backfill captive-core cfg template + task + var
      (+ galexie-backfill.toml + /etc/default/galexie-backfill)
- [x] §7 `nftables.conf.j2` → open 80/443
- [x] §8 add verify-archive-tier-a / archive-completeness / supply-snapshot units (+ drop-ins) to the role
- [x] §9 deployed `galexie-archive.yml` to r1 — `ratesengine.galexie_archive_tip_lag` (3 rules) loaded, prometheus SIGHUP-reloaded 2026-05-22
      (NOT an Ansible-role change — handled on r1 directly by the operator)
- [ ] §1 sshd — ROLE IS CORRECT (tasks/12-hardening.yml templates a hardened
      sshd_config). Drift is one-way: r1 itself never hardened. A rebuild
      WOULD be hardened. Action = separate r1-side security task (harden live
      sshd), needs operator sign-off — NOT an Ansible-fidelity fix.
- [x] §10 rule comment drift: `deploy/monitoring/rules/storage.yml`
      max_locks comment refreshed 256 → 4096. (healthchecks-script
      refresh handled separately, per the audit scope.)
- [x] delete stale `stellar-rpc.*.j2` (+ task 08, main.yml include,
      handler, defaults — rpc removed from our architecture 2026-04-23)

## Reconciliation complete — 2026-05-22

All items closed. Ansible commits `da8d5709..2aecd9a1` on `main`.
The role now faithfully rebuilds r1. Two operator follow-ups:

1. **Vault keys.** `templates/ratesengine.env.j2` + `galexie-backfill.toml.j2`
   reference vault vars that the real (encrypted) `inventory/r1.secrets.yml`
   must define: `postgres_pass_ratesengine`,
   `vault_ratesengine_reader_secret_key`, `vault_massive_api_key`,
   `vault_coingecko_demo_api_key`, `vault_chainlink_rpc_url`,
   plus the galexie-archive-writer creds. The optional API keys have
   `| default('')`; the DSN password + reader secret do not — a
   playbook run fails loud if they're absent (correct fail-mode).
2. **§1 sshd** — the role hardens sshd correctly; r1's *live* sshd is
   still stock. Hardening live r1 is a separate r1-side change
   (touches SSH access) — tracked as its own task.

## Render-verification (2026-05-22, post-reconciliation)

Every reconciled template was rendered with `ansible-playbook`
(`-i r1.example.yml`, precedence-faithful) and diffed against r1's
live files:

| Template | Result |
| --- | --- |
| `ratesengine.toml.j2` | functionally byte-identical to live `/etc/ratesengine.toml` (only delta = the Ansible-managed header + comment placement) |
| `ratesengine-indexer/aggregator/api.service.j2` | **byte-identical** to r1's live units |
| `ratesengine.env.j2` | renders clean; all 10 keys match r1 (secret *values* are vault-sourced — unverifiable here by design) |
| `galexie-backfill.toml.j2`, backfill `captive-core.cfg` | identical to r1's live files + added explanatory comments |
| `verify-archive-tier-a` / `archive-completeness` / `supply-snapshot` service+timer | render r1's EFFECTIVE config (base unit + folded drop-ins); diffs are comments only |

Also verified: every template variable resolves (no undefined-var
runtime failure); `ansible-playbook --syntax-check` passes.

**NOT verifiable from here — the operator's acceptance gate:** a
`ansible-playbook --check --diff -i <real r1.yml> playbooks/archival-node.yml`
run against r1 with the real vault. `--check` renders every
template + reports what *would* change, non-destructively. A
faithful role shows near-zero diff. Run that before trusting the
role for an actual rebuild. Task wiring (install/enable tasks,
handlers, file modes) is syntax-checked but only a real run
executes it.
