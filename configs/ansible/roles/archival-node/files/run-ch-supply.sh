#!/usr/bin/env bash
# Refresh stellar.token_supply — the lake-derived per-token supply
# (Σmint−Σburn−Σclawback over the certified ClickHouse lake) that backs
# /v1/assets supply for SEP-41 tokens (the 10k+ Soroban tokens that can't be
# on an LCM observer watch-list). Two phases:
#   1. seed supply_flows for the gap [last-seeded+1, tip] in chunks — decode one
#      row per mint/burn/clawback event from stellar.contract_events (idempotent
#      via the ReplacingMergeTree key);
#   2. re-aggregate the FINAL-deduped flows [genesis, tip] into token_supply.
# Chunked + memory-guarded so the initial catch-up AND the daily steady-state
# both stay gentle on ClickHouse. Same DSN sourcing as compute-archive-to.sh.
set -uo pipefail
. /etc/default/stellarindex

DSN="$STELLARINDEX_POSTGRES_DSN"
OPS=/usr/local/bin/stellarindex-ops
CONFIG="${CONFIG_PATH:-/etc/stellarindex.toml}"
CHADDR="${CH_ADDR:-127.0.0.1:9300}"
CHUNK="${CHSUPPLY_CHUNK:-25000}"
MEMGUARD="${CHSUPPLY_MEMGUARD:-6442450944}"   # wait while CH mem > 6 GiB
LOG=/var/log/ch-supply-refresh.log
CH() { curl -sS --max-time 3600 http://localhost:8123/ --data-binary "$1" 2>/dev/null; }

TIP=$(psql "$DSN" -tA -c "SELECT last_ledger FROM ingestion_cursors WHERE source='ledgerstream'" 2>/dev/null | tr -d '[:space:]')
[ -n "$TIP" ] && [ "$TIP" != "0" ] || { echo "$(date -u) ch-supply: tip unresolved" >> "$LOG"; exit 1; }
FROM=$(CH "SELECT max(ledger)+1 FROM stellar.supply_flows" | tr -d '[:space:]')
[ -n "$FROM" ] && [ "$FROM" != "0" ] || FROM=2

echo "$(date -u) ch-supply refresh: seed [$FROM,$TIP] (chunk=$CHUNK) then aggregate" >> "$LOG"

# Phase 1 — seed new flows in chunks, with a memory guard between.
while [ "$FROM" -lt "$TIP" ]; do
  TO=$(( FROM + CHUNK )); [ "$TO" -gt "$TIP" ] && TO=$TIP
  for _ in $(seq 1 30); do
    M=$(CH "SELECT sum(memory_usage) FROM system.processes" | tr -d '[:space:]')
    [ "${M:-0}" -lt "$MEMGUARD" ] && break
    sleep 20
  done
  "$OPS" ch-supply -config "$CONFIG" -ch-addr "$CHADDR" -from "$FROM" -to "$TO" -seed-flows </dev/null >> "$LOG" 2>&1 \
    || echo "$(date -u) seed [$FROM,$TO] FAILED" >> "$LOG"
  FROM=$TO
done

# Phase 2 — aggregate the full FINAL-deduped flows into token_supply.
echo "$(date -u) aggregating token_supply [2,$TIP]" >> "$LOG"
"$OPS" ch-supply -config "$CONFIG" -ch-addr "$CHADDR" -from 2 -to "$TIP" -write -final </dev/null >> "$LOG" 2>&1 \
  && echo "$(date -u) ch-supply refresh DONE (tip=$TIP)" >> "$LOG" \
  || { echo "$(date -u) ch-supply aggregate FAILED" >> "$LOG"; exit 1; }
