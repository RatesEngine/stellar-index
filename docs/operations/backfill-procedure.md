---
title: Backfill procedure — replaying a historical ledger range
last_verified: 2026-05-03
status: operator runbook
---

# Backfill procedure

Operator runbook for `stellarindex-ops backfill`. Use when:

- A new source is enabled; need to populate historical trades.
- A gap was discovered in the trades hypertable.
- A region is brought up later than its peers and needs to catch up.
- A source's WASM audit was completed retroactively (`BackfillSafe=true`
  was flipped in `internal/sources/external/registry.go`); historical
  rows can now be ingested.

The CLI lives at `cmd/stellarindex-ops/backfill.go`. It replays a
bounded ledger range through the same dispatcher → decoder →
sink path the live indexer uses, so output matches what the
indexer would have produced.

## What it does (and doesn't)

**Does:**
- Replays the requested range against the configured (or
  flag-overridden) source set.
- Writes one trade row per decoded event into the trades
  hypertable.
- **Force-refreshes the long-lived CAGGs (`prices_1h` /
  `prices_4h` / `prices_1d` / `prices_1w` / `prices_1mo`) over
  each chunk's timestamp range as soon as the chunk's trade-
  insert loop completes** — this is mandatory for historical
  inserts, see "Why" below. Disable with `-refresh-caggs=false`
  only when debugging a specific CAGG-refresh failure.
- Maintains its own cursor row (`source="backfill"`) so a crash
  doesn't pollute the indexer's resume position.

### Why auto-refresh matters (the May 2026 SDEX incident)

The first SDEX historical backfill (~80M trades, ledgers
6,307,178 → 50,457,423) ran May 6-11 2026 and **completed every
cursor range** — yet a week later the trades hypertable
`MIN(ledger)` for `sdex` was 61,191,617. Every backfilled trade
was lost.

Root cause: the 90-day retention policy on raw `trades`
(migration 0001) runs daily and drops chunks whose `range_end >
90d ago`. Historical inserts carry `ts` values from 2017-2024 —
those chunks are *immediately* eligible for drop. The CAGG policy
refresher runs every 30 min but only rolls forward; it doesn't
auto-backfill historical buckets when old trades are inserted. So:

  T0   : backfill inserts ~80M trades with ts = 2017-2024
  T+24h: daily retention drops every backfilled chunk
  T+30m: CAGG refresher's next tick finds no data to roll up
  ...    : 80M trades and ~5d of wall-clock work, gone

The fix landed 2026-05-13 (`feat(ops): auto-refresh CAGGs in
backfill`): the backfill tool now calls
`refresh_continuous_aggregate` over each chunk's actual ts range
immediately after the insert loop, **before** the next retention
cycle. Aggregates persist.

> **Update (migration 0031, 2026-05-14):** the 90-day retention
> policy on raw `trades` described above was REMOVED. Raw trades are
> now retained indefinitely, so the aging-out failure mode no longer
> occurs. The CAGG auto-refresh remains correct and is kept (it still
> ensures historical buckets materialize promptly), but the
> "trades age out 90 days later" outcome no longer happens.

**Doesn't:**
- Tail live ledgers — exits at `-to`.
- Pollute the indexer's `ingestion_cursors` cursor.
- Run unaudited Soroban sources. Each on-chain Soroban decoder
  is gated by `BackfillSafe` in
  `internal/sources/external/registry.go`; the backfill CLI
  refuses to run an unsafe source against a historical range.

## Prerequisites

- [ ] **Operator config validates.**
      `stellarindex-ops -config /etc/stellarindex.toml dry-run`
      (or whatever your config-validate path is).
- [ ] **All sources you'll replay have `BackfillSafe=true`** in
      `internal/sources/external/registry.go`. Soroban sources
      need a per-WASM-hash audit (`docs/operations/wasm-audits/`
      directory) before this flag flips. SDEX + off-chain are
      `BackfillSafe=true` unconditionally.
- [ ] **Galexie archive bucket reaches the requested range.**
      Today r1 has full coverage from ledger 2; r2 reads via
      `aws-public-blockchain` so any range is reachable; r3
      pulls from Vultr Object Storage. If a range is older than
      the archive bucket on a given region, run from r1.
- [ ] **Disk + DB headroom.** A ~24h backfill produces tens of
      thousands of trade rows for popular pairs; budget IO for
      the CAGG materialisation that follows insert.
- [ ] **Coordinate with the live indexer.** A backfill running
      simultaneously with live ingest is fine (they share the
      same trades hypertable + dedupe by primary key) but you
      will see a brief CPU spike. The indexer continues
      tip-tail uninterrupted.

## Step-by-step

### 1. Pick the ledger range

```sh
# Find the gap. Easiest: query the trades hypertable for
# distinct ledgers in the range you suspect missing.
psql stellarindex -c "
  SELECT min(ledger), max(ledger), count(*)
  FROM trades
  WHERE source = 'soroswap'
    AND ts BETWEEN '2026-04-15' AND '2026-04-20';
"

# Cross-reference with what was on-chain — Galexie bucket
# typically has every ledger in the range:
ssh r1 "ls /var/lib/galexie/galexie-archive/ | head -3"
```

