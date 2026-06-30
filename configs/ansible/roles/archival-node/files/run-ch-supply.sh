#!/usr/bin/env bash
# Keep stellar.supply_flows complete to tip — the per-token mint/burn/clawback
# flow set that backs /v1/assets SEP-41 supply (TokenSupply() sums it FINAL
# on-demand; the 10k+ Soroban tokens can't sit on an LCM observer watch-list).
#
# supply_flows is written LIVE by the indexer's decode-at-ingest path AND stays
# verified-complete vs the lake. This timer is the DEFENSIVE forward-gap-fill: if
# the live writer ever falls behind, it re-seeds [last-seeded+1, tip] from the
# certified lake (one decoded row per mint/burn/clawback event, idempotent via
# the ReplacingMergeTree key). Normally a no-op. Chunked + memory-guarded so a
# real catch-up stays gentle on ClickHouse. (The stellar.token_supply rollup
# table is NOT refreshed here — nothing reads it; serving sums supply_flows
# directly.) Same DSN sourcing as compute-archive-to.sh.
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
FROM=$(CH "SELECT max(ledger_seq)+1 FROM stellar.supply_flows" | tr -d '[:space:]')
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
echo "$(date -u) supply_flows seed complete (tip=$TIP)" >> "$LOG"
