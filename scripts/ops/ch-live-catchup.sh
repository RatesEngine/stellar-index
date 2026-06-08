#!/usr/bin/env bash
# ch-live-catchup.sh — keep the ClickHouse Tier-1 lake COMPLETE up to the live
# indexer's confirmed position (ADR-0034 live fan-out). Two responsibilities:
#
#   1. HOLE HEALING — the in-dispatcher dual-sink (clickhouse.LiveSink) is
#      best-effort: it DROPS whole ledgers under buffer pressure and a flush can
#      partially fail, leaving holes BELOW the lake's max ledger. A tip-only
#      catch-up ([CH_max+1, tip]) can never re-fill those — the sink already
#      wrote past them. So we gap-scan stellar.ledgers over [LIVE_ERA_FROM, tip]
#      and ch-backfill every missing range.
#   2. TIP EXTENSION — backfill [CH_max+1, tip] for whatever the dual-sink hasn't
#      reached yet (e.g. between flushes, or while the sink was down).
#
# ch-backfill is idempotent (ReplacingMergeTree dedups), so this is safe on a
# short timer; each run only writes the ranges that are actually missing. The
# real-time projector (clickhouse_projector_source) reads CH only up to the
# contiguous-completeness watermark, so a hole stalls the projector until THIS
# script heals it — never silent loss.
set -uo pipefail
set -a; . /etc/default/ratesengine-ops; set +a
OPS=${OPS:-/usr/local/bin/ratesengine-ops-ch}
CFG=${CFG:-/etc/ratesengine.toml}
DSN="$RATESENGINE_POSTGRES_DSN"
PAR=${PAR:-4}
CH() { clickhouse-client --port "${CH_PORT:-9300}" "$@"; }
# Backfill is certified complete through 62894000; holes only form in the live
# era above it. Scan from there up. Override if the backfill ceiling changes.
LIVE_ERA_FROM=${LIVE_ERA_FROM:-62894001}

CH_MAX=$(CH -q "SELECT max(ledger_seq) FROM stellar.ledgers" 2>/dev/null)
TIP=$(psql "$DSN" -tAc "SELECT max(last_ledger) FROM ingestion_cursors" 2>/dev/null | tr -d '[:space:]')
if [ -z "${CH_MAX:-}" ] || [ -z "${TIP:-}" ]; then
  echo "$(date -u) ch-live-catchup: could not resolve CH_MAX=$CH_MAX / TIP=$TIP" >&2
  exit 1
fi

rc=0

# 1. Heal holes below CH_MAX. leadInFrame's (CURRENT ROW .. 1 FOLLOWING) frame
#    returns the last row's own value, so there is no spurious trailing gap.
GAPS=$(CH -q "
  SELECT gap_start, gap_end FROM (
    SELECT ledger_seq + 1 AS gap_start, nxt - 1 AS gap_end
    FROM (
      SELECT ledger_seq,
             leadInFrame(ledger_seq) OVER (
                 ORDER BY ledger_seq ROWS BETWEEN CURRENT ROW AND 1 FOLLOWING
             ) AS nxt
      FROM (SELECT DISTINCT ledger_seq FROM stellar.ledgers WHERE ledger_seq >= ${LIVE_ERA_FROM})
    )
    WHERE nxt > ledger_seq + 1
  )
  ORDER BY gap_start
  FORMAT TSV" 2>/dev/null)
if [ -n "$GAPS" ]; then
  NGAPS=$(printf '%s\n' "$GAPS" | wc -l | tr -d '[:space:]')
  echo "$(date -u) ch-live-catchup: healing $NGAPS hole(s) in [$LIVE_ERA_FROM,$CH_MAX]"
  while IFS=$'\t' read -r gstart gend; do
    [ -z "$gstart" ] && continue
    echo "$(date -u) ch-live-catchup: heal [$gstart,$gend] ($((gend - gstart + 1)) ledgers)"
    "$OPS" ch-backfill -config "$CFG" -from "$gstart" -to "$gend" -parallel "$PAR" || rc=1
  done <<< "$GAPS"
else
  echo "$(date -u) ch-live-catchup: no holes in [$LIVE_ERA_FROM,$CH_MAX]"
fi

# 2. Extend the tip.
if [ "$TIP" -gt "$CH_MAX" ]; then
  FROM=$((CH_MAX + 1))
  echo "$(date -u) ch-live-catchup: tip-extend [$FROM,$TIP] ($((TIP - CH_MAX)) ledgers)"
  "$OPS" ch-backfill -config "$CFG" -from "$FROM" -to "$TIP" -parallel "$PAR" || rc=1
else
  echo "$(date -u) ch-live-catchup: tip current (max=$CH_MAX tip=$TIP)"
fi

exit $rc
