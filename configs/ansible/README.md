# Rates Engine — Ansible bootstrap

Config-management entrypoint for Rates Engine hosts. Today the only
role is `archival-node` — it takes a bare Ubuntu 24.04 (or 22.04)
install and brings it up as a fully configured Stellar archival node running
stellar-core, Galexie, stellar-rpc, and Postgres 15, with ZFS, a
locked-down firewall, and Prometheus exporters wired in.

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

# 2. Run the playbook
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml \
  --tags preflight,kernel,zfs,postgres,stellar-core,galexie,firewall,monitoring

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
4. **Postgres 15** — PGDG repo, tuned for stellar-core BucketListDB.
5. **stellar-core** — installed from `apt.stellar.org` (signed by
   SDF); configured as non-voting archival with a Tier-1-style
   quorum set.
6. **Galexie** — captive-core + exporter; writes to local MinIO or
   filesystem per config.
7. **stellar-rpc** — captive-core serving `getEvents`;
   retention capped.
8. **MinIO** (optional) — single-node for local `galexie-live/`
   bucket; skipped if external S3-compatible target is configured.
9. **Firewall** — nftables locking everything to SCP port 11625
   externally + a short list of internal ports.
10. **Observability** — node_exporter, stellar-core-prometheus-
    exporter, promtail (Loki shipper) on a configurable target.
11. **Hardening** — SSH keys-only, fail2ban, unattended-upgrades
    for security only, auditd with CIS L2 profile.

Every step is idempotent: re-running the playbook on a healthy host
should be a no-op after the initial install.

## Running a subset

Every task file has a tag matching its name. Examples:

```sh
# Just update stellar-core to a new apt version
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml --tags stellar-core

# Re-template config but don't restart services
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml --tags stellar-core --skip-tags restart

# Dry-run everything
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml --check --diff
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
