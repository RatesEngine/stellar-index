---
title: Bootstrap — Archival node from bare Ubuntu
last_verified: 2026-04-22
status: draft — ratified after first live run
---

# Runbook — Bootstrap an Archival Node

**Purpose:** take a fresh Hetzner box from `root@<ip>:~#` to a fully
configured Rates Engine archival node syncing pubnet. ~15 min of
Ansible + ~2–5 h background catchup.

**Pairs with:** `configs/ansible/roles/archival-node/` +
[archival-node-spec.md](../../architecture/infrastructure/archival-node-spec.md).

**Applies to:** EX63 / auction-i9-13900 / future AX Ryzen — the role
is hardware-agnostic. Different hardware means different
`zfs_data_devices` in inventory, nothing else.

---

## 0. Before you start

- [ ] You have SSH access as `root` to the new box.
- [ ] You have the box's public IP.
- [ ] The repo is checked out on your workstation at
      `~/code/ratesengine`.
- [ ] You've installed Ansible:
      `pip install --user "ansible-core>=2.16"` and
      `ansible-galaxy collection install -r configs/ansible/requirements.yml`.

---

## 1. Smoke-test SSH + inventory the hardware

First thing — verify SSH works, then list the NVMe devices so we can
put their stable IDs in inventory:

```sh
ssh root@<ip> "lsblk -d -o NAME,ROTA,TYPE,MODEL,SIZE && ls -la /dev/disk/by-id/ | grep nvme"
```

Record:
- The four big drives (`3.84 TB` or `7.68 TB` depending on box).
- Their stable `/dev/disk/by-id/nvme-*` paths (those are idempotent
  across reboots; `/dev/nvme0n1` can change).
- Confirm `ROTA` column is `0` (SSD) for all four.

If any drive is `ROTA=1` (rotational): stop. Wrong box.

---

## 2. Populate inventory + secrets

```sh
cd ~/code/ratesengine/configs/ansible
cp inventory/r1.example.yml inventory/r1.yml
$EDITOR inventory/r1.yml
```

Fill in:
- `ansible_host`: the public IP.
- `ansible_ssh_private_key_file`: typically `~/.ssh/id_ed25519`.
- `zfs_data_devices`: the four `/dev/disk/by-id/nvme-*` paths.
- `admin_ssh_keys`: contents of `~/.ssh/id_ed25519.pub`.

Then create the vault-encrypted secrets:

```sh
ansible-vault create inventory/r1.secrets.yml
```

Put in:

```yaml
postgres_pass_core: "<strong random password>"
minio_root_user:    "<admin username>"
minio_root_password: "<strong random password>"
galexie_s3_access_key: "{{ minio_root_user }}"
galexie_s3_secret_key: "{{ minio_root_password }}"
```

Generate strong passwords with `openssl rand -base64 32`.

---

## 3. Dry-run (check mode)

```sh
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml \
  --ask-vault-pass --check --diff
```

Expected: "ok" for everything that already exists, "changed" for
every task that would make a change. Error output → fix the
inventory or open an issue before applying.

---

## 4. Apply

```sh
ansible-playbook -i inventory/r1.yml playbooks/archival-node.yml \
  --ask-vault-pass
```

Runtime: ~10–20 min. Output:
- `PLAY RECAP` at the bottom shows `changed=<N> failed=0 unreachable=0`.
- If any `failed=>0` — re-read the error, fix root cause, re-apply.
  The role is idempotent.

---

## 5. Post-apply verification

Smoke tests against the running box:

```sh
ssh root@<ip>

# ZFS pool + datasets
zpool status data
zfs list -t all

# Postgres
systemctl status postgresql@15-main
sudo -u postgres psql -c "\l" | grep stellar_core

# stellar-core — will be in CATCHUP_RECENT for the next few hours
systemctl status stellar-core
journalctl -u stellar-core -n 50 --no-pager
curl -s http://127.0.0.1:11626/info | jq '.info | {ledger, state, network}'

# Galexie
systemctl status galexie
journalctl -u galexie -n 30 --no-pager

# stellar-rpc
systemctl status stellar-rpc
curl -s http://127.0.0.1:8000/ | jq

# MinIO
systemctl status minio
curl -sI http://127.0.0.1:9000/minio/health/live

# Firewall
nft list ruleset | head -40
```

Expected state immediately after bootstrap:
- **stellar-core** → `state: Catching up`, `ledger` ticking toward
  network tip.
- **galexie** → may be idle until stellar-core is caught up; that's
  fine.
- **stellar-rpc** → `Catching up` with own captive-core.
- **MinIO** → healthy, zero buckets yet.
- **Firewall** → SCP (11625) + SSH (22) open; other ports
  LAN-gated.

