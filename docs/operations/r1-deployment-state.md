---
title: r1 archival node — current state and next-steps
last_verified: 2026-04-23
status: living doc
---

# r1-01 (FSN1) deployment state

Snapshot of what's running on `136.243.90.96` as of 2026-04-23. Updated
at each session.

## Hardware

- Intel Core Ultra 7 265 (20 cores — 8 P + 12 E)
- 192 GB DDR5 ECC (4 × 48 GB single-bit-ECC verified)
- 4 × 7.68 TB Samsung PM9A3 NVMe (datacenter-grade, 1 DWPD / 14 PBW)
- Hetzner dedicated, FSN1 datacenter (Falkenstein, Germany)
- Ubuntu 24.04.3 LTS noble

## Disk layout

```
nvme0n1 / nvme1n1 : OS mirror (mdadm RAID1)
                     p1  /boot/efi  512M  (per-drive vfat, not RAID)
                     p2  swap       4G    (md0, raid1)
                     p3  /          50G   (md1, raid1)
                     p4  ~7.63T     (ZFS raidz2 vdev)
nvme2n1 / nvme3n1 : untouched, full 7.68T (ZFS raidz2 vdevs)
```

ZFS pool `data` (raidz2, ~13.3 TB usable) with 7 datasets:
- `data/os` → `/var/lib/ratesengine`
- `data/postgres` → `/var/lib/postgresql` (recordsize=8K, logbias=throughput)
- `data/core` → `/var/lib/stellar-core`
- `data/rpc` → `/var/lib/stellar-rpc` (recordsize=16K)
- `data/galexie` → `/var/lib/galexie`
- `data/minio` → `/var/lib/minio`
- `data/archive` → `/srv/history-archive`

## Services (systemd)

| Service | State 2026-04-23 | Notes |
|---------|------------------|-------|
| postgresql@15-main | active | |
| ~~stellar-core~~ | **REMOVED 2026-04-23** | Primary daemon dropped — archive pipeline doesn't need it; see [archival-nodes.md](../../discovery/data-sources/archival-nodes.md) for revival path in Phase 3. |
| stellar-rpc | active, healthy | Own captive-core, ingests live from pubnet, getHealth = healthy |
| galexie | active, exporting | Own captive-core; uploading `FC4A....xdr.zst` objects to MinIO galexie-live at ~1/ledger. ~100 objects/5min at steady state. |
| minio | active | Buckets: `galexie-live`, `galexie-archive`, `backups` |
| node_exporter | active | :9100 |
| ~~stellar-core-prometheus-exporter~~ | **REMOVED 2026-04-23** | Scraped primary /info endpoint; captives don't expose one. |
| node-healthcheck.timer | active | 5-min push to Healthchecks.io UUID 4cb3daba |

## Stellar quorum set (trust anchors)

21 tier-1 validators across 7 orgs, identical to SDF's canonical
`packages/docs/examples/pubnet-validator-full/stellar-core.cfg`
fetched 2026-04-23:

- publicnode.org (3): Boötes, Lyra by BP Ventures, Hercules by OG Technologies
- lobstr.co (3): LOBSTR 1/2/5
- www.franklintempleton.com (3): FT SCV 1/2/3 *(archive frequently 404s, known upstream)*
- satoshipay.io (3): Frankfurt, Singapore, Iowa
- stellar.creit.tech (3): Creit Alpha/Beta/Gamma
- www.stellar.org (3): SDF 1/2/3
- stellar.blockdaemon.com (3): Blockdaemon Validator 1/2/3

## What's running in background

- **`stellar-archivist mirror`** in tmux session `archive-mirror`:
  pulling SDF's core_live_001 into `/srv/history-archive/`.
  Started 2026-04-23 13:26, ~467 MB after 5 min (→ ~1 TB in 3-4 h).
  Logs at `/var/log/stellar-archivist-mirror.log`.
  Check progress: `ssh root@… "tmux attach -t archive-mirror"` then
  Ctrl+B D to detach.

- **Healthchecks.io push every 5 min** reports service health +
  ledger-age + ZFS health + disk space. Currently pings `/fail` if
  stellar-rpc isn't yet `active` (expected on fresh start).

## Known gaps / next-session priorities

### Unblocked ✓
1. **Galexie PEER_PORT collision fixed — exports flowing.**
   (Updated 2026-04-23 14:03.) Earlier today galexie + stellar-rpc
   were stuck in a restart loop (152+ restarts); root cause was
   `PEER_PORT = 0` in captive-core.cfg getting stripped by the
   go-stellar-sdk toml marshaller (omitempty on zero), leaving
   stellar-core to default to pubnet's 11625 → collision with
   primary → SIGABRT. Fixed by giving each captive a distinct
   non-zero PEER_PORT (primary 11625, stellar-rpc captive 11725,
   galexie captive 11726) in separate /etc/stellar/captive-core*.cfg
   files. Commit `507e4de`. Post-fix: 0 restarts, captive-core
   reached network head at 62250034, galexie uploading one
   `.xdr.zst` object per closed ledger to `local/galexie-live/`.
   300+ objects landed within 5 min of sync. Ingestion pipeline
   is end-to-end working.

