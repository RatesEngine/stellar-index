# Rates Engine — Ansible bootstrap

Config-management entrypoint for Rates Engine hosts. Today the only
role is `archival-node` — it takes a bare Ubuntu 24.04 (or 22.04)
install and brings it up as a Stellar archival node running
**Galexie + Postgres 15 + MinIO** by default, with ZFS, a locked-down
firewall, and Prometheus exporters wired in.

The role *also* contains tasks for installing **stellar-core** and
**stellar-rpc** (and the stellar-core Prometheus exporter) — but
they are gated behind `run_stellar_core` / `run_stellar_rpc`
defaults that have been **`false` since 2026-04-23**
([r1-deployment-state.md](../../docs/operations/r1-deployment-state.md)).
Production ingest reads Galexie's MinIO output directly; the two
daemons are kept for Phase-3 (Tier-1 validator rollout per
ADR-0004) and flip back to `true` per region inventory when needed.

## Prerequisites

On your workstation:

```sh
pip install --user "ansible-core>=2.16"
ansible-galaxy collection install -r configs/ansible/requirements.yml
```

On the target host: a fresh Ubuntu 24.04 LTS install — Hetzner's
standard "Ubuntu 24.04 base" image works out of the box — with SSH
reachable as `root` or a sudo-enabled user. 22.04 still works if you
have an older box around; apt-repo tasks use
`{{ ansible_distribution_release }}` so both codenames resolve
correctly.

## First-run bootstrap

```sh
cd configs/ansible

# 1. Put the host's IP + SSH key into inventory
cp inventory/r1.example.yml inventory/r1.yml
$EDITOR inventory/r1.yml        # fill in ansible_host, ansible_user, ssh_private_key_file

# 2. Run the playbook (default tag set — no stellar-core / stellar-rpc)
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml \
  --tags preflight,kernel,zfs,postgres,galexie,firewall,monitoring
# To bring up a Phase-3 validator host, set run_stellar_core: true
# (and optionally run_stellar_rpc: true) in inventory and add the
# stellar-core / stellar-rpc tags to the list above.

# 3. Watch the logs; when it finishes, SSH in and run the catchup runbook:
#    docs/operations/runbooks/bootstrap-archival-node.md
```

Runtime on a clean Hetzner EX63: ~15 minutes for config, then
2–5 hours for first `CATCHUP_RECENT` in the background.

## What the role does, at a glance

1. **Preflight** — verifies OS version, RAM ≥ 32 GB, NVMe devices
   present, no conflicting services.
2. **Kernel** — sysctl profile for high-fd services + network
   buffers; swap tuned for DB workload.
3. **ZFS** — installs `zfsutils-linux`, creates the `data` raidz2
   pool across 4 NVMe drives, creates per-workload datasets with
   workload-tuned `recordsize` + `compression=zstd`.
4. **Postgres 15** — PGDG repo, tuned for the indexer's NUMERIC-
   heavy ledger-meta workload (was originally tuned for stellar-
   core BucketListDB; defaults still safe).
5. **Galexie** — embeds its own captive-core; writes
   `FC4A....xdr.zst` objects to S3-compatible storage (local
   MinIO in the default layout). The single live captive-core
   on a default-config r1 host.
6. **MinIO** (optional) — single-node for local `galexie-live/` +
   `galexie-archive/` buckets; skipped if external S3-compatible
   target is configured.
7. **Firewall** — nftables locking inbound to a short list of
   internal ports; SCP port 11625 only opens when
   `run_stellar_core: true`.
8. **Observability** — node_exporter, promtail (Loki shipper) on
   a configurable target. `stellar-core-prometheus-exporter`
   only runs alongside stellar-core (`run_stellar_core: true`).
9. **Hardening** — SSH keys-only, fail2ban, unattended-upgrades
   for security only, auditd with CIS L2 profile.

**Phase-3 / validator hosts** (`run_stellar_core: true` and / or
`run_stellar_rpc: true`) additionally install and configure
**stellar-core** (apt.stellar.org, non-voting archival with a
Tier-1-style quorum set) and **stellar-rpc** (captive-core serving
`getEvents`, retention capped). These are off by default on r1
since 2026-04-23.

Every step is idempotent: re-running the playbook on a healthy host
should be a no-op after the initial install.

## Running a subset

Every task file has a tag matching its name. Examples:

```sh
# Just update Galexie to a new release
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml --tags galexie

# Re-template config but don't restart services
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml --tags galexie --skip-tags restart

# Dry-run everything
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml --check --diff

# Phase-3 only — re-apply stellar-core (requires run_stellar_core:
# true in inventory)
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml --tags stellar-core
```

## Secrets

Secrets (Postgres password, MinIO keys, validator HSM PIN, etc.)
live in `inventory/<region>.secrets.yml` **encrypted with ansible-
vault**. Never commit an unencrypted secrets file.

```sh
# Create once
ansible-vault create inventory/r1.secrets.yml

# Edit later
ansible-vault edit inventory/r1.secrets.yml

# Run the playbook with vault password
ansible-playbook ... --ask-vault-pass
```

## Adding a region (R2, R3 …)

Copy `inventory/r1.yml` to `inventory/r2.yml`, adjust host details,
rerun the same playbook. The role's defaults in
`roles/archival-node/defaults/main.yml` already have per-region
knobs (home_domain, peer list, MinIO endpoint) that inventory
overrides.

## Where decisions live

- Hardware spec: [`docs/architecture/infrastructure/archival-node-spec.md`](../../docs/architecture/infrastructure/archival-node-spec.md)
- Multi-region topology: [`docs/architecture/infrastructure/multi-region-topology.md`](../../docs/architecture/infrastructure/multi-region-topology.md)
- Validator promotion plan: [`docs/architecture/infrastructure/validator-rollout.md`](../../docs/architecture/infrastructure/validator-rollout.md)
- Bootstrap runbook (how to use this Ansible from scratch):
  [`docs/operations/runbooks/bootstrap-archival-node.md`](../../docs/operations/runbooks/bootstrap-archival-node.md)

## Caveats / known skeletons

This role is the **first landing**. Some tasks are stubs:

- `09-minio.yml` — single-node MinIO; HA MinIO (9-node EC) is a
  later concern.
- `12-hardening.yml` — CIS L2 auditd profile + full SSH pattern is
  TODO (filed as Week-9 hardening).
- Vault/HSM wiring (Phase B validator promotion) isn't in this role;
  it lands as a separate `validator-keys` role per the validator
  rollout plan.

See TODO markers in the task files for each.
