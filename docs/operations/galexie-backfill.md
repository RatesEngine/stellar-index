# Galexie backfill — genesis → live-bucket handoff

Procedure for replaying pubnet history into the `galexie-archive`
MinIO bucket on an archival node, plus the verification we run
afterwards to prove the bytes are canonical.

## Why a separate bucket

- `galexie-live` is appended to by the long-running `galexie.service`
  (one ledger every ~5 s, forever). Co-mingling historical writes
  with live-tip writes invites races and breaks the "live bucket
  tails the network tip" invariant the indexer assumes.
- `galexie-archive` is the immutable historical half: `[ledger 1,
  first-live-ledger − 1]`. Written once by a one-shot backfill job
  (see below), never again. The indexer reads from both buckets —
  contents stitch cleanly at the boundary.

## Backfill procedure

1. **Disk check** — ensure the `data` zpool has room (~2.5 TB for
   genesis-to-tip after zstd). `zfs list data/minio` + `zpool list`.
2. **MinIO user** — `galexie-writer` is scoped to `galexie-live`
   only (PR #156). Create `galexie-backfill-writer` with write
   access scoped to `galexie-archive`; revoke it after the backfill
   completes.
3. **Captive-core config** — copy
   `/etc/stellar/captive-core-galexie.cfg` to
   `captive-core-galexie-backfill.cfg`, set
   `CATCHUP_COMPLETE = true`, `CATCHUP_RECENT` unset. Use a
   distinct `PEER_PORT` so it can run alongside the live captive-
   core without collision.
4. **Galexie config** — copy `/etc/galexie/galexie.toml` to
   `galexie-backfill.toml`, change
   `datastore_config.params.destination_bucket_path = "galexie-archive/"`
   and point `captive_core_toml_path` at the new backfill cfg.
5. **Launch.** Use `galexie scan-and-fill --start 2 --end N`
   (`scan-and-fill` is **idempotent** — safe to interrupt/restart;
   picks up where it left off; only writes ledgers missing from
   the bucket). `--end` is `first-live-ledger − 1`. Run in tmux or
   as a `galexie-backfill.service` oneshot systemd unit so it
   survives SSH disconnects.
6. **Throughput expectation** — full pubnet ≈ 62 M ledgers →
   **8–14 h wall-clock** on r1-class hardware (observed
   2026-04-25 backfill run on r1: 9 h 33 m for phase 1 alone).

   **Monitoring**: r1 ships a `galexie-backfill-status` TUI at
   `/usr/local/bin/galexie-backfill-status` (history download
   progress + LCM-bucket object count + zpool I/O + process
   table). Run it under `watch`:

   ```sh
   ssh -t r1 'watch -c -n 5 galexie-backfill-status'
   ```

   For raw bucket-size sampling without ssh: `mc du
   local/galexie-archive` every hour.

   The run unfolds in three roughly-equal-cost phases (the live
   TUI shows phase 1 explicitly; phases 2 and 3 read as
   continuous LCM uploads):

   1. **History download** — captive-core fetches every
      checkpoint's bucket files from the configured peer
      archives into `/srv/history-archive`. Throughput is
      bounded by upstream archive availability (FT SCV /
      LOBSTR / SatoshiPay etc); per-archive 404s on missing
      checkpoints are normal and self-heal via fail-over.
   2. **Bucket apply** — captive-core hydrates its in-memory
      ledger state from the downloaded buckets. Brief, mostly
      CPU-bound, no significant zpool I/O.
   3. **Ledger replay + LCM upload** — captive-core replays
      ledger-by-ledger from genesis; galexie wraps each
      `LedgerCloseMeta` in a zstd-compressed `xdr.zst` object
      and uploads to `galexie-archive`. Visible as steady
      `Uploading FFFFFFFF--…/<seq>.xdr.zst` log lines + zpool
      writes near 5–10 MiB/s. Object count grows from
      checkpoint-count to per-ledger-count
      (~62 M for full pubnet at `LedgersPerFile = 1`).

## Verification tiers

Run after the backfill exits cleanly. Each tier is independent —
run the ones whose cost/evidence balance fits the situation.

### Tier A — Chain-link integrity (primary, free, mandatory)

Walks every written LCM. Asserts
`ledger[N].LedgerHeader.LedgerHash ==
ledger[N+1].LedgerHeader.PreviousLedgerHash`. Catches any internal
corruption, dropped ledger, or replay divergence regardless of
upstream trust.

Command: `ratesengine-ops verify-archive -tier chain` (PR #17).

### Tier B — Checkpoint anchoring against local history archive (primary, free, mandatory)

Every 64 ledgers, `/srv/history-archive/history/.../history-XXXXXXXX.json`
stores the canonical SCP-agreed `currentLedger.hash`. This was
produced by SDF and signed off on as canonical when the archive was
published — that's what `rs-stellar-archivist mirror` pulled into
our local disk. Assert our output's `LedgerHash` at each checkpoint
matches the archive's signed value.

**Cryptographically equivalent** to byte-comparing against SDF's
published bucket, because both derive from the same source. If
checkpoint hashes match at every 64th ledger, inter-checkpoint
content is byte-identical by induction (each ledger's hash chains to
the next).

Command: `ratesengine-ops verify-archive -tier checkpoint` (PR #18).

### Tier C — Byte-compare sample against SDF's GCS bucket (optional, belt-and-braces)

Pulls N random ledgers from `gs://sdf-ledger-close-meta/v1/ledgers/pubnet`,
decompresses both sides, byte-diffs.

Doesn't add evidence beyond Tier B (same upstream source), but:

- Documents our "re-seed from SDF on disaster recovery" capability
  works in practice.
- Catches any corruption introduced between the history archive and
  SDF's own galexie output, in the unlikely event SDF replayed
  incorrectly.
- Surfaces GCS requester-pays / egress issues before an actual DR
  event.

Command (planned): `ratesengine-ops verify-archive -tier sdf-sample --samples 1000`. Deferred pending public-read confirmation on the SDF bucket — see open items in [stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md).

Caveat: SDF's galexie bucket may not retain to genesis; check
coverage before relying on it (open item in
[stellar-data-lakes.md](../discovery/data-sources/stellar-data-lakes.md)).

### Tier D — Multi-peer checkpoint cross-validation (optional, high-evidence)

For a sampled set of checkpoints (say, 20 across the chain — ledger
63, 1M, 5M, 10M, 20M, 40M, current − 1000, etc.), fetch the
`history-XXXXXXXX.json` from **multiple** tier-1 validators'
published archives (SDF, LOBSTR, SatoshiPay, PublicNode, Blockdaemon,
Franklin Templeton) and diff the `currentLedger.hash` fields.

The tier-1 URLs are already listed on each `[[VALIDATORS]]` block
in `/etc/stellar/captive-core-galexie.cfg` — no new config needed,
just parse them out.

If N independent validators' archives all agree on the hash at
ledger K, and our replay produces the same hash, the network
agreed on those bytes via SCP consensus. Cryptographically the
strongest evidence available short of running our own validator.

Command: `ratesengine-ops verify-archive -tier peers -peer-samples
20 -peers <url>,<url>,...` (PR #20). Defaults to a built-in
seven-peer set when `-peers` is empty.

Cost: tiny — one HTTP GET per (checkpoint × peer). ~20 × 6 = 120
JSON fetches of ~1 KB each. Run on demand.

### Tier E — stellar-archivist scan of the source archive (housekeeping)

Validates `/srv/history-archive` itself is internally consistent —
that no checkpoint JSON files are corrupt, no ledger XDR blobs are
missing, no SCP messages truncated.

Wired into `verify-archive` as a tier — under the hood it shells
out to `stellar-archivist scan <url>`. Defaults to scanning the
local mirror at `file://<archive-root>`; pass `-archivist-url
https://...` to scan a peer's published archive instead.

```sh
ratesengine-ops verify-archive -config /etc/ratesengine.toml \
  -tier archivist
# or against a remote archive:
ratesengine-ops verify-archive -config /etc/ratesengine.toml \
  -tier archivist \
  -archivist-url https://history.stellar.org/prd/core-live/core_live_001
# or with the Rust port:
ratesengine-ops verify-archive -config /etc/ratesengine.toml \
  -tier archivist -archivist-bin rs-stellar-archivist
```

Run occasionally (monthly?) to make sure the mirror hasn't rotted on
disk. Also the right command to run immediately before kicking off a
backfill, just to catch any disk corruption before we build an hour
of replay work on top of it. Long-running — gated by
`-archivist-timeout` (default 30 min).

## Tuning — when 60 ledgers/sec isn't enough

The 2026-04-25 r1 backfill ran phase 3 at **~59 ledgers/sec** —
~10–25× under galexie's claimed 500–1500 ledgers/sec ceiling.
Captive-core CPU was at 1.5%, zpool had headroom, network
bandwidth peaked at 133 MiB/s. The bottleneck is the
**single-goroutine S3 PUT loop in galexie's uploader** — ~16 ms
RTT per object × 60 PUTs/sec ≈ what we observed.

Verified against `stellar/stellar-galexie@6dec23e2`
(`internal/uploader.go`'s `Run` method) and
`go-stellar-sdk:support/datastore/s3.go`'s `putFile`. There is
no `--workers` / `--upload-concurrency` flag exposed.

### Highest-impact lever: parallel `scan-and-fill` processes

Galexie's per-object `IfNoneMatch: "*"` precondition makes
overlapping writes idempotent (loser gets `PreconditionFailed`,
treats as success), so multiple processes on **disjoint** ledger
ranges are safe — no lock file or DB coordinator. Each process
needs its own captive-core `storage_path` and `admin_port` so
the LMDB buckets don't collide.

For a full pubnet backfill on r1-class hardware:

```sh
# 8 workers × ~7.7M ledgers each. CPU at 1.5% per worker means
# we have plenty of headroom for at least 8 — measure before
# pushing past 16.
mkdir -p /var/galexie/{1,2,3,4,5,6,7,8}/captive-core
for i in 1 2 3 4 5 6 7 8; do
  start=$(( 2 + (i - 1) * 7781216 ))
  end=$((   1 +  i      * 7781216 ))
  systemd-run --unit=galexie-bf-$i \
    galexie scan-and-fill \
      --config-file /etc/galexie/galexie-backfill-$i.toml \
      --start "$start" --end "$end"
done
```

…where each `galexie-backfill-$i.toml` overrides:

```toml
[stellar_core_config]
storage_path = "/var/galexie/$i/captive-core"
admin_port   = $((11725 + i))
```

Expected throughput at 8 workers: ~470 ledgers/sec → full
pubnet backfill in ~1.5 days instead of ~12.

### Other knobs (lower impact, mostly not exposed)

| Knob | Status | Notes |
| --- | --- | --- |
| `[datastore_config.schema] ledgers_per_file` | exposed but locked | Schema is persisted in the bucket manifest on first write; can't change for an existing datastore. Only relevant for greenfield buckets. Bumping from 1 → 64 divides PUT count by 64. |
| `[datastore_config.schema] files_per_partition` | exposed | Layout only, no PUT-rate impact. |
| Upload concurrency / worker count | hard-coded (single goroutine) | `internal/uploader.go` `Uploader.Run`. Patching upstream is ~15 lines of code; worth filing as a galexie issue. |
| `uploadQueueCapacity = 128` | hard-coded constant | Doesn't help when the consumer is single-threaded. |
| zstd compression level | hard-coded `SpeedDefault` (~level 3) | `support/compressxdr/compressor.go`'s `ZstdCompressor.NewWriter` — no options. Comment in galexie's `config.go` reads `// user-configurable in the future`. |
| S3 client tuning (PUT timeout, keep-alive pool, multipart) | hard-coded defaults | `s3.go` `NewS3DataStore` calls `config.LoadDefaultConfig(ctx)` with no transport overrides. |
| Multipart upload | irrelevant | `manager.NewUploader` defaults to 5 MB part size, 5 concurrent parts per upload — but parts only kick in for objects ≥ 5 MB. LCM-per-file is small. |

### Highest-impact lever (in practice): mirror from the AWS public bucket

AWS hosts a publicly-readable galexie-format Stellar dataset at
`s3://aws-public-blockchain/v1.1/stellar/ledgers/pubnet/`. For
historical backfill (genesis → live tip), we don't need to run
galexie ourselves — we can `mc mirror` / `aws s3 sync` the
public bucket into our `galexie-archive` and skip the
~12-day-serial / ~1.5-day-parallel export step entirely.

OBSRVR's `nebu` tool does exactly this in its archive mode
(per `nebu/docs/ARCHIVE_MODE.md`). The 2026-04-25 OBSRVR-fork
research confirmed they have **no parallel-write galexie fork**
— they piggy-back on the AWS public dataset for backfill and
the SDK's `NumWorkers` parallel prefetch on the read side.

#### Verified 2026-04-25

- **Retention floor: genesis.** Anonymous `aws s3 ls
  --no-sign-request` against the bucket lists partition
  `FFFFFFFF--0-63999/` (Galexie reverse-hex naming for ledgers
  0-63999) with all 63,997 expected objects (pubnet ledger
  numbering starts at 3; ledgers 0/1/2 don't exist on
  pubnet). No gaps, no missing objects. Object timestamps
  cluster around 2025-09-20 — single full backfill / re-write,
  not rolling pruning.
- **Egress: free.** The bucket is registered in the AWS Open
  Data Sponsorship Program at
  <https://registry.opendata.aws/aws-public-blockchain/>. The
  registry YAML carries the `aws-pds` tag and **no
  `RequesterPays: true` field**; anonymous reads with
  `--no-sign-request` succeed without the
  `--request-payer requester` flag (a requester-pays bucket
  would 403). The standard $0.09/GB egress does not apply.

So pulling the full ~2.5 TB pubnet archive costs **$0** in
egress. Wall-clock time depends on what fraction of the
1 Gbps link the source actually delivers, not the link's
rated speed. Observed on r1 during the 2026-04-25 cutover:
**sustained ~80 MB/s (640 Mbps)** = ~65% link utilisation,
which works out to roughly **9 h** for the full ~2.5 TB.
The "5–6 h at 1 Gbps" figure you'll see in older planning
notes assumed the link saturates; in practice S3 GETs from
us-east-2 to Hetzner FRA cap out around two-thirds of that.

#### When this is the right call

- Bringing up a brand-new archival node from cold — switch to
  mirror immediately, don't run scan-and-fill at all for the
  historical chunk.
- Disaster recovery on a corrupt `galexie-archive` — re-seed
  from AWS.
- Mid-backfill cutover (current 2026-04-25 r1 case): galexie's
  `IfNoneMatch: "*"` precondition makes the mirror idempotent
  against already-uploaded objects, so killing the in-flight
  scan-and-fill and switching to mirror is a clean cutover.

#### When it isn't

- We give up the cross-validation we'd otherwise have between
  our own captive-core export and someone else's. If the
  audit story matters more than the wall-clock cost, run our
  own scan-and-fill at least once and Tier-C compare against
  AWS afterwards (still TBD per the verify-archive playbook).
- The bucket is in `us-east-2` (Ohio); transatlantic latency
  to a European colo is real. Throughput's still bounded by
  network, not RTT, but small-object listings can feel
  slower.

#### Runbook

```sh
# 1. Stop the in-flight scan-and-fill (if any).
sudo systemctl stop galexie-backfill   # or: kill <pid>

# 2. Configure mc with anonymous AWS access.
mc alias set aws https://s3.us-east-2.amazonaws.com "" "" --api S3v4

# 3. Mirror, idempotent — won't re-write objects already in our bucket.
mc mirror --overwrite=false \
  aws/aws-public-blockchain/v1.1/stellar/ledgers/pubnet/ \
  local/galexie-archive/

# 4. Sanity-check the floor.
mc ls local/galexie-archive/FFFFFFFF--0-63999/ | head
# Expect: FFFFFFFC--3.xdr.zst, FFFFFFFB--4.xdr.zst, FFFFFFFA--5.xdr.zst, ...

# 5. Run verify-archive (Tier A + B) before declaring success:
ratesengine-ops verify-archive -config /etc/ratesengine.toml \
  -tier all -from 2 -to <last-mirrored-ledger>
```

#### Antipattern: do not substitute `mc cp --recursive` in xargs

`mc mirror --overwrite=false` is the only correct tool for step 3.
Do NOT replace it with parallel `mc cp --recursive` loops driven
from a `comm -23 aws.txt local.txt` worklist, even if the worklist
appears equivalent. Three reasons (all observed on r1 2026-04-26):

1. **`mc cp` has no skip-if-exists.** It always copies. Two parallel
   loops reading the same worklist double S3 GETs; a single loop on
   a stale worklist re-fetches partitions completed since the
   snapshot. `mc mirror`'s default *is* skip-if-exists at the object
   level — the `--overwrite` flag is opt-in.
2. **Partition-level worklists hide partial partitions.** A galexie
   partition contains 64 000 `.xdr.zst` objects. `mc ls` reports the
   directory as present even if it holds 21 307 of 64 000 — and
   `comm -23` then excludes it from the worklist. Both loops skip it
   forever. `mc mirror` walks objects, not directories, so partials
   self-heal.
3. **`mc cp --recursive partition/` is not resumable.** Killed mid-
   partition, restart re-fetches all 64 000 objects from scratch.
   `mc mirror` resumes from the missing object.

If you find an existing `xargs ... mc cp --recursive ...` running:
SIGTERM the xargs (lets in-flight copies drain naturally), wait for
`mc cp` to clear, then restart with the canonical `mc mirror`
command above. Mirror will fix any partials the cp loop left.

The verify-archive playbook's Tier C (deferred) was originally
shaped around the SDF GCS bucket. The AWS public bucket is the
same shape and accessible via S3-native APIs (no GCS auth
dance), so a future Tier C implementation should target AWS
first.

## What we do today

- **At backfill time:** Tier A + Tier B + Tier E.
- **First-pass disaster-recovery rehearsal:** Tier C once, to prove
  the path works.
- **Periodic health check:** Tier D quarterly, or any time a
  downstream price consumer reports a divergence.

## State after backfill completes

- `galexie-archive/` — 1 → `first-live-ledger − 1` (historical,
  immutable)
- `galexie-live/` — `first-live-ledger` → live tip (continuous,
  append-only)
- Indexer reads from both; seams align at the handoff ledger.
- MinIO policy: `galexie-writer` keeps write on `galexie-live`.
  `galexie-backfill-writer` user is **deleted** once the job
  exits clean (one-shot credential, no need to leave live write
  permission hanging around on an unused account).
