---
title: FX history empty / fx_quotes table missing — apply migration 0028 + backfill
last_verified: 2026-05-10
status: living procedure
---

# FX history empty / `fx_quotes` table missing

Companion to [`db-disk-full.md`](db-disk-full.md) and
[`redis-write-blocked-disk-full.md`](redis-write-blocked-disk-full.md).
Different shape: a database migration that ships in the repo
(0028) but hasn't been applied to the deployment, so a feature
that depends on the new table fails silently at runtime.

This runbook captures the 2026-05-10 finding on r1 + the recovery
sequence so future operators don't re-investigate from
"`/currencies/EUR.history_1y` returns 0 points" backwards.

## Signal

- `/v1/currencies/EUR` (or any other ticker) returns
  `history_1y: 0` and `history_all: 0` on the wire while
  `history_7d` populates normally.
- API log shows recurring WARN every forex refresh tick:
  ```
  {"level":"WARN","msg":"forex: fx_quotes persist failed",
   "rows":810,
   "err":"timescale: InsertFXQuoteBatch ticker=\"AED\":
          pq: relation \"fx_quotes\" does not exist
          at position 2:15 (42P01)"}
  ```
- `psql -tA -c "SELECT to_regclass('public.fx_quotes')"` returns
  empty.
- `psql -tA -c "SELECT version FROM schema_migrations
  ORDER BY version DESC LIMIT 1"` returns 27 (or older).

## Why this happens

The `fx_quotes` hypertable was added in PR #1041 (task #104,
"Persistent fx_quotes hypertable + 10y backfill") via migration
0028. The migration ships in the repo at
`migrations/0028_create_fx_quotes.up.sql`. Two operator-side
steps make it live on a deployment:

1. **Copy the migration file** to the deployment's migrations
   directory (`/var/lib/ratesengine/migrations/` on r1).
2. **Apply it** via `ratesengine-migrate up`.

Once the table exists, the forex worker (running inside
`ratesengine-api`) starts persisting on its next refresh tick,
so live data backfills forward as it arrives. Historical depth
needs the one-shot `fx-history-backfill` script — see step 3.

## Triage (1 min)

```sh
# 1. Confirm the table is missing
sudo -u postgres psql -d ratesengine -tA -c "SELECT to_regclass('public.fx_quotes')"
# → empty line means missing

# 2. Confirm migration version
sudo -u postgres psql -d ratesengine -tA -c "SELECT version, dirty FROM schema_migrations ORDER BY version DESC LIMIT 1"
# → 27|f means migration 0028 hasn't been applied yet

# 3. Confirm the API log shows the symptom (every ~5 min)
journalctl -u ratesengine-api --since "10 minutes ago" -o cat | grep "fx_quotes persist failed" | tail -1
```

## Recovery (5 min)

### 1. Copy the migration file

From your local checkout:

```sh
scp migrations/0028_create_fx_quotes.{up,down}.sql \
    root@<host>:/var/lib/ratesengine/migrations/
```

(R1 host: `136.243.90.96`. The `migrations/` directory is the
authoritative path the `ratesengine-migrate` binary reads —
confirm with `cat /etc/systemd/system/ratesengine-migrate*.service`
if you've moved it.)

### 2. Apply the migration

```sh
ssh root@<host> '
  set -e
  cd /var/lib/ratesengine
  /usr/local/bin/ratesengine-migrate \
    -migrations migrations \
    -dsn "$RATESENGINE_POSTGRES_DSN" \
    up
'
```

Expected output: `1/u create_fx_quotes` then exit 0.

The migration is forward-only, additive, and idempotent on
re-runs (the `create_hypertable` call uses `if_not_exists =>
TRUE`; the table itself doesn't but won't be re-attempted because
`schema_migrations.version` advances). Safe to apply on a live
deployment — no service restart needed; the forex worker picks
up the new table on its next refresh tick.

### 3. Confirm the worker started persisting

```sh
journalctl -u ratesengine-api --since "5 minutes ago" -o cat \
  | grep -c "fx_quotes persist failed"
# → 0 once the next refresh tick fires (≈5 min cadence)

sudo -u postgres psql -d ratesengine -tA -c "SELECT count(*) FROM fx_quotes"
# → > 0 within ~5 min
```

### 4. Backfill historical depth (slow path — separate step)

The forward-flow worker only writes the LATEST snapshot per
refresh tick — it doesn't go back in time. The 1y / all-time
charts on `/currencies/[ticker]` need historical data that the
one-shot `fx-history-backfill` binary fetches from the upstream's
grouped-daily endpoint.

```sh
# On the operator's workstation (needs MASSIVE_API_KEY):
export MASSIVE_API_KEY=...
export DATABASE_URL=postgres://...:5432/ratesengine
go run ./scripts/ops/fx-history-backfill --years=10 --concurrency=4
```

Cost note: Massive bills per historical request. ~3,650 days ×
N currencies, with the upstream's day-grouped endpoint
returning all currencies per day in one call → ~3,650 paid
requests for a 10-year backfill. Schedule for off-peak.

The script logs one line per day to stderr; on completion it
writes a final summary (total days, total rows, elapsed). Safe
to interrupt and resume — the writer upserts on
`(ticker, bucket)` so re-running on the same range is a no-op.

## Prevention

The 2026-05-10 finding exposed a process gap: a release that
adds a migration ships the binary changes via the deploy
workflow, but the migration files + `ratesengine-migrate up`
are operator-side actions not automated by the same workflow.
Two paths forward (pick one):

1. **Wire migrations into the deploy workflow** — add a
   `migrate-up` step to `.github/workflows/deploy.yml` that runs
   before the binary swap. Adds a per-deployment cost (one
   pg-connection round trip + the actual migration time) but
   eliminates the manual step entirely.
2. **Add a startup gate** — `ratesengine-api`'s ready check
   compares the binary's expected schema version (computed at
   build time from the embedded migrations) against
   `schema_migrations.version`; readyz returns 503 with a
   diagnostic if they diverge. Doesn't auto-apply but prevents
   the silent runtime failure.

Either would surface this class of gap immediately at deploy
time rather than letting it silently fail at runtime. Tracking
as follow-up to this runbook.

## Related runbooks

- [`db-disk-full.md`](db-disk-full.md) — different shape; the
  postgres-side disk-pressure surface.
- [`redis-write-blocked-disk-full.md`](redis-write-blocked-disk-full.md) —
  another silent-runtime-failure shape (Redis writes blocked).