2. **SCVal decoders are stubs.** Nothing in our Go code actually
   decodes events yet. Even once stellar-rpc's DB is populated,
   `internal/sources/{soroswap,aquarius,phoenix,reflector}/decode.go`
   all return placeholder errors. This is the single biggest
   unblocker between "stack running" and "trade data flowing."
   Precondition: take a dependency on `github.com/stellar/go-stellar-sdk/xdr`
   (not yet in go.mod — callers use `stellarrpc.Event.Value` as
   opaque base64 today). The `decoderHooks` pattern in each
   decode.go is ready for real impls to replace the stubs.

### Important but not urgent
3. **Firewall + SSH hardening (phase 3)** not applied. Intentional —
   avoiding lockout risk until the box is stable. Keep KVM tab
   ready when we do.

4. **Layer-2 monitoring (Prometheus on a separate box)** is still
   TODO. Layer-1 (Healthchecks.io push) catches total-death but
   not per-metric alerts — we have 27 runbooks wired to alert
   rules nobody is evaluating yet.

5. **pgBackRest** not configured. Postgres has no backups.

6. **stellar-archivist mirror → MinIO sync.** Our mirror lands on
   ZFS (`/srv/history-archive/`), not in MinIO. Phase-1 plan
   called for `aws s3 sync` post-mirror into `galexie-archive`
   bucket for durability. TODO after mirror completes.

### Backlog (no urgency)
7. Scope Galexie to a dedicated MinIO user with bucket-scoped
   write-only policy (task #156). Right now it uses root creds.

8. Galexie's resume-from-last-ledger behaviour: wrapper currently
   restarts from `archive_tip - 128` every time. Long-term should
   probe MinIO for last-exported-ledger and resume past it.

9. stellar-rpc full-history replay (multi-day). Only valuable once
   decoders work — otherwise nothing consumes the indexed events.

## Configuration pitfalls captured during first deploy

These are all now fixed in the role, but noted so the lessons survive:

1. Captive-core runs WITH a separate primary stellar-core on one box
   → every captive-core child MUST set `HTTP_PORT=0` and a **non-zero**,
   **distinct** `PEER_PORT`. `PEER_PORT = 0` looks right but gets
   stripped by the go-stellar-sdk toml marshaller (the `PeerPort`
   field has `toml:"PEER_PORT,omitempty"` in toml.go:90), and
   stellar-core then falls back to the pubnet default 11625 — which
   the primary daemon owns. Collision manifests as
   `std::system_error(98, "Address already in use", "bind: …")` →
   SIGABRT → ingestion restart loop. Our layout: primary 11625,
   stellar-rpc captive 11725, galexie captive 11726. Every captive
   needs its OWN captive-core.cfg file since the port must differ
   per consumer. The parent must also pass
   `STELLAR_CAPTIVE_CORE_HTTP_PORT=0` to match `HTTP_PORT=0` in the
   captive file (stellar-rpc validates parent↔child agreement).

2. Galexie's config schema is `[datastore_config]` / `[stellar_core_config]`
   — not what older docs suggest. Match `config/config.example.toml`
   from the galexie source tree at the pinned tag.

3. stellar-core's config parser has no "return to root" — anything
   after `[SECTION]` is scoped to that section. Top-level directives
   (KNOWN_PEERS, NETWORK_PASSPHRASE, DATABASE, etc.) must come
   BEFORE any `[...]` or `[[...]]` block.

4. `apt-stellar-rpc`'s shipped `/etc/default/stellar-rpc` hard-codes
   futurenet. systemd's `EnvironmentFile` takes precedence over
   `--config-path`. Either blank the file, or drop `EnvironmentFile`
   from the unit. Our role does the latter.

5. Galexie's append subcommand requires an explicit `--start`
   ledger; there's no auto-discover. Wrapper must query an archive's
   `.well-known/stellar-history.json` (NOT stellar-core's live
   tip — archives lag 5-15 min) and subtract a safety margin.

6. The stellar-galexie tag format is `galexie-vX.Y.Z`, and there are
   no prebuilt binaries. Build from source via
   `go install github.com/stellar/stellar-galexie@galexie-vX.Y.Z`
   and copy (NOT symlink) out of `/root/go/bin/` into `/usr/local/bin/`
   because `/root` is mode 0700.

7. `[HISTORY.ratesengine]` (our local history-archive block with
   `put`/`mkdir` commands) must ONLY appear in the PRIMARY
   stellar-core config — never in any captive. Captive-cores that
   see `put`/`mkdir` assume they're supposed to publish checkpoints
   too; they then loop "Activating publish for ledger X" every 2s
   against an archive that hasn't been initialised (no
   `.well-known/stellar-history.json`) and ingestion stalls
   mid-ledger. Our template now gates the block on cfg_mode=='full'.

## Credentials (pointers, not values)

- Vault password: in ash's password manager
- MinIO root: in `inventory/r1.secrets.yml` (vaulted)
- Postgres stellar-core role password: same
- Healthchecks.io ping URL: in `/etc/default/node-healthcheck` on
  the box (mode 0600), also to be added to `r1.secrets.yml` via
  `ansible-vault edit`
- SSH admin key: ed25519 public key in `inventory/r1.yml`
