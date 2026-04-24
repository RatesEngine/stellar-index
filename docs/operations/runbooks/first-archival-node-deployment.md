---
title: First archival-node deployment — plan from zero to replay-ready
last_verified: 2026-04-23
status: SUPERSEDED 2026-04-23 (late) — retained for historical context
superseded_by:
  - docs/operations/r1-deployment-state.md
  - docs/architecture/ecosystem-review-2026-04-23.md
---

> **⚠️ SUPERSEDED (same day as drafted).**
>
> This plan centered on `stellar-rpc` with infinite retention as the
> indexer's query surface. After running the live deploy we concluded
> stellar-rpc is redundant on our data path: our indexer will consume
> galexie's MinIO output directly via `ingest.ApplyLedgerMetadata`.
> r1 was trimmed to a single stellar-core process (galexie's captive)
> on 2026-04-23.
>
> **For current state:** [r1-deployment-state.md](../r1-deployment-state.md).
> **For architectural rationale:** [ecosystem-review-2026-04-23.md](../../architecture/ecosystem-review-2026-04-23.md).
>
> The sections below are preserved because the hardware sizing,
> ZFS layout, burn-test approach, and archive-mirror sequencing
> are still broadly right — just without the stellar-rpc-centric
> framing. Re-read with that substitution in mind.

# First archival-node deployment

**Purpose:** sequence the first Rates Engine archival node from
"Hetzner box ordered" to "our indexer replays since-inception
history into Timescale" without ever needing to wipe and start over.

**Pairs with:**
- [bootstrap-archival-node.md](bootstrap-archival-node.md) — the
  step-by-step Ansible-apply runbook (inserted as §3 below).
- [archival-node-spec.md](../../architecture/infrastructure/archival-node-spec.md)
  — hardware + OS target.
- [stellar-archivist.md](../../discovery/data-sources/stellar-archivist.md)
  — `mirror` command reference + flag semantics.

---

## The key insight (read this first)

**stellar-rpc with long retention is our replay layer.** We don't
build a separate "replay from archive" code path. Instead:

1. stellar-core + Galexie mirror the full history archive onto the
   box's local disk.
2. stellar-rpc is configured with `HISTORY_RETENTION_WINDOW=0`
   (infinite) and points at the completed archive.
3. stellar-rpc indexes every historical event into its own
   internal Postgres.
4. Our `indexer.BackfillRange` hits stellar-rpc's `getEvents` over
   localhost — the same code path that serves live tail.
5. From the indexer's perspective there's no "archive replay" vs
   "live ingestion" — it's all just `getEvents(startLedger, endLedger)`
   against a fast local RPC.

Consequence: **archive-build and indexer-code are decoupled.** The
box can run headless for weeks building the archive. Meanwhile we
finish SCVal decoders against fixture data. When both are ready,
we flip the indexer on and backfill completes at NVMe speed.

---

## 1. Decisions to freeze BEFORE ordering the box

These are the choices you cannot easily change later without
wiping + starting over. Get them right once.

### 1.1 Hardware sizing

The specific deal we're evaluating:
- Intel Core Ultra 7 265 (20 cores: 8 P + 12 E)
- 192 GB DDR5 ECC
- 4× 7.68 TB NVMe datacenter edition
- €254/mo + €99 setup + 20% VAT
- Hetzner direct (not auction)

Verify before ordering:
- [ ] NVMe drives are rated ≥ 1 DWPD. Ask Hetzner for the exact
      model number on each of the four slots; cross-check TBW on
      the datasheet. TimescaleDB compression + CAGG refreshes
      generate sustained writes — consumer NVMe burns through in
      18–24 months.
- [ ] ECC confirmed in spec sheet, not just "DDR5".
- [ ] 1 Gbps unmetered network (Hetzner standard) — required for
      the initial pubnet archive mirror (~4–5 h).

### 1.2 Disk layout (ZFS)

Decision: **raidz2 across all 4 NVMe drives** + separate OS disk
if available, else carve a small root partition off one drive.

- Raidz2 = 2 parity drives → 15.36 TB usable, survives any 2-drive
  failure (which happens; DC NVMe isn't immune to firmware
  bricks).
- Striped (raidz0) doubles usable space but a single drive loss
  destroys the archive. Not worth the risk after spending weeks
  building it.
- Mirrored pairs (raid10) = same 15.36 TB but survives only
  specific pairs of failures. Raidz2 is strictly better for our
  read-heavy / rebuild-infrequent pattern.

ZFS dataset layout (all under `data/`):
- `data/stellar-core` — captive core buckets + postgres
- `data/stellar-rpc` — rpc captive core + rpc postgres
- `data/minio` — MinIO object store (galexie-live, galexie-archive,
  history-mirror, backups)
- `data/timescale` — our own Postgres (trades + CAGGs)

Space budget (5-year projection at current pubnet growth):
| Dataset | Year 1 | Year 3 | Year 5 |
| ------- | ------ | ------ | ------ |
| stellar-core buckets | 300 GB | 600 GB | 1 TB |
| stellar-rpc postgres (full retention) | 1 TB | 2–3 TB | 3–5 TB |
| MinIO galexie + history | 1 TB | 2 TB | 3 TB |
| Timescale (our data) | 50 GB | 150 GB | 300 GB |
| **Subtotal** | **~2.4 TB** | **~5 TB** | **~8 TB** |

15.36 TB usable leaves comfortable headroom through year 5.
Re-evaluate disk topology at year 3 if growth accelerates.

### 1.3 Stellar network

Decision: **pubnet only**, period. Testnet resets periodically,
futurenet is ephemeral. We need stable data for since-inception
OHLC.

Lock this in `/etc/stellar/stellar-core.cfg`:
```
NETWORK_PASSPHRASE="Public Global Stellar Network ; September 2015"
```

### 1.4 stellar-rpc retention + historical ingestion

Two related decisions:

**Retention:** `HISTORY_RETENTION_WINDOW=0` (infinite).
Default is 7 days — events older get pruned from stellar-rpc's
Postgres. Infinite retention keeps everything we've ever ingested
queryable forever.

**Ingestion starting point:** `stellar-rpc admin ingest from-ledger 1`,
triggered as a separate command after setup (see §5). Setting
retention to 0 doesn't automatically trigger a full replay; it just
stops the pruner. We still have to explicitly kick off the
historical ingestion.

Cost of both decisions together: stellar-rpc's Postgres grows
500 GB–1 TB/year. Full genesis replay of pubnet takes 2–5 days of
wall clock on NVMe — not hours. Budget accordingly.

**Validation dependency:** don't commit to this path until the
burn test in §4.3 confirms replay actually produces usable events.
The fallback (direct Galexie XDR consumption) exists and is viable
if this doesn't work.

### 1.5 Network posture

Decisions to freeze:
- **Public IPs:** Hetzner gives one IPv4 + /64 IPv6. Use IPv6 for
  public RPC if we ever expose it; IPv4 stays for SSH.
- **SSH:** key-only, `PermitRootLogin no`, admin user via Ansible.
- **Firewall:** nftables, default-deny. Allow: SSH from our
  workstations, stellar-core SCP (port 11625) from anywhere,
  internal service ports loopback-only.
- **No Let's Encrypt / no TLS termination on the box.** We do TLS
  at the edge (future Caddy/HAProxy in a separate region).
  Node-local services stay plaintext over loopback.

### 1.6 Bootstrap catchup strategy

Decision: **CATCHUP_RECENT first, then `stellar-archivist mirror`
the full archive in parallel, NOT CATCHUP_COMPLETE.**

`CATCHUP_COMPLETE` replays every ledger from genesis, which is
slow (days) and puts all the CPU on stellar-core. It's also
unnecessary — a completed history archive on disk lets stellar-rpc
index everything without re-running consensus.

Workflow:
1. stellar-core CATCHUP_RECENT → synced to tip in ~30 min.
2. Galexie exports live ledgers from now forward.
3. Separate `stellar-archivist mirror` process pulls the entire
   public archive onto MinIO in parallel.
4. Once the archive mirror completes, stellar-rpc is configured
   to index the whole range.

This keeps the box producing USEFUL live data from hour 1, while
the historical fill happens in the background.

---

## 2. Pre-flight checklist (workstation side)

Before provisioning, confirm:

- [ ] SSH keypair exists at `~/.ssh/id_ed25519`.
- [ ] Ansible installed: `ansible --version` shows ≥ 2.16.
- [ ] Required collections: `ansible-galaxy collection install -r configs/ansible/requirements.yml`.
- [ ] `VERSIONS.md` SHAs checked — stellar-core / galexie / rpc /
      archivist versions we pin are still current.
- [ ] Secrets prepared: Postgres pass, MinIO root creds, admin
      SSH key. Generate via `openssl rand -base64 32`.

---

## 3. Provisioning + Ansible (one session)

Follow [bootstrap-archival-node.md](bootstrap-archival-node.md)
§1–§6 verbatim. That runbook covers:

- SSH smoke test + NVMe inventory (§1)
- Inventory + vault population (§2)
- Ansible dry-run (§3)
- Ansible apply (§4)
- Post-apply verification (§5)
- MinIO bucket creation (§6)

Expected state at end of §3: box has stellar-core, stellar-rpc,
galexie, minio, pg15 all installed + systemd-managed. stellar-core
is in CATCHUP_RECENT.

**Do not proceed to archive mirror until §5 verification passes.**
Specifically:
- `curl :11626/info | jq .info.state` eventually returns
  `"Synced!"` (not `"Catching up"`).
- `mc ls local/galexie-live/` shows at least one exported
  checkpoint (these land ~every 5 minutes).
- `curl :8000 -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}'`
  returns status `healthy`.

---

## 4. Archive mirror (in parallel with live data capture)

Once stellar-core is synced to tip, start the history-archive
mirror. This runs in a separate screen/tmux session; it will take
4–5 hours on 1 Gbps.

### 4.1 Primary mirror (SDF)

```sh
# On the box
tmux new -s archive-mirror
mkdir -p /data/minio/history-mirror
cd /data/minio/history-mirror

export RUST_LOG=info
stellar-archivist mirror \
  https://history.stellar.org/prd/core-live/core_live_001/ \
  file:///data/minio/history-mirror/ \
  --concurrency 64
```

Notes:
- `--concurrency 64` is the sweet spot — higher risks SDF rate
  limits; 32 is the default.
- The Rust port (`rs-stellar-archivist`, what we've pinned) has no
  `repair` subcommand — `mirror` is the only way to fill an
  archive. That's fine for a fresh seed.
- Progress: watch `du -sh /data/minio/history-mirror/` climb.
  Pubnet archive is ~1 TB as of 2026-04.

### 4.2 Secondary mirror (LOBSTR, for redundancy)

Optional but recommended — gives us a diff-check if SDF's archive
ever drifts. Run in a second tmux window:

```sh
mkdir -p /data/minio/history-mirror-lobstr
stellar-archivist mirror \
  https://stellar-history.prd.stellar.lobstr.co/ \
  file:///data/minio/history-mirror-lobstr/ \
  --concurrency 64
```

Compare after both complete:
```sh
# Checksums must match for every HAS file
find /data/minio/history-mirror -name '*.json' | \
  xargs -I{} md5sum {} | sort > /tmp/sdf.md5
find /data/minio/history-mirror-lobstr -name '*.json' | \
  xargs -I{} md5sum {} | sort > /tmp/lobstr.md5
diff /tmp/sdf.md5 /tmp/lobstr.md5  # expect empty
```

### 4.3 Burn test — verify replay produces usable events BEFORE committing

**Critical step.** Do this before the full genesis-replay in §5.

Premise: we're betting on stellar-rpc's captive-core replaying
historical transactions cleanly enough to produce the same events
that fired originally. Soroban state archival *shouldn't* break
this (historical bucket files are time-sealed and contain the
state that was live at replay time), but "shouldn't" is not
"verified." A failed burn test here saves days of wasted genesis
replay.

Pick a narrow range with known Soroswap activity:

```sh
# Soroswap mainnet launched around protocol 20, ~mid-2023.
# Pick ledger range with known swap activity — confirm via
# https://stellar.expert/explorer/public/contract/<soroswap-factory>
# which shows approximate first-swap ledgers.
BURN_START=48000000
BURN_END=48100000   # 100K ledgers = ~6 days of history = ~30 min of replay

# Tell stellar-rpc to ingest just this range
stellar-rpc admin ingest from-ledger $BURN_START --to-ledger $BURN_END

# Wait. Monitor:
journalctl -u stellar-rpc -f | grep -E "ingested|error"

# When done, confirm events are queryable
curl -s http://127.0.0.1:8000 -d "{
  \"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"getEvents\",
  \"params\":{
    \"startLedger\":$BURN_START,
    \"endLedger\":$BURN_END,
    \"filters\":[{\"type\":\"contract\",\"contractIds\":[\"CA4HEQTL23WSGTYOPPC4DAVOZRXAYMARHT55L3EJBNGHVRYZ7AW2QUBC\"]}],
    \"pagination\":{\"limit\":10}
  }}" | jq '.result.events | length, .result.events[0]'
```

Pass criteria:
- [ ] `events` array length > 0 — at least some Soroswap events came
      back.
- [ ] First event has non-empty `topic` array (at least 2 entries).
- [ ] First event has non-empty `value` (base64 SCVal).
- [ ] Decoding `topic[0]` as SCVal::Symbol produces either `"swap"`
      or `"sync"` (both are expected event kinds).

If any pass-criterion fails: **stop and investigate.** Don't run
full replay on a broken path. Likely causes + fallbacks:

- **Events missing entirely** → stellar-rpc isn't ingesting from
  the archive. Check archive URL config + replay logs. If that's
  fine, the archive may not contain the buckets for this range
  (unlikely but possible). Verify with
  `ls /data/minio/history-mirror/history/` for checkpoint files
  in range.

- **Events come back but SCVal values are empty/malformed** →
  captive-core version mismatch. stellar-rpc's bundled captive-core
  must match the protocol version of the historical ledgers.

- **Replay itself fails** → state reconstruction error. This is
  the "archive was incomplete" failure mode. Re-verify mirror
  completeness with `stellar-archivist scan` against the same
  range.

If all three fallbacks are exhausted: **architectural pivot to
reading Galexie XDR directly via `withObsrvr/stellar-extract`.**
That library consumes captive-core meta streams without needing
stellar-rpc as an intermediary. Cost: a new Source implementation
that reads from MinIO instead of stellar-rpc. Maybe a week of code.
Not fatal, but worth knowing early so we don't pretend replay
works when it doesn't.

Once burn test passes: proceed to §5.

### 4.4 Incremental top-up (ongoing)

After the initial mirror completes, keep it fresh via cron:

```sh
# /etc/cron.hourly/archivist-topup
#!/bin/bash
set -euo pipefail
LAST=$(find /data/minio/history-mirror/history -name 'HAS-*.json' | \
       sort -V | tail -1 | grep -oE '[0-9a-f]+' | head -1)
stellar-archivist mirror \
  https://history.stellar.org/prd/core-live/core_live_001/ \
  file:///data/minio/history-mirror/ \
  --low "$LAST" --concurrency 32
```

---

## 5. stellar-rpc full-history index (after burn test passes)

**Prerequisites:** §4.3 burn test passed. Archive mirror complete.
Galexie has been running live for at least 24 hours. Do NOT start
this step if §4.3 uncovered problems.

Two settings enable full-range indexing:

Edit `/etc/stellar/stellar-rpc.cfg`:
```
HISTORY_ARCHIVE_URLS=["file:///data/minio/history-mirror/"]
HISTORY_RETENTION_WINDOW=0
```

- `HISTORY_ARCHIVE_URLS` points captive-core at our local mirror
  (no network egress needed for replay).
- `HISTORY_RETENTION_WINDOW=0` means "retain everything forever"
  in stellar-rpc's Postgres — without this, events older than the
  default 7-day window get pruned even after being indexed.

Trigger the full-range ingestion:

```sh
systemctl restart stellar-rpc
# Once up, tell it to ingest from genesis forward:
stellar-rpc admin ingest from-ledger 1
```

**Expected runtime:** this is the slow step. Captive-core has to
replay every historical transaction forward from genesis. Realistic
estimate on Core Ultra + NVMe: **2–5 days** of wall clock. It's
single-captive-core-bound, so more cores don't help.

Monitor progress:
```sh
# Ledger index depth
curl -s http://127.0.0.1:8000/ -d \
  '{"jsonrpc":"2.0","id":1,"method":"getEvents","params":{"startLedger":100,"pagination":{"limit":1}}}' | \
  jq '.result.oldestLedger, .result.latestLedger'

# Postgres growth
du -sh /data/stellar-rpc/postgres/

# Replay throughput (ledgers/second)
journalctl -u stellar-rpc -f | grep -E 'ingested.*ledger'
```

**Disk watch.** stellar-rpc's Postgres grows most steeply during
historical replay (years of events compressed into days of wall
clock). Keep an eye on `df -h /data/stellar-rpc`. If it approaches
90%, pause ingestion, grow the ZFS reservation, resume.

Once `oldestLedger` reaches 1 (or the first genuinely available
ledger in the archive — some early ledgers pre-date contract
events), stellar-rpc is fully indexed.

**Parallel concern:** Galexie is still writing live meta during
this phase. stellar-rpc ingesting historical + Galexie writing
live = steady concurrent disk I/O. NVMe handles this; rotational
disks would not.

---

## 6. Validation milestones (checkpoints, not optional)

Between each phase, confirm the box is in a recoverable state.
These are the "if something's wrong, we want to know NOW, not
three days in" checkpoints.

### After §3 (Ansible apply):
- [ ] `zpool status data` → `state: ONLINE`, all 4 drives green.
- [ ] `zfs list` shows all datasets created with expected
      quotas/reservations.
- [ ] Systemd shows all 5 services (stellar-core, stellar-rpc,
      galexie, minio, postgresql@15-main) running.
- [ ] `nft list ruleset` matches expected policy.
- [ ] `ssh admin@<ip>` works (not just root).

### After §3 + catchup complete:
- [ ] `curl :11626/info | jq .info.state` → `"Synced!"`.
- [ ] Galexie has exported at least 10 checkpoints to
      `local/galexie-live/`.
- [ ] stellar-rpc healthy + serving getEvents for recent range.

### After §4 (archive mirror):
- [ ] `du -sh /data/minio/history-mirror/` ≈ expected size
      (~1 TB pubnet).
- [ ] Every HAS file checksum matches between SDF + LOBSTR
      mirrors (if both ran).
- [ ] Hourly cron top-up running; no errors in syslog.

### After §5 (stellar-rpc full index):
- [ ] stellar-rpc `getEvents(startLedger=1000)` returns data.
- [ ] stellar-rpc `getEvents(startLedger=<current_tip - 1>)`
      returns data (live + historical both work).
- [ ] stellar-rpc Postgres size is within projection.

**If any checkpoint fails, stop and fix before proceeding.** Every
later phase assumes the previous one is solid.

---

## 7. Parallel code work (weeks 1–4)

Archive mirror + stellar-rpc full index takes roughly 2–3 days of
wall clock. Meanwhile, on the workstation side:

### Decoder wiring (highest priority)
- [ ] Replace SCVal decoder stubs in
      `internal/sources/{soroswap,aquarius,phoenix,reflector}/decode.go`
      with real implementations backed by `github.com/stellar/go-stellar-sdk/xdr`.
- [ ] Pull real event fixtures from public stellar-rpc via
      `ratesengine-ops rpc-probe` → save under
      `test/fixtures/<source>/`.
- [ ] Unit-test each decoder against golden fixtures.

### Aggregator (high priority)
- [ ] Build `cmd/ratesengine-aggregator` main loop.
- [ ] VWAP/TWAP over windows from the trades hypertable → write
      to Redis hot-path keys per ADR-0007.
- [ ] Refresh cadence per granularity (matches migration 0002
      CAGG refresh intervals).

### Remaining sources (medium priority)
- [ ] SDEX — via stellar-core ledger meta (not getEvents; classic
      DEX predates Soroban).
- [ ] Comet, Blend, Redstone, Band — same five-file convention.

### Infra polish (low priority, but cheap)
- [ ] Dockerfiles for each binary (currently `make build-docker`
      fails cleanly).
- [ ] CI workflow for integration tests against testcontainers.

---

## 8. Indexer flip-switch (once decoders + archive both ready)

When stellar-rpc is fully indexed AND our decoders are tested
against fixtures:

```sh
# On the box (or wherever the indexer runs — probably same box
# for simplicity in phase 1)
systemctl start ratesengine-indexer
```

With config pointing at localhost stellar-rpc and `backfill_from_ledger`
set to 1 (genesis), the indexer replays every event via
`getEvents` paginated requests. Expected replay time: hours, not
days, because:
- getEvents is local (no network latency)
- stellar-rpc already has everything indexed
- pagination bounded at 200 events/page → indexer processes at
  decoder-throughput speed
- Our `InsertTrade` is idempotent (ON CONFLICT DO NOTHING), so
  restarts are safe.

Monitor via:
- `ratesengine_source_events_total` rate.
- `ratesengine_cursor_last_ledger` climbing toward tip.
- `ratesengine_source_insert_errors_total` zero.

Once the cursor reaches live tip, backfill is complete. The
indexer continues consuming live events from stellar-rpc
indefinitely.

---

## 9. If things go wrong

### During catchup (§3)

Most issues are transient peer connectivity. Check `curl :11626/peers`.
If pathological: re-run Ansible with `--tags stellar-core` to
re-render config.

### During mirror (§4)

SDF or LOBSTR rate-limited → reduce `--concurrency` to 32 or 16.
Network blip → `stellar-archivist mirror` is idempotent with
`--overwrite`; just re-run.

### During stellar-rpc full index (§5)

Postgres disk pressure — check `df -h /data/stellar-rpc`. If
approaching full: pause indexing, add dataset quota headroom,
resume.

### During backfill (§8)

Decoder errors spike → `ratesengine_source_decode_errors_total`
rises. Look at indexer logs; usually means we hit an event shape
the fixture tests didn't cover. Pause indexer, fix decoder, restart
(backfill resumes from last persisted cursor).

---

## 10. When it's time to wipe (and how to avoid it)

Reasons you'd actually want to wipe:
- **ZFS layout wrong.** Unrecoverable — redo §1.2.
- **Pinned on testnet instead of pubnet.** Redo from §1.3.
- **Drive hardware failure.** Raidz2 survives 2 simultaneous; ask
  Hetzner for a swap.
- **Archive mirror corrupted.** Delete + re-mirror from §4.1. Does
  NOT require rebuilding stellar-core state.

Reasons that DO NOT require wiping:
- **Code bug in our decoder.** Drop + rerun migration 0001, keep
  everything else. Indexer re-populates.
- **TimescaleDB schema change.** Migrations handle it.
- **stellar-rpc Postgres corruption.** Rebuild via re-index from
  archive (multi-hour, not multi-day).
- **MinIO credential change.** Rotate keys in vault + re-render
  config.

The `archival-node` Ansible role is idempotent. Running it again
is always safe; it only changes what's drifted.

---

## 11. References

- [bootstrap-archival-node.md](bootstrap-archival-node.md) — §3 detail.
- [stellar-archivist.md](../../discovery/data-sources/stellar-archivist.md) — mirror command reference.
- [galexie.md](../../discovery/data-sources/galexie.md) — how Galexie writes to S3.
- [archival-node-spec.md](../../architecture/infrastructure/archival-node-spec.md) — hardware + OS target.
- ADR-0002 — MinIO S3-compat (why not local filesystem).
- ADR-0004 — Tier-1 validator aspiration.
