#!/usr/bin/env bash
# data-freshness watchdog — the "never get behind" signal.
#
# Emits node_exporter textfile gauges for (a) per-domain ingest freshness across
# EVERY data domain and (b) the per-source ADR-0033 completeness verdict, so a
# feed dying (coingecko hit its quota → 11 days stale, unnoticed), a timer
# silently not firing (sep1-refresh never existed; the completeness verdict went
# 21 days stale), or a real served≠lake gap (a source going complete=false now
# that the watchdog is trustworthy) all PAGE instead of rotting silently.
#
# The gap detector (source_coverage_snapshots) already covers on-chain
# trade/event source gaps; this fills the rest: reference oracles, FX, supply,
# the issuer-metadata cron, and the verdict itself.
#
# Run from a 15-min timer. One cheap grouped query per domain; same DSN sourcing
# as compute-archive-to.sh (peer-auth fails under systemd's user-switch).
set -euo pipefail
. /etc/default/stellarindex

OUT="${TEXTFILE_OUTPUT:-/var/lib/node_exporter/textfile_collector/data_freshness.prom}"
TMP="$(mktemp "${OUT}.XXXXXX")"
trap 'rm -f "$TMP"' EXIT

{
  echo '# HELP stellarindex_data_freshness_age_seconds Seconds since the newest row for a data domain/source.'
  echo '# TYPE stellarindex_data_freshness_age_seconds gauge'
  echo '# HELP stellarindex_data_freshness_stale 1 when a domain/source is staler than its expected cadence.'
  echo '# TYPE stellarindex_data_freshness_stale gauge'
  echo '# HELP stellarindex_completeness_incomplete 1 when a source latest ADR-0033 verdict is complete=false (real served<>lake gap).'
  echo '# TYPE stellarindex_completeness_incomplete gauge'
} > "$TMP"

# (domain, source, age_seconds, threshold_seconds) per domain. Thresholds are a
# generous multiple of each domain's natural cadence so only a real stall fires.
psql "$STELLARINDEX_POSTGRES_DSN" -tA -F$'\t' >> "$TMP" <<'SQL'
WITH f AS (
  SELECT 'oracle'  AS domain, source AS src, extract(epoch FROM now()-max(ingested_at)) AS age, 10800 AS thr
    FROM oracle_updates WHERE ingested_at > now()-interval '30 days' GROUP BY source
  UNION ALL
  -- FX is daily-grain: observed_at is the data-point time (lags ~a day even
  -- when healthy), so freshness is measured off `bucket` (today's bucket
  -- written = the worker is alive). 48h tolerates a late daily publish.
  SELECT 'fx', source, extract(epoch FROM now()-max(bucket)), 172800
    FROM fx_quotes WHERE bucket > now()-interval '30 days' GROUP BY source
  UNION ALL
  SELECT 'trades', source, extract(epoch FROM now()-max(bucket)), 14400
    FROM source_volume_1h GROUP BY source
  UNION ALL
  SELECT 'supply', 'asset_supply_history', extract(epoch FROM now()-max(time)), 108000
    FROM asset_supply_history WHERE time > now()-interval '7 days'
  UNION ALL
  SELECT 'verdict', source, extract(epoch FROM now()-max(computed_at)), 129600
    FROM completeness_snapshots GROUP BY source
  UNION ALL
  SELECT 'sep1', 'issuers', extract(epoch FROM now()-max(sep1_resolved_at)), 172800
    FROM issuers WHERE sep1_resolved_at IS NOT NULL
)
SELECT 'stellarindex_data_freshness_age_seconds{domain="'||domain||'",source="'||src||'"} '||round(age)::text
  FROM f
UNION ALL
SELECT 'stellarindex_data_freshness_stale{domain="'||domain||'",source="'||src||'"} '||(age>thr)::int::text
  FROM f;
SQL

# Per-source completeness verdict (latest snapshot per source): 1 = incomplete.
psql "$STELLARINDEX_POSTGRES_DSN" -tA -F$'\t' >> "$TMP" <<'SQL'
SELECT 'stellarindex_completeness_incomplete{source="'||source||'"} '||(NOT complete)::int::text
  FROM (SELECT DISTINCT ON (source) source, complete
          FROM completeness_snapshots ORDER BY source, computed_at DESC) s;
SQL

# CS-090: a verdict can read complete=true while its watermark lags the live
# network head (a mid-walk stall or a manual small -to). complete/computed_at
# alone can't see that, so emit the per-source lag (live ingest cursor tip −
# verdict watermark) — a source verified only to an old ledger becomes
# observable/alertable instead of showing a green "N/N complete" badge.
psql "$STELLARINDEX_POSTGRES_DSN" -tA -F$'\t' >> "$TMP" <<'SQL'
WITH tip AS (SELECT max(last_ledger) AS t FROM ingestion_cursors)
SELECT 'stellarindex_completeness_watermark_lag_ledgers{source="'||s.source||'"} '
       ||greatest(0, (SELECT t FROM tip) - s.watermark_ledger)::text
  FROM (SELECT DISTINCT ON (source) source, watermark_ledger
          FROM completeness_snapshots ORDER BY source, computed_at DESC) s;
SQL

# supply_flows (ClickHouse) is the per-token mint/burn/clawback set that backs
# /v1/assets SEP-41 supply — TokenSupply() sums it FINAL on-demand, so its
# freshness IS the served SEP-41 supply's freshness. Live-written by the
# indexer; if it stalls, served SEP-41 supply goes stale. (Threshold generous —
# supply events are bursty.)
SF_AGE=$(curl -sS --max-time 15 http://localhost:8123/ --data-binary \
  "SELECT toUInt64(dateDiff('second', max(ingested_at), now())) FROM stellar.supply_flows" 2>/dev/null | tr -d '[:space:]')
if [ -n "$SF_AGE" ]; then
  printf 'stellarindex_data_freshness_age_seconds{domain="sep41_supply",source="supply_flows"} %s\n' "$SF_AGE" >> "$TMP"
  printf 'stellarindex_data_freshness_stale{domain="sep41_supply",source="supply_flows"} %s\n' \
    "$([ "$SF_AGE" -gt 3600 ] && echo 1 || echo 0)" >> "$TMP"
fi

# node_exporter runs unprivileged — mktemp defaults to 0600, so make the
# rendered file world-readable before the atomic swap or the collector skips it.
chmod 0644 "$TMP"
mv "$TMP" "$OUT"
trap - EXIT
