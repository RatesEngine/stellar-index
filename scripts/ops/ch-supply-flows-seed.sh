#!/usr/bin/env bash
# ch-supply-flows-seed.sh — windowed, resumable seed of stellar.supply_flows from
# the existing ClickHouse contract_events lake (decode-at-ingest history backfill,
# ADR-0034). Once seeded, the real-time dual-sink keeps supply_flows current; this
# script is only for the one-time history fill (and disaster recovery).
#
# WHY WINDOWED: a single ch-supply -seed-flows over all history exceeds openRead's
# 1h ReadTimeout — the write-back paces the read to ~130k flows/s, so ~570M flows
# take ~73min — AND StreamMintBurnFlows has no ORDER BY, so ClickHouse streams rows
# unordered across parts; a mid-stream timeout therefore leaves SCATTERED holes,
# not a clean [2,X] prefix. Windowing bounds each read well under the timeout.
# Idempotent (supply_flows ReplacingMergeTree dedups by event identity, so re-runs
# and dup-partition reads with -final=false are safe) and resumable via a
# done-windows file.
set -uo pipefail
set -a; . /etc/default/ratesengine-ops; set +a
OPS=${OPS:-/usr/local/bin/ratesengine-ops-ch}
CFG=${CFG:-/etc/ratesengine.toml}
CH_PORT=${CH_PORT:-9300}
FROM=${FROM:-2}
TO=${TO:-$(clickhouse-client --port "$CH_PORT" -q "SELECT max(ledger_seq) FROM stellar.ledgers")}
# 1M-ledger windows: even the densest recent window (~73 supply flows/ledger ⇒
# ~73M flows ⇒ ~9min at 130k/s) finishes well under the 1h read timeout.
WINDOW=${WINDOW:-1000000}
STATE=${STATE:-/var/lib/ch-backfill/supply-flows-done.txt}

if [ -z "${TO:-}" ]; then echo "$(date -u) could not resolve TO" >&2; exit 1; fi
mkdir -p "$(dirname "$STATE")"; touch "$STATE"
echo "$(date -u) ch-supply-flows-seed: [$FROM,$TO] window=$WINDOW state=$STATE"

w=$FROM
rc=0
while [ "$w" -le "$TO" ]; do
  end=$((w + WINDOW - 1)); [ "$end" -gt "$TO" ] && end=$TO
  if grep -qx "$w" "$STATE"; then
    w=$((end + 1)); continue
  fi
  echo "$(date -u) seed [$w,$end]"
  if "$OPS" ch-supply -config "$CFG" -from "$w" -to "$end" -seed-flows -final=false >/dev/null; then
    echo "$w" >> "$STATE"
  else
    echo "$(date -u) FAILED [$w,$end]" >&2; rc=1; break
  fi
  w=$((end + 1))
done
[ "$rc" -eq 0 ] && echo "$(date -u) ch-supply-flows-seed: complete [$FROM,$TO]"
exit $rc
