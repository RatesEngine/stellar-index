---
title: Archival node bring-up — end-to-end recipe
last_verified: 2026-04-27
status: living doc
---

# Archival node bring-up

End-to-end procedure for provisioning a new archival node (or
rebuilding one from disaster) from the moment the box is reachable
to the moment `ratesengine-indexer` is committing rows to
TimescaleDB. Distilled from the messy r1 bring-up of 2026-04-23 →
2026-04-27 — every step here is one we learned the hard way.

If you're recovering an existing node, jump to [§ Disaster
recovery](#disaster-recovery). Otherwise read top-down.

---

## Prerequisites

Before any ansible runs against the target host:

| Need | Where it lives | Notes |
|---|---|---|
| Provisioned host | Hetzner / equivalent | Ubuntu 24.04+, ≥ 4 NVMe drives, ≥ 192 GB RAM (per `archival-node-spec.md`) |
| Root SSH access | `inventory/<host>.yml` (`ansible_user: root`) | Hetzner installimage default; harden later, not yet |
| ansible-vault password | Operator's password manager | Won't be on disk |
| Inventory file | `configs/ansible/inventory/<host>.yml` | Copy from `r1.example.yml`, fill in disk serials + IP |
| Inventory secrets | `configs/ansible/inventory/<host>.secrets.yml` | `ansible-vault create` if new; needs `postgres_pass_*`, `minio_root_password`, `galexie_s3_*`, `ratesengine_reader_secret_key`, `ratesengine_pass_ratesengine` |
| Local Go ≥ 1.25 | Operator's machine | Required by the cross-compile step in `14-ratesengine-services.yml`. Confirm with `go version` |

The stellar-archivist binary should be available on the host —
the role doesn't install it today (Phase-1 gap; tracked under
operator follow-up). On r1 it was installed by hand from
[stellar/go-stellar-archivist](https://github.com/stellar/go-stellar-archivist).

---

## Bring-up sequence

### 1. Apply the ansible role (10–15 min wall, mostly waits)

```sh
cd configs/ansible
ansible-playbook playbooks/archival-node.yml \
  --inventory inventory/<host>.yml \
  --extra-vars "@inventory/<host>.secrets.yml" \
  --ask-vault-pass
```

This creates: ZFS pool + datasets, MinIO single-node + buckets +
IAM (`galexie-writer`, `galexie-archive-writer`,
`ratesengine-reader`), Postgres 15 + TimescaleDB extension +
`ratesengine` db/role, galexie service (live tail starts ingesting
immediately), all five `ratesengine-*` binaries cross-compiled
locally and copied up, migrations applied, indexer systemd unit
installed (initially **stopped** — see step 5).

Verify after apply:

```sh
ssh <host> 'systemctl is-active galexie minio postgresql@15-main'
# All three should print: active
ssh <host> 'mc admin user list local'
# Three users: galexie-writer, galexie-archive-writer, ratesengine-reader
ssh <host> 'mc ls local/'
# Three buckets: galexie-archive (empty), galexie-live (filling), backups
ssh <host> 'sudo -u postgres psql -d ratesengine -c "\dt"'
# trades, oracle_updates, ingestion_cursors, schema_migrations
```

Galexie should be exporting to `local/galexie-live/` already —
**capture the live-start ledger** from the running process for
step 4:

```sh
ssh <host> 'pgrep -af "galexie append"'
# /usr/local/bin/galexie append --config-file ... --start <SEAM>
```

Save that ledger number — it's the **live seam** the indexer will
use to know where the archive ends and live begins.

### 2. Mirror the SDF history archive (3–4 h wall, 7 TB)

`/srv/history-archive` is the trusted reference dataset that
`verify-archive` Tier B uses to anchor checkpoint hashes. We mirror
SDF's published archive once at bring-up.

```sh
ssh <host>
tmux new-session -d -s archive-mirror "
  set -eux
  cd /srv/history-archive
  stellar-archivist mirror \
    https://history.stellar.org/prd/core-live/core_live_001/ \
    file:///srv/history-archive/ \
    --concurrency 64 2>&1 | tee /var/log/stellar-archivist-mirror.log
"
```

Walk away for ~4 h. **Expect a fatal error count at the end** —
on r1 this completed on 2026-04-25 with `fatal: 21394 errors while
mirroring`. Those are partial-write artefacts of peer 4xx/timeouts
and need cleaning up before `verify-archive` can use the dataset.
Mandatory next step:

### 3. Sweep + heal `/srv/history-archive` (5–10 min sweep + 5 min refetch)

Find every corrupt `.gz`:

```sh
ssh <host> 'systemd-run --unit=archivist-sweep --no-block bash -c "
  find /srv/history-archive -type f -name \"*.gz\" -print0 \
    | xargs -0 -P 16 -n 50 bash -c \"
        for f in \\\$@; do
          gzip -t \\\"\\\$f\\\" 2>/dev/null || echo \\\"\\\$f\\\"
        done
      \" _ > /tmp/corrupt-gz.txt
  echo done > /tmp/sweep-done
"'

# Wait for /tmp/sweep-done to appear, then check the count:
ssh <host> 'wc -l /tmp/corrupt-gz.txt; awk -F/ "{print \$4}" /tmp/corrupt-gz.txt | sort | uniq -c'
```

On r1 the sweep found **5 193 corrupt files** distributed across
`bucket/` (2 906), `scp/` (1 023), `transactions/` (740),
`results/` (462), `ledger/` (62). Re-fetch each from upstream:

```sh
ssh <host> 'systemd-run --unit=archivist-refetch --no-block /usr/local/bin/refetch-history-archive'
# Wait for /tmp/refetch-2-done to appear, then check the result:
ssh <host> 'cat /tmp/refetch-2-summary.txt; wc -l /tmp/refetch-failed-2.txt'
```

Expect ≥ 99 % fixed. Stragglers are usually in `bucket/` and are
typically transient network errors — re-run with `--retry 3` and
lower parallelism:

```sh
ssh <host> 'PARALLEL=4 RETRIES=3 /usr/local/bin/refetch-history-archive --input /tmp/refetch-failed-2.txt'
```

Sweep again to confirm:

```sh
ssh <host> 'find /srv/history-archive -type f -name "*.gz" -print0 \
  | xargs -0 -P 16 gzip -t 2>&1 | head'
# Empty output = clean.
```

### 4. Mirror the historical Galexie data (4–6 h wall, 4.8 TB)

The historical galexie ledger-meta exists in the AWS public
blockchain bucket — mirror it directly into `galexie-archive`.
Use the per-partition tool that handles the `mc mirror` mtime
gotcha (see [galexie-backfill.md](galexie-backfill.md) for why):

```sh
ssh <host> '/usr/local/bin/galexie-archive-fill 2>&1 | tee /var/log/galexie-archive-fill.log'
```

The script audits local partitions, deletes any partials (zero on
a fresh node), computes the missing-from-AWS set, and runs
`mc mirror --skip-errors` per missing partition with 8-way
parallelism. On r1 (greenfield: 0 → 974 partitions) this ran in
**~4 h** at ~1 500 files/sec sustained.

Confirm: 974 partitions present, 4.7+ TB on disk:

```sh
ssh <host> 'mc ls local/galexie-archive/ | wc -l'
ssh <host> 'zfs list -Ho used data/minio'
```

### 5. Verify integrity (1.5–2 h wall)

```sh
ssh <host>
set -a; source /etc/default/ratesengine-ops; set +a
tmux new-window -t gbackfill -n verify-A
tmux send-keys -t gbackfill:verify-A "
ratesengine-ops verify-archive \
  -config /etc/ratesengine.toml \
  -tier all \
  -from 2 -to <SEAM-1> \
  2>&1 | tee /var/log/galexie-verify.log
" Enter
```

`<SEAM>` is the live-start ledger from step 1. Tier A walks every
ledger and confirms the hash chain links; Tier B compares each 64th
ledger's hash against the local `/srv/history-archive`; Tier E
runs `stellar-archivist scan` on the local archive.

Expected outcome: `verified <N> ledgers, chain-link integrity OK ✓,
checkpoint anchor OK ✓ (XX matched, YY missed)`. **Both Tier A and
Tier B must say OK before declaring success.**

If Tier B trips on `archive read failed: open gz stream: EOF` or
`unexpected EOF`, step 3 was incomplete — sweep + refetch the
specific failing partition's checkpoint range and resume.

### 6. Set the live seam in inventory + reapply, start the indexer (5 min)

The first apply (step 1) installed the indexer service but kept it
stopped via `LiveSeamLedger=0` (live-only mode = refuses to start
without a cursor on a fresh node). Set the real seam now:

```yaml
# inventory/<host>.yml
ratesengine_live_seam_ledger: <SEAM>          # from step 1
ratesengine_backfill_from_ledger: 2           # genesis
ratesengine_enabled_sources:
  - soroswap
  - aquarius
  - phoenix
  # add others as their per-WASM-hash audit completes
```

Re-apply just the ratesengine bits:

```sh
ansible-playbook playbooks/archival-node.yml \
  --tags ratesengine \
  --inventory inventory/<host>.yml \
  --extra-vars "@inventory/<host>.secrets.yml" \
  --ask-vault-pass
```

This re-templates `/etc/ratesengine.toml` with the seam value and
restarts `ratesengine-indexer.service`. The indexer log should
show:

```
ledgerstream: archive phase from=2 to=<SEAM-1>
... ~hours of trade/oracle inserts ...
ledgerstream: archive phase complete; handing off to live
ledgerstream: live-only seam=<SEAM>
```

Watch:

```sh
ssh <host> 'journalctl -fu ratesengine-indexer'
# In another window:
ssh <host> 'sudo -u postgres psql -d ratesengine -c "
  SELECT source, count(*), max(ts), max(ledger)
  FROM trades GROUP BY source ORDER BY 2 DESC;
"'
```

When the archive phase is done and the indexer is in live mode,
trade rows should land within ~5 s of each ledger close.

---

## Disaster recovery

Triage tree by symptom:

### Galexie service is down

```sh
ssh <host> 'systemctl status galexie -n 50'
# Common causes: captive-core PEER_PORT collision (see r1-
# deployment-state.md "Configuration pitfalls" §1), MinIO
# unreachable, archive tip stale.
```

Most galexie failures self-heal via systemd `Restart=on-failure`.
If it loops, journal will have the captive-core stderr.

### `galexie-archive` has missing or partial partitions

(e.g. someone ran `mc cp` against it and left partials, or the
bucket lost objects to disk failure.)

```sh
# Symptom: verify-archive trips on missing-or-truncated .xdr.zst.
# Identify partials with the partition-counts approach in
# /usr/local/bin/galexie-archive-fill (audit phase). For a known
# partial, just delete and re-mirror:
ssh <host> 'PARTIALS="<partition-id>" /usr/local/bin/galexie-archive-fill'
```

Never try to fix a partial partition by `mc cp --recursive`. See
[galexie-backfill.md](galexie-backfill.md) "Antipattern".

### `/srv/history-archive` has corrupt files

Same procedure as step 3 above:

```sh
# Sweep + refetch.
ssh <host> 'systemd-run --unit=sweep ... && /usr/local/bin/refetch-history-archive'
```

### Postgres is empty / wiped

```sh
# Re-run migrations:
ssh <host> 'set -a; source /etc/default/ratesengine-ops; set +a; \
  ratesengine-migrate -migrations /usr/local/share/ratesengine/migrations up'

# Ingestion cursor is gone, so the indexer needs an explicit
# starting point — set ratesengine_backfill_from_ledger: 2 in
# inventory and re-apply, then watch the archive phase replay.
```

The trades + oracle_updates hypertables will rebuild from genesis
from the existing galexie-archive data — no AWS round-trip
needed. Wall-clock: ≈ archive phase time on first bring-up.

### MinIO data dir lost

Worst case. galexie-live data is unrecoverable past the upstream
archive horizon (which is whatever the AWS bucket has — usually
within ~24 h of network tip). galexie-archive is fully recoverable
via step 4. Procedure:

1. Re-run the ansible role to re-template the buckets + IAM.
2. Run step 4 (`galexie-archive-fill`) to re-mirror from AWS.
3. Wait for galexie service to fill `galexie-live` from the
   archive tip onward (`galexie-append.sh` queries SDF's
   `.well-known/stellar-history.json` and starts there).
4. Update `ratesengine_live_seam_ledger` in inventory if galexie
   restarted at a different ledger than before — query the new
   process args.
5. Re-run migrations + restart indexer (it'll replay from genesis
   per the cursor logic).

---

## Time budget summary

| Step | Wall-clock | Bottleneck |
|---|---|---|
| 1. Ansible apply | 10–15 min | apt + go cross-compile |
| 2. stellar-archivist mirror | 3–4 h | upstream bandwidth |
| 3. Sweep + refetch | 15 min | local I/O + small re-fetches |
| 4. galexie-archive-fill | 4–6 h | AWS → us-east-2 → FRA bandwidth |
| 5. verify-archive | 1.5–2 h | local datastore read |
| 6. Indexer apply + start | 5 min | systemd |
| **End-to-end** | **~10–13 h** | mostly networks |

If any step fails partway, re-running it is idempotent — none
write twice, all skip already-complete work. Keep going.

---

## Per-region variations (R2 AWS / R3 Vultr) — per ADR-0016

The recipe above is the **R1 (Hetzner Frankfurt)** path: full local
mirror of every dataset. For the other two regions the storage shape
differs (per [ADR-0016](../adr/0016-per-region-storage-strategy.md))
and several recipe steps change or drop.

### R2 — AWS us-east-1 (galexie-direct-from-public-bucket)

R2 reads galexie ledger-meta data **directly from
`s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`**, no
local mirror. The bucket is co-located in us-east-2 (Ohio); from a
us-east-1 indexer the latency is ~5-15 ms per S3 GET, free egress
(AWS Open Data Sponsorship).

Differences from the recipe above:

- **Step 2 (stellar-archivist mirror)** — *skip*. R2 doesn't keep a
  local SDF history archive; it trusts R1's Tier B + E verification.
- **Step 3 (sweep + heal)** — *skip*. Nothing local to sweep.
- **Step 4 (galexie-archive-fill)** — *skip*. The indexer reads from
  AWS public bucket directly via galexie's `datastore_config` block:

  ```toml
  [storage]
  s3_endpoint        = "https://s3.us-east-2.amazonaws.com"
  s3_bucket_archive  = "aws-public-blockchain"
  s3_bucket_archive_prefix = "v1.1/stellar/ledgers/pubnet/"
  s3_region          = "us-east-2"
  ```

  AWS public bucket access is anonymous — no `RATESENGINE_S3_*` creds
  needed for the archive read path; galexie's S3 client falls back
  to anonymous when no credentials are configured. (`galexie-live/`
  for R2's own captive-core export still uses an authenticated
  bucket in us-east-1.)
- **Step 5 (verify-archive)** — runs `-tier chain,peers` (Tier A + D),
  not `all`. Tier A confirms R2's local chain integrity (catches
  bytes corrupted in transit from AWS). Tier D cross-validates against
  ~6 tier-1 validator archives over HTTPS (catches a forked upstream).
  No `/srv/history-archive` needed. Wall-clock ~30-45 min for
  Tier A + D.