Decide `-from` (inclusive) and `-to` (inclusive) ledger
sequences. Galexie buckets at 64-ledger granularity, so the
backfill aligns to `floor(from / 64) * 64` internally.

### 2. Dry-run first

```sh
stellarindex-ops backfill \
  -config /etc/stellarindex.toml \
  -from 50000000 \
  -to   50100000 \
  -dry-run
```

Expected output:

```
backfill dry-run:
  range:   [50000000, 50100000] (100001 ledgers)
  sources: [soroswap aquarius phoenix sdex binance]
  bucket:  galexie-archive
```

The bucket is `galexie-archive` (historical) when the range is
below the live seam; `galexie-live` when it's above. The CLI
picks automatically — you can override with `-bucket` if the
range straddles.

### 3. Run

```sh
stellarindex-ops backfill \
  -config /etc/stellarindex.toml \
  -from 50000000 \
  -to   50100000
```

Stream the output to a log so a stuck run is diagnosable:

```sh
stellarindex-ops backfill ... 2>&1 | tee backfill-50000000-50100000.log
```

Throughput in steady state: ~50-150 ledgers/second per source,
limited by Galexie XDR fetch + decode. A ~100k-ledger range
replays in ~10-30 minutes.

### 4. Resume after a crash

```sh
stellarindex-ops backfill \
  -config /etc/stellarindex.toml \
  -from 50000000 \
  -to   50100000 \
  -resume
```

`-resume` reads the prior cursor (keyed on `source="backfill"`,
`sub_source` = `"<from>-<to>:<sources>"`) and skips ledgers
already processed. The cursor row gets upserted every ~256
ledgers during the run, so crash-and-restart loses at most
that many ledgers of progress (each replayable cleanly thanks
to the trades-hypertable primary-key dedupe).

### 5. Narrow the source set (optional)

```sh
stellarindex-ops backfill \
  -config /etc/stellarindex.toml \
  -from 50000000 -to 50100000 \
  -source soroswap,phoenix
```

By default the run uses `cfg.Ingestion.EnabledSources`. Override
with `-source <csv>` for a subset — useful when only one source
is missing data.

### 6. Verify

```sh
# Trade count for the range:
psql stellarindex -c "
  SELECT source, count(*)
  FROM trades
  WHERE ledger BETWEEN 50000000 AND 50100000
  GROUP BY source
  ORDER BY 1;
"

# Spot-check the most-recent rows:
psql stellarindex -c "
  SELECT source, ledger, base_asset, quote_asset, ts
  FROM trades
  WHERE ledger BETWEEN 50000000 AND 50100000
  ORDER BY ledger DESC, ts DESC
  LIMIT 5;
"

# CAGG materialisation should auto-trigger; verify by querying:
psql stellarindex -c "
  SELECT bucket_to_ts, base_asset, quote_asset,
         vwap_quote_per_base, trade_count
  FROM prices_1m
  WHERE bucket_to_ts BETWEEN '2026-04-15' AND '2026-04-15 01:00'
    AND base_asset = 'native'
  ORDER BY bucket_to_ts
  LIMIT 5;
"
```

If the CAGGs look empty for the backfilled range, manually
refresh them:

```sql
CALL refresh_continuous_aggregate('prices_1m',
       '2026-04-15'::timestamptz, '2026-04-21'::timestamptz);
```

The default policy auto-refreshes on a 30-min cadence, so
manual refresh is rarely needed.

## Failure modes

### `BackfillSafe=false` for source X

The CLI exits with:

```
backfill: source "<X>" is BackfillSafe=false; per-WASM-hash
audit required before historical replay
```

Run the WASM audit per `docs/operations/wasm-audits/README.md`,
then flip `BackfillSafe: true` in
`internal/sources/external/registry.go`. Re-run backfill.

### Cursor collision

If two operators try to replay overlapping ranges with the same
source set, both will write to the same `(source="backfill",
sub_source=...)` cursor row. The cursor key includes
`(from, to, sources)` so non-identical ranges don't collide;
identical ones share progress. To force a fresh run, change
the source set or the range by even one ledger.

### Galexie archive missing the range

Surfaces as:

```
ledgerstream: 404 fetching FC<...>.xdr.zst
```

Confirm the range is within the bucket's coverage:

```sh
ssh r1 "ls /var/lib/galexie/galexie-archive/<partition>/" | head
```

If the archive genuinely doesn't cover the range, cross-anchor
recovery is in `docs/operations/archival-node-bringup.md`
§"Disaster recovery".

## One-year retention catch-up (F-1265)

The service targets historical retention ≥ 1 year (ideally since
inception). Pre-launch R1 only has the
prices_1m data the indexer has filled since first deploy
(~7 days at audit time 2026-05-12); `/v1/chart?timeframe=1y`
truncates accordingly. This section walks through running the
catch-up backfill so the retention target is met on launch day.

