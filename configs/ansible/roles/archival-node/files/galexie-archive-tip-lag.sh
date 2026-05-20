#!/bin/bash
# galexie-archive-tip-lag — defense-in-depth alert source for #26.
#
# WHY: #26 was a 23-day silent stall of galexie-archive (the
# ADR-0016 R1 durable full-mirror). The recurrence fix is the
# hourly galexie-archive-fill.timer; this script is the
# defense-in-depth — if that timer itself silently breaks (mc
# alias rotated, aws-public IAM change, MinIO mtime-poison
# deadlock per "mc mirror gotcha"), we want to PAGE within hours,
# not weeks. The metric is the lag in ledgers between the newest
# object in galexie-live and the newest in galexie-archive; the
# Prometheus alert sits on top.
#
# Mechanics:
#   - List the newest partition in each bucket (mc ls default sort
#     surfaces it first — partition names are reverse-hex prefixed,
#     so lexically largest = oldest ledgers; lexically smallest =
#     newest ledger range. The first row of `mc ls bucket/` is the
#     newest partition.)
#   - List the newest object inside that partition; object names
#     are `<reverse-hex>--<ledger>.xdr.zst`, so again the first row
#     is the newest ledger.
#   - Compute lag = live_tip - archive_tip (ledgers).
#   - Write the textfile-collector .prom atomically.
#
# Runtime: ~2-3 seconds (two `mc ls bucket/` + two `mc ls
# bucket/partition/` calls — small listings, not the whole bucket).

set -euo pipefail

TEXTFILE_DIR=/var/lib/node_exporter/textfile_collector
OUT="$TEXTFILE_DIR/galexie_archive_tip_lag.prom"
TMP="$OUT.tmp.$$"

newest_ledger() {
  # $1 = bucket name (e.g. local/galexie-live).
  #
  # Implementation note: buffer `mc ls` into a variable BEFORE
  # awk-parsing. Streaming `mc ls | awk '...; exit'` causes awk to
  # close the pipe on its first match; mc ls gets SIGPIPE (status
  # 141) and under `set -o pipefail` the whole script aborts. The
  # listings here are tiny (one bucket's partition list, or one
  # partition's first page of objects), so buffering is free.
  local bucket="$1" parts_raw objs_raw part
  parts_raw=$(mc ls "$bucket/" 2>/dev/null) || true
  part=$(printf '%s\n' "$parts_raw" | awk '/\/$/{print $NF; exit}' | sed 's:/$::') || true
  if [ -z "$part" ]; then
    echo "0"
    return
  fi
  objs_raw=$(mc ls "$bucket/$part/" 2>/dev/null) || true
  printf '%s\n' "$objs_raw" \
    | awk -F'--' '/--[0-9]+\.xdr\.zst/{print $2; exit}' \
    | sed 's/\.xdr\.zst$//' \
    || true
}

live=$(newest_ledger local/galexie-live)
archive=$(newest_ledger local/galexie-archive)

# Guard: numeric.
[[ "$live" =~ ^[0-9]+$ ]] || live=0
[[ "$archive" =~ ^[0-9]+$ ]] || archive=0

if [ "$live" -gt "$archive" ]; then
  lag=$((live - archive))
else
  lag=0
fi

mkdir -p "$TEXTFILE_DIR"
cat > "$TMP" <<EOF
# HELP galexie_archive_tip_ledger Newest ledger sequence present in galexie-archive (R1 durable full-mirror).
# TYPE galexie_archive_tip_ledger gauge
galexie_archive_tip_ledger $archive
# HELP galexie_live_tip_ledger Newest ledger sequence present in galexie-live (rolling appender).
# TYPE galexie_live_tip_ledger gauge
galexie_live_tip_ledger $live
# HELP galexie_archive_tip_lag_ledgers Ledger lag of galexie-archive behind galexie-live (live - archive). Defense-in-depth for #26: hourly catch-up timer should keep this near zero; sustained drift = the timer / its dependencies have broken.
# TYPE galexie_archive_tip_lag_ledgers gauge
galexie_archive_tip_lag_ledgers $lag
# HELP galexie_archive_tip_lag_updated_seconds Unix time of the most recent successful tip-lag computation.
# TYPE galexie_archive_tip_lag_updated_seconds gauge
galexie_archive_tip_lag_updated_seconds $(date +%s)
EOF
chmod 644 "$TMP"
mv "$TMP" "$OUT"
