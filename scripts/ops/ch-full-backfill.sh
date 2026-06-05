#!/usr/bin/env bash
# ch-full-backfill.sh — durable, resumable Phase-3 historic backfill driver
# (ADR-0034). Walks [FROM, TO] in fixed WINDOW-sized slices (aligned to the
# ClickHouse 1M-ledger partition scheme) and runs `ratesengine-ops ch-backfill
# -parallel PAR` on each window from the galexie-archive bucket.
#
# Resume: a window is recorded in $STATE only after ch-backfill exits 0, so a
# restart skips completed windows. ClickHouse writes are idempotent
# (ReplacingMergeTree), so re-running an interrupted window is safe.
#
# Run detached on r1, e.g.:
#   set -a; . /etc/default/ratesengine-ops; set +a
#   setsid nice -n 10 bash scripts/ops/ch-full-backfill.sh > /var/log/ch-full-backfill.log 2>&1 < /dev/null &
#
# Env overrides: FROM TO WINDOW PAR FLUSH BUCKET CONFIG OPS CHADDR STATE.
set -uo pipefail

FROM=${FROM:-1}
TO=${TO:?set TO to the target tip ledger}
WINDOW=${WINDOW:-1000000}
PAR=${PAR:-8}
FLUSH=${FLUSH:-200}
BUCKET=${BUCKET:-galexie-archive}
CONFIG=${CONFIG:-/etc/ratesengine.toml}
OPS=${OPS:-/usr/local/bin/ratesengine-ops-ch}
CHADDR=${CHADDR:-127.0.0.1:9300}
STATE=${STATE:-/var/lib/ch-backfill/done-windows.txt}

mkdir -p "$(dirname "$STATE")"
touch "$STATE"

echo "ch-full-backfill: [$FROM,$TO] window=$WINDOW parallel=$PAR flush=$FLUSH bucket=$BUCKET"
echo "ch-full-backfill: state=$STATE (already $(wc -l < "$STATE") windows done)"

started=$(date +%s)
w=$FROM
while [ "$w" -le "$TO" ]; do
  wto=$((w + WINDOW - 1))
  [ "$wto" -gt "$TO" ] && wto=$TO

  if grep -qx "$w" "$STATE"; then
    echo "ch-full-backfill: skip window $w-$wto (done)"
    w=$((wto + 1))
    continue
  fi

  echo "=== window $w-$wto  ($(date -u +%FT%TZ)) ==="
  if "$OPS" ch-backfill -config "$CONFIG" -bucket "$BUCKET" \
        -ch-addr "$CHADDR" -from "$w" -to "$wto" -parallel "$PAR" -flush-every "$FLUSH"; then
    echo "$w" >> "$STATE"
    elapsed=$(( $(date +%s) - started ))
    echo "ch-full-backfill: window $w-$wto DONE (total elapsed ${elapsed}s)"
  else
    rc=$?
    echo "ch-full-backfill: window $w-$wto FAILED (exit $rc) — stopping; re-run to resume"
    exit "$rc"
  fi
  w=$((wto + 1))
done

echo "ch-full-backfill: ALL WINDOWS COMPLETE [$FROM,$TO] in $(( $(date +%s) - started ))s"
