---
title: Backfill procedure — replaying a historical ledger range
last_verified: 2026-05-03
status: operator runbook
---

# Backfill procedure

Operator runbook for `ratesengine-ops backfill`. Use when:

- A new source is enabled; need to populate historical trades.
- A gap was discovered in the trades hypertable.
- A region is brought up later than its peers and needs to catch up.
- A source's WASM audit was completed retroactively (`BackfillSafe=true`
  was flipped in `internal/sources/external/registry.go`); historical
  rows can now be ingested.

The CLI lives at `cmd/ratesengine-ops/backfill.go`. It replays a
bounded ledger range through the same dispatcher → decoder →
sink path the live indexer uses, so output matches what the
indexer would have produced.

## What it does (and doesn't)

**Does:**
- Replays the requested range against the configured (or
  flag-overridden) source set.
- Writes one trade row per decoded event into the trades
  hypertable.
- Lets the price-aggregate CAGGs (1m / 15m / 1h / 4h / 1d / 1w /
  1mo per migration 0002) auto-materialise on the inserted rows.
- Maintains its own cursor row (`source="backfill"`) so a crash
  doesn't pollute the indexer's resume position.

**Doesn't:**
- Tail live ledgers — exits at `-to`.
- Pollute the indexer's `ingestion_cursors` cursor.
- Run unaudited Soroban sources. Each on-chain Soroban decoder
  is gated by `BackfillSafe` in
  `internal/sources/external/registry.go`; the backfill CLI
  refuses to run an unsafe source against a historical range.

## Prerequisites

- [ ] **Operator config validates.**
      `ratesengine-ops -config /etc/ratesengine.toml dry-run`
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
psql ratesengine -c "
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
ratesengine-ops backfill \
  -config /etc/ratesengine.toml \
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
ratesengine-ops backfill \
  -config /etc/ratesengine.toml \
  -from 50000000 \
  -to   50100000
```

Stream the output to a log so a stuck run is diagnosable:

```sh
ratesengine-ops backfill ... 2>&1 | tee backfill-50000000-50100000.log
```

Throughput in steady state: ~50-150 ledgers/second per source,
limited by Galexie XDR fetch + decode. A ~100k-ledger range
replays in ~10-30 minutes.

### 4. Resume after a crash

```sh
ratesengine-ops backfill \
  -config /etc/ratesengine.toml \
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
ratesengine-ops backfill \
  -config /etc/ratesengine.toml \
  -from 50000000 -to 50100000 \
  -source soroswap,phoenix
```

By default the run uses `cfg.Ingestion.EnabledSources`. Override
with `-source <csv>` for a subset — useful when only one source
is missing data.

### 6. Verify

```sh
# Trade count for the range:
psql ratesengine -c "
  SELECT source, count(*)
  FROM trades
  WHERE ledger BETWEEN 50000000 AND 50100000
  GROUP BY source
  ORDER BY 1;
"

# Spot-check the most-recent rows:
psql ratesengine -c "
  SELECT source, ledger, base_asset, quote_asset, ts
  FROM trades
  WHERE ledger BETWEEN 50000000 AND 50100000
  ORDER BY ledger DESC, ts DESC
  LIMIT 5;
"

# CAGG materialisation should auto-trigger; verify by querying:
psql ratesengine -c "
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

- [`cmd/ratesengine-ops/backfill.go`](../../cmd/ratesengine-ops/backfill.go) — implementation.
- [`docs/operations/wasm-audits/README.md`](wasm-audits/README.md) — flip `BackfillSafe` once a Soroban source's WASM history is audited.
- [`internal/sources/external/registry.go`](../../internal/sources/external/registry.go) — `BackfillSafe` flag per source.
- [`migrations/0002_create_price_aggregates.up.sql`](../../migrations/0002_create_price_aggregates.up.sql) — CAGG definitions that materialise on inserted trades.
- [`docs/operations/archival-node-bringup.md`](archival-node-bringup.md) §"Disaster recovery" — when the Galexie archive itself is missing a range.