- **Step 6 (indexer apply + start)** — same as R1 but
  `ratesengine_live_seam_ledger` in inventory points at *R2's own
  galexie-append start*, not R1's. Otherwise identical.

R2 also runs the **Tier D weekly cron** (per
`14-ratesengine-services.yml`) — same defence-in-depth as R1.

End-to-end R2 bring-up: **~1–2 h** (compute + EBS provisioning +
ansible apply + Tier A+D verify), vs ~10–13 h for R1. The
short-circuit is "no local history-archive mirror, no local galexie-
archive mirror".

### R3 — Vultr Singapore (bare-metal + Vultr Object Storage hybrid)

R3 keeps the bulk dataset (galexie-archive) on **Vultr Object Storage**
(S3-compatible, region-local at ~5-10 ms latency, ~$25/mo for the
4.76 TB), with postgres + galexie-live + OS on local NVMe.

Differences from the recipe above:

- **Step 2 (stellar-archivist mirror)** — *skip* (same as R2).
- **Step 3 (sweep + heal)** — *skip*.
- **Step 4 (galexie-archive-fill)** — runs, but writes to **Vultr
  Object Storage** rather than local MinIO. The fill script reads
  AWS public bucket and copies into Vultr's S3 endpoint. Procedure:

  ```sh
  # On r3 — set Vultr Object Storage endpoint as the destination
  mc alias set vultr-objstor https://sgp1.vultrobjects.com $VULTR_S3_KEY $VULTR_S3_SECRET
  mc alias set aws-public https://s3.us-east-2.amazonaws.com "" "" --api S3v4

  # Use the same per-partition fill helper, just point at the
  # Vultr alias as destination
  PARTIALS="" galexie-archive-fill \
    --dest vultr-objstor/galexie-archive \
    --source aws-public/aws-public-blockchain/v1.1/stellar/ledgers/pubnet/
  ```

  (The current `galexie-archive-fill` script is hardcoded to
  `local/galexie-archive` — making `--dest` configurable is a small
  ansible role tweak; track as operator follow-up if R3 is being
  brought up before that lands.)

  Wall-clock: ~6-8 h (same bandwidth as R1's fill, plus Vultr's S3
  endpoint write latency from the bare metal).