### When to run it

Once before public-flip, and any time the operator's data window
shrinks back below 1 year (e.g. after a disaster-recovery
restore that started from a more recent snapshot).

### Scope

The catch-up runs ~1 year of pubnet ledgers — at typical Stellar
cadence of 5 s/ledger that's ~6.3M ledgers. Pacing depends on
the dispatcher's parallelism + the source set; budget 6–12 hours
wall-clock on a single R1 box at `-parallel 4`.

### Plan

1. **Resolve the target window.** The audit's data point: prod
   should anchor at "1 year ago today". Compute the
   corresponding ledger sequence via the Galexie archive's
   manifest:

   ```sh
   ssh r1 'ls -1 /var/lib/galexie/galexie-archive/2025-05-* | head -1'
   # Use the first ledger in the earliest archive bucket
   # within scope. Round DOWN to a multiple of 64.
   ```

2. **Sanity-check the upstream archive.** Catch-up reads only
   from the immutable archive bucket; the live bucket isn't
   in scope. Confirm no gaps:

   ```sh
   stellarindex-ops verify-archive \
     -from <year-ago> -to <today> \
     -bucket galexie-archive
   ```

3. **Estimate the row count.** Each Soroban DEX source emits
   roughly 50–500 trades per day at recent volume; aggregator
   prices_1m row count is bounded by (pairs × minutes). A
   1-year backfill across the audited Soroban set produces
   roughly 50–200 GB of trade rows + ~10–20 GB of CAGG
   materialisation (compressed: ~5×).

4. **Run in 1-week chunks.** Don't try the whole year as a
   single `-from`/`-to`: a crash mid-run is a 12-hour resume,
   and the run holds the source-cursor row for its duration.

   ```sh
   # Adapt to your range; each chunk is ~120k ledgers.
   for week_from in $(seq -w 50000000 120000 56000000); do
     week_to=$((week_from + 120000))
     stellarindex-ops backfill \
       -config /etc/stellarindex.toml \
       -from "$week_from" -to "$week_to" \
       -resume \
       -parallel 4 2>&1 | tee "backfill-${week_from}.log"
     # Stop if the chunk failed — don't paper over.
   done
   ```

5. **(Automatic since 2026-05-13.)** Backfill auto-refreshes the
   long-lived CAGGs at the end of each chunk — no manual step
   needed. If you're running on an older binary that lacks
   `-refresh-caggs`, append this after the trade-insert loop:

   ```sh
   psql stellarindex -c "CALL refresh_continuous_aggregate('prices_1h',  NULL, NULL);"
   psql stellarindex -c "CALL refresh_continuous_aggregate('prices_4h',  NULL, NULL);"
   psql stellarindex -c "CALL refresh_continuous_aggregate('prices_1d',  NULL, NULL);"
   psql stellarindex -c "CALL refresh_continuous_aggregate('prices_1w',  NULL, NULL);"
   psql stellarindex -c "CALL refresh_continuous_aggregate('prices_1mo', NULL, NULL);"
   ```

   `prices_1m` / `prices_15m` previously had 30-day retention by
   design (migration 0002), which is why refreshing them for
   historical ranges used to be wasted work. **Migration 0031
   (2026-05-14) removed that retention — they are now retained
   indefinitely**, so refreshing them over historical ranges is no
   longer wasted; include them if you need sub-hourly history.

6. **Verify.** `/v1/chart?asset=native&quote=fiat:USD&timeframe=1y`
   should return a non-truncated point set; spot-check earliest
   bucket's timestamp.

### Failure & resumption

Each chunk runs with `-resume` so a crash mid-chunk re-anchors at
the last persisted cursor. Don't manually edit the cursor table —
that path is `stellarindex-ops backfill -resume` only.

## When NOT to use this

- **Live tail.** That's the indexer's job; backfill exits at
  `-to`.
- **Re-deriving the prices CAGG from existing trades.** Run
  `CALL refresh_continuous_aggregate(...)` directly; backfill
  re-decodes from XDR which is much heavier.
- **Source whose `BackfillSafe=false`.** Audit first (see
  `wasm-audits/README.md`). Skipping the audit risks
  silently-bad historical data per CLAUDE.md "Soroban DeFi
  contracts upgrade in place".

## Cross-references

- [`cmd/stellarindex-ops/backfill.go`](../../cmd/stellarindex-ops/backfill.go) — implementation.
- [`docs/operations/wasm-audits/README.md`](wasm-audits/README.md) — flip `BackfillSafe` once a Soroban source's WASM history is audited.
- [`internal/sources/external/registry.go`](../../internal/sources/external/registry.go) — `BackfillSafe` flag per source.
- [`migrations/0002_create_price_aggregates.up.sql`](../../migrations/0002_create_price_aggregates.up.sql) — CAGG definitions that materialise on inserted trades.
- [`docs/operations/archival-node-bringup.md`](archival-node-bringup.md) §"Disaster recovery" — when the Galexie archive itself is missing a range.