---

## 6. Create MinIO buckets

MinIO is up but has no buckets. Do this from the box once:

```sh
# On the box:
apt install -y mc
mc alias set local http://127.0.0.1:9000 $MINIO_ROOT_USER $MINIO_ROOT_PASSWORD
mc mb local/galexie-live
mc mb local/galexie-archive
mc mb local/backups
```

(Credentials: from `/etc/default/minio` on the box.)

Galexie will start writing ledger meta to `galexie-live` within a
few seconds.

---

## 7. Catchup timeline expectations

| Milestone | Time on EX63 / i9-auction | Monitor via |
| --------- | ------------------------- | ----------- |
| stellar-core DB initialised | ~30 s | `journalctl -u stellar-core -g "new-db"` |
| CATCHUP_RECENT starts | ~10 s after start | `curl :11626/info` → `state: Catching up` |
| Catchup complete (near tip) | 10–30 min (NVMe) / 2–4 h (SATA) | `state: Synced!` |
| Galexie exporting current ledgers | ~1 min after sync | `journalctl -u galexie -f` |
| stellar-rpc serving queries | ~10 min after sync | `curl :8000 -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}'` |

If catchup has not reached the network tip after the upper bound
above: check for peer connectivity issues (`curl :11626/peers`),
disk I/O (`iostat -xm 5`), or resource pressure (`htop`).

**Genesis-to-tip galexie backfill is a separate, much longer
phase** — the table above is just stellar-core catchup. Plan
for an additional **8–14 h** for serial galexie scan-and-fill,
or **~1.5 days** with 8-worker parallel scan-and-fill (recipe
in
[galexie-backfill.md § Tuning](../galexie-backfill.md#tuning--when-60-ledgerssec-isnt-enough)).
The galexie backfill is the long pole when budgeting bring-up
time for an archival node — see
[archival-node-spec.md § 3.3.4](../../architecture/infrastructure/archival-node-spec.md#334-galexie-backfill-time-genesis--live-tip)
for the per-tier breakdown.

---

## 8. First failures to expect (and what they mean)

### stellar-core keeps restarting

Almost always **Postgres connection refused** on first boot.
Remedy: confirm `postgres_pass_core` in the vault matches what's in
the `DATABASE` line of `/etc/stellar/stellar-core.cfg`. Re-run the
`postgres,stellar-core` tags.

### Galexie fails with "access denied" to MinIO

Buckets don't exist yet → see §6. Or access keys wrong in
`/etc/default/galexie` → re-render via
`ansible-playbook ... --tags galexie`.

### stellar-rpc SQLite locked

Two captive-cores writing the same location. Check
`CAPTIVE_CORE_STORAGE_PATH` in `stellar-rpc.cfg` — it should be
`/var/lib/stellar-rpc/captive`, distinct from stellar-core's
`/var/lib/stellar-core/buckets`.

### Firewall locked us out

The playbook applies nftables before hardening SSH. If you lose
access: reboot the box via Hetzner Robot's KVM, set
`allowed_ssh_cidrs` wider, re-run `--tags firewall`.

---

## 9. What happens next

The node is now producing ledger meta. The next work is:

1. **Galexie export observability.** Confirm the hourly checkpoint
   files land in MinIO (`mc ls local/galexie-live/`).
2. **Backup schedule.** Configure `pgBackRest` to ship WAL to
   MinIO `backups/`. Separate runbook (Week 3).
3. **Week-2 ingestion code.** `internal/sources/sdex` reads from
   Galexie's output + populates `trades`. Separate PR (in flight).
4. **Full history archive mirror + stellar-rpc genesis replay.**
   See [first-archival-node-deployment.md](first-archival-node-deployment.md)
   §4–§5 for the end-to-end sequence, including the mandatory
   burn-test checkpoint before committing to days of replay.

---

## 10. Teardown / redo

If something goes catastrophically wrong and you want a clean slate:

```sh
# On the box, as root
systemctl stop stellar-core stellar-rpc galexie minio postgresql@15-main
zpool destroy data
zpool destroy rpool || true
```

Then re-run the Ansible playbook. The role is idempotent; a clean
ZFS destroy + re-apply takes ~10 min.

---

## 11. References

- [archival-node-spec.md](../../architecture/infrastructure/archival-node-spec.md)
- [multi-region-topology.md](../../architecture/infrastructure/multi-region-topology.md)
- [validator-rollout.md](../../architecture/infrastructure/validator-rollout.md)
- [hosting-options.md](../../architecture/infrastructure/hosting-options.md)
- `configs/ansible/README.md`
