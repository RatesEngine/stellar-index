# W18 — Deployment, infrastructure, ansible roles

## Scope

Every artifact that runs in production: systemd units, Docker
images, Ansible roles + playbooks, Caddy, Loki, Prometheus,
MinIO, Galexie, Postgres, Redis.

In scope:
- `deploy/systemd/*` (11 unit files)
- `deploy/docker-compose/{dev.yaml,init/}` (local dev stack +
  init scripts)
- `deploy/monitoring/{README.md,rules/}` (content audit owned
  by W14; here we cover provisioning)
- `deploy/comms/*` (incident, launch, maintenance, onboarding,
  rollback templates)
- `deploy/status-page/*` (cstate scaffold)
- `docker/*.Dockerfile` (W03 covers build; W18 covers runtime)
- `configs/ansible/{playbooks,roles,tasks}/`
- `configs/caddy/*`
- `configs/loki/*`
- `configs/prometheus/{prometheus.r1.yml,rules.r1/}`
- `configs/alertmanager/{alertmanager.r1.yml,apply.sh}`
- `configs/healthchecks/*`
- `configs/audit/wasm-walk-contracts.yaml`
- `configs/example.toml` (config schema reference)
- `configs/ansible/inventory/{r1.example.yml,r2.example.yml,r3.example.yml,r1.secrets.yml}` (inventory file truth + secret hygiene; secrets covered by W19 / F-1207)

## Inputs

- `inventory/docker-systemd-inventory.md`
- live R1 systemd state (`evidence/r1-probes/r1-baseline-2026-05-12.md`)

## Per-systemd-unit checklist

| Unit | File | Type | Wired by ansible? | Runbook references it? | Status |
| --- | --- | --- | --- | --- | --- |
| `ratesengine-api.service` | | | | | |
| `ratesengine-aggregator.service` | | | | | |
| `ratesengine-indexer.service` | | | | | |
| `archive-completeness.{service,timer}` | | | | | |
| `sla-probe.{service,timer}` | | | | | |
| `supply-snapshot.{service,timer}` | | | | | |
| `verify-archive-tier-a.{service,timer}` | | | | | |
| `ratesengine-heartbeat@.{service,timer}` (instance) | | | | | |
| `ratesengine-smoke.{service,timer}` | | | smoke.sh / r1-smoke.sh discrepancy (EV-1207) | | |
| `ratesengine-sla-probe.{service,timer}` (under configs/healthchecks) | | | | | |

## Per-ansible-role checklist

| Role | Playbook | Idempotent | Produces expected artifacts | Covers HA? | Status |
| --- | --- | --- | --- | --- | --- |
| `configs/ansible/roles/archival-node` | `playbooks/archival-node.yml` | | | | |
| `configs/ansible/roles/haproxy` | `playbooks/...` | | | (ADR-0008 / 0024) | |
| `configs/ansible/roles/loki` | | | | | |
| `configs/ansible/roles/patroni` | | | | (ADR-0008) | |
| `configs/ansible/roles/prometheus` | `playbooks/monitoring.yml` | | | | |
| `configs/ansible/roles/redis-sentinel` | | | | (ADR-0024) | |
| `configs/ansible/tasks/deploy-one-binary.yml` | invoked by `deploy.yml` | | | | |

## Caddy audit

- TLS termination via Let's Encrypt
- header injection for trusted-proxy (X-Forwarded-For from
  Cloudflare IPs)
- HSTS, X-Content-Type-Options, X-Frame-Options
- rate-limit at Caddy layer? or only at API layer?
- ACME renewal observability

## MinIO + Galexie audit

- MinIO bucket policy (private)
- access keys via env, never in plaintext config
- Galexie writes only to one bucket; multi-process write
  warning per Galexie docstring
- ADR-0002 invariant: never local filesystem
- live R1 reports MinIO 78% disk usage — alert/retention policy

## Postgres audit

- Patroni cluster topology (ADR-0008)
- WAL retention vs PITR target
- backup procedure (pgbackrest? wal-g?)
- ZFS volume management (per zfs-degraded runbook)

## Redis audit

- Sentinel topology (ADR-0024)
- maxmemory + eviction policy
- AOF + RDB
- ACL hygiene

## Comms templates

| Template | Purpose | Up to date | Status |
| --- | --- | --- | --- |
| `incident-update.md` | mid-incident comms | | |
| `launch-announcement.md` | public-flip comms | | |
| `maintenance-window.md` | scheduled maintenance | | |
| `onboarding-email.md` | new-customer welcome | | |
| `rollback-update.md` | post-rollback comms | | |

## Status page scaffold

- `deploy/status-page/` cstate scaffold
- live `web/status/` is the actual deployed page; reconcile

## Adversarial vectors

- C3.1..C3.5 network layer
- C4.1..C4.3 Galexie / MinIO chain
- E3.1..E3.3 public-flip path
- D1.3 Docker base image rebuild

## Cross-workstream dependencies

- W03 owns Dockerfile build
- W04 owns Docker base-image pin policy
- W14 owns prometheus rules + alertmanager content
- W19 owns secret management within configs
- W21 R1 live probe of everything in this workstream

## Closure criteria

- Every systemd unit row terminal
- Every ansible role row terminal
- Caddy / MinIO / Galexie / Postgres / Redis audits complete
- Comms templates each have a freshness verdict
- Status page scaffold vs live page reconciled