- **Step 5 (verify-archive)** — `-tier chain,peers` (Tier A + D),
  no `/srv/history-archive` needed. ~30-45 min.
- **Step 6 (indexer apply + start)** — config points the indexer's
  archive bucket at `vultr-objstor/galexie-archive` instead of the
  local MinIO bucket. `ratesengine_live_seam_ledger` is R3's own
  galexie-append start ledger.

R3 captive-core for galexie-live runs locally on the bare metal NVMe
(small footprint, ~7 GB). `galexie-live` writes go to either a small
Vultr Object Storage bucket (cheap) or a local MinIO single-node
on the bare metal's NVMe (faster, easier).

End-to-end R3 bring-up: **~7-9 h** (compute provisioning + ansible +
S3 fill + verify), most of which is the AWS-public-bucket read
+ Vultr-Object-Storage write step.

### Per-region trust + verification model

Recapping per ADR-0016:

- **R1** is the *integrity leader*. Runs all four tiers (A+B+D+E)
  on a schedule (Tier B + E weekly, Tier A nightly, Tier D weekly).
- **R2** runs Tier A + D locally (weekly via cron). Trusts R1 for
  Tier B + E.
- **R3** runs Tier A + D locally (weekly via cron). Trusts R1 for
  Tier B + E.

The **cross-region CAGG consistency monitor** (per ADR-0015's contract,
implementation pending) is the strongest check — it samples
`(pair, window, from_ts)` triples across all three regions and asserts
the closed-bucket VWAP rows are byte-identical. Failures there are
investigated immediately; the most likely cause is decoder-version
drift across regions, not raw upstream data divergence.

---

## What this doc deliberately doesn't cover

- **Phase-3 validator activation** (running our own three
  geographically-separated full validators) — see
  `docs/architecture/infrastructure/validator-rollout.md`.
- **Per-WASM-hash decoder audit** for full historical replay —
  see `docs/architecture/contract-schema-evolution.md`. Today the
  default `enabled_sources` list is conservative (soroswap +
  aquarius + phoenix) for exactly that reason.
- **HA / multi-region failover** — see `ha-plan.md`.

---

## References

- [galexie-backfill.md](galexie-backfill.md) — `mc mirror` gotcha,
  the per-partition fill helper, the antipattern that bit r1.
- [r1-deployment-state.md](r1-deployment-state.md) — current
  state of r1; configuration pitfalls captured during first
  deploy.
- [docs/architecture/ingest-pipeline.md](../architecture/ingest-pipeline.md)
  — the binding rules for the ingest path the indexer runs.
- [docs/architecture/infrastructure/archival-node-spec.md](../architecture/infrastructure/archival-node-spec.md)
  — hardware + software baseline.
