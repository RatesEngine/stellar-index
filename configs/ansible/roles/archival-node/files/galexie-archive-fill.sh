#!/bin/bash
# Fill galexie-archive from AWS public bucket — mtime-aware approach.
#
# WHY: mc mirror --overwrite=false errors on every object whose mtime
# differs between source AWS and dest MinIO (which is every object that
# was previously copied via mc cp), then deadlocks. The runbook claim
# that --overwrite=false skips silently is wrong as of mc 2025-08-13.
#
# Strategy: compute (AWS partitions − local partitions) = missing
# partition set, mirror each in parallel with --skip-errors. For known
# partial partitions (passed via PARTIALS env var or stdin), delete
# them first so mirror sees them as missing and copies cleanly.
#
# We deliberately do NOT walk the entire 25M-object bucket to detect
# all partial partitions — that listing is slow under contention and
# blocks the actual fill work. Run verify-archive (Tier A + B) after
# this script completes; any remaining partials surface there.
#
# See docs/operations/galexie-backfill.md "mc mirror gotcha" for the
# failure mode this script works around.
set -euo pipefail

LOG=/var/log/galexie-mirror.log
PARALLEL="${PARALLEL:-8}"

# Known partials: pass via env var (newline- or space-separated), e.g.
#   PARTIALS=$'FC49CDFF--62272000-62335999\nXYZ--...' galexie-archive-fill
# Partition names should NOT have trailing slashes.
PARTIALS_INPUT="${PARTIALS:-}"

if [ -n "$PARTIALS_INPUT" ]; then
  echo "=== $(date -Iseconds) Phase 1: delete known partials ===" | tee -a "$LOG"
  echo "$PARTIALS_INPUT" | tr ' ' '\n' | grep -v '^$' | while read -r p; do
    echo "  rm: $p" | tee -a "$LOG"
    mc rm --recursive --force "local/galexie-archive/$p/" >/dev/null 2>&1 || true
  done
fi

echo "=== $(date -Iseconds) Phase 2: build needs-work list ===" | tee -a "$LOG"
mc ls aws-public/aws-public-blockchain/v1.1/stellar/ledgers/pubnet/ \
  | awk '{print $NF}' | sed 's:/$::' | sort > /tmp/galexie-fill.aws.txt
mc ls local/galexie-archive/ \
  | awk '{print $NF}' | sed 's:/$::' | sort > /tmp/galexie-fill.local.txt
comm -23 /tmp/galexie-fill.aws.txt /tmp/galexie-fill.local.txt \
  > /tmp/galexie-fill.needs-work.txt
echo "  AWS partitions: $(wc -l < /tmp/galexie-fill.aws.txt)" | tee -a "$LOG"
echo "  local partitions present: $(wc -l < /tmp/galexie-fill.local.txt)" | tee -a "$LOG"
echo "  needs work (missing): $(wc -l < /tmp/galexie-fill.needs-work.txt)" | tee -a "$LOG"

echo "=== $(date -Iseconds) Phase 3: mirror per-partition (parallel=$PARALLEL) ===" | tee -a "$LOG"
# Each partition is fully missing locally (we just deleted any partials),
# so mc mirror has no mtime conflicts. --skip-errors is belt-and-braces.
# Parallel=8 is conservative — 100 MB/s observed link saturation, so
# more workers won't help.
xargs -a /tmp/galexie-fill.needs-work.txt -P "$PARALLEL" -I {} bash -c '
  echo "==> $(date -Iseconds) {}" >> "'"$LOG"'"
  mc mirror --skip-errors \
    "aws-public/aws-public-blockchain/v1.1/stellar/ledgers/pubnet/{}/" \
    "local/galexie-archive/{}/" >> "'"$LOG"'" 2>&1
  echo "<== $(date -Iseconds) {}" >> "'"$LOG"'"
'

echo "=== $(date -Iseconds) Done ===" | tee -a "$LOG"
echo "Next: ratesengine-ops verify-archive -tier all -from 2 -to <last-mirrored-ledger>" | tee -a "$LOG"
