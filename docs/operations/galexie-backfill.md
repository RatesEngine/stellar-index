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
   Monitor via `mc du local/galexie-archive` every hour.

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
