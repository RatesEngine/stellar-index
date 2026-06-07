#!/usr/bin/env bash
# ch-rebuild-projected.sh — ADR-0034 Phase-4 clean-slate rebuild of the
# PROJECTED (soroban_events-derived) sources from the ClickHouse lake.
#
# Why clean-slate (not additive upsert): the live AMM/projected trades were
# written through the collision-era event_index=0 bug, so their op_index — and
# thus the trades PK (source,ledger,tx_hash,op_index,ts) — differs from the
# correct CH re-derivation. An additive -write DOUBLES rows (ON CONFLICT can't
# dedup mismatched keys). So per 1M-ledger window: DELETE the window's rows,
# then ch-rebuild -write re-derives them from CH with correct keys.
#
# Scoped to <=62.894M (the CH backfill tip) so the live tail the indexer is
# still writing stays untouched; the delete/rebuild range never overlaps the
# indexer's current writes, so ingestion keeps running. Resumable (per-window
# marker), append-logged, ON_ERROR_STOP-guarded.
#
# NOT in scope: sdex (op-derived, correctly keyed), external/band (not
# CH-event-derived). reflector/redstone are exact (no collision) — harmless.
#
# Run on r1: nohup setsid bash scripts/ops/ch-rebuild-projected.sh >/dev/null 2>&1 &
set -uo pipefail
set -a; . /etc/default/ratesengine-ops; set +a
OPS=${OPS:-/usr/local/bin/ratesengine-ops-ch}
CFG=${CFG:-/etc/ratesengine.toml}
DSN="$RATESENGINE_POSTGRES_DSN"
SRC=${SRC:-"aquarius,soroswap,phoenix,comet,blend,cctp,rozo,defindex"}
FROM=${FROM:-50000000}; TO=${TO:-62894000}; WIN=${WIN:-1000000}
STATE=${STATE:-/var/lib/ch-backfill/rebuild-done-windows.txt}
LOG=${LOG:-/var/log/ch-rebuild-projected.log}
mkdir -p "$(dirname "$STATE")"; touch "$STATE"
exec >>"$LOG" 2>&1
echo "=== ch-rebuild-projected START $(date -u) [$FROM,$TO] sources=$SRC ==="
w=$FROM
while [ "$w" -le "$TO" ]; do
  hi=$((w+WIN-1)); [ "$hi" -gt "$TO" ] && hi=$TO
  if grep -qx "$w" "$STATE"; then w=$((w+WIN)); continue; fi
  echo "--- window [$w,$hi] DELETE $(date -u) ---"
  psql "$DSN" -v ON_ERROR_STOP=1 <<SQL || { echo "DELETE FAILED [$w,$hi]"; exit 1; }
DELETE FROM trades WHERE source IN ('aquarius','soroswap','phoenix','comet') AND ledger BETWEEN $w AND $hi;
DELETE FROM soroswap_skim_events WHERE ledger BETWEEN $w AND $hi;
DELETE FROM phoenix_liquidity     WHERE ledger BETWEEN $w AND $hi;
DELETE FROM phoenix_stake_events  WHERE ledger BETWEEN $w AND $hi;
DELETE FROM comet_liquidity       WHERE ledger BETWEEN $w AND $hi;
DELETE FROM cctp_events           WHERE ledger BETWEEN $w AND $hi;
DELETE FROM rozo_events           WHERE ledger BETWEEN $w AND $hi;
DELETE FROM defindex_flows        WHERE ledger BETWEEN $w AND $hi;
DELETE FROM blend_auctions        WHERE ledger BETWEEN $w AND $hi;
DELETE FROM blend_positions       WHERE ledger BETWEEN $w AND $hi;
DELETE FROM blend_emissions       WHERE ledger BETWEEN $w AND $hi;
DELETE FROM blend_admin           WHERE ledger BETWEEN $w AND $hi;
SQL
  echo "--- window [$w,$hi] REBUILD $(date -u) ---"
  $OPS ch-rebuild -config "$CFG" -from "$w" -to "$hi" -sources "$SRC" -write \
    || { echo "REBUILD FAILED [$w,$hi]"; exit 1; }
  echo "$w" >> "$STATE"
  echo "window [$w,$hi] DONE $(date -u)"
  w=$((w+WIN))
done
echo "=== ch-rebuild-projected COMPLETE $(date -u) ==="
