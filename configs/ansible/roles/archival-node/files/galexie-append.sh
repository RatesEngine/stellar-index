#!/bin/bash
#
# galexie append wrapper.
#
# Galexie's `append` subcommand requires --start <ledger>. On a
# fresh deploy there's no prior cursor, so we need to pick a start
# ledger that is actually reachable from the history archives.
#
# CRITICAL: do NOT start from "now" (i.e. the live network tip).
# Archives publish checkpoints with ~5-15 min lag. If galexie
# starts at "now," every archive 404s on the HAS file and
# galexie's embedded captive-core spins forever waiting for it to
# appear. Instead, query SDF's `.well-known/stellar-history.json`
# for the *archive's* current tip — that's guaranteed to have an
# HAS file already uploaded. We additionally round down to a
# checkpoint boundary and subtract a safety margin so captive-core
# has solid ground.
#
# On a restart after galexie has already exported some ledgers,
# we'd ideally resume from "last-exported-ledger + 1." TODO: probe
# MinIO galexie-live for the latest object and derive. For Phase 1
# we always start from the archive tip; duplicate exports are
# content-addressed so reprocessing is idempotent.
#
# 2026-04-23: removed the "wait for primary stellar-core" preamble —
# we no longer run a separate stellar-core daemon on the box. The
# ONLY stellar-core here is galexie's own captive-core, which this
# wrapper doesn't need to wait for (galexie spawns it itself).

set -euo pipefail

CONF=/etc/galexie/galexie.toml

# SDF's primary archive — same source our captive-core trusts.
SDF_HAS_URL="https://history.stellar.org/prd/core-live/core_live_001/.well-known/stellar-history.json"

# --- 1. Discover archive tip, subtract safety margin -----------
# CHECKPOINT_MARGIN: how many ledgers to back off from the
# archive's reported tip. 128 ledgers ≈ 2 checkpoints ≈ 10 min —
# enough slack that even a straggling archive has the file.
CHECKPOINT_MARGIN="${CHECKPOINT_MARGIN:-128}"

archive_tip=""
for i in $(seq 1 30); do
  if body=$(curl -sfm10 "$SDF_HAS_URL" 2>/dev/null); then
    archive_tip=$(echo "$body" | jq -r '.currentLedger // empty')
    if [[ -n "$archive_tip" && "$archive_tip" -gt 1 ]]; then
      break
    fi
  fi
  sleep 2
done
if [[ -z "$archive_tip" || "$archive_tip" -le 1 ]]; then
  echo "galexie-append.sh: could not read archive tip from $SDF_HAS_URL after 30 attempts" >&2
  exit 1
fi

# Round DOWN to previous checkpoint boundary (every 64 ledgers;
# checkpoint_N spans ledgers [N*64 + 1, (N+1)*64], with the HAS
# file named after the last ledger in the span, i.e. ending in 3f,
# 7f, bf, or ff). `start` must be > 1 and for safety should sit
# comfortably before the archive's advertised tip.
start=$(( archive_tip - CHECKPOINT_MARGIN ))
# Floor to preceding checkpoint boundary (file ending .xxff etc.)
start=$(( (start / 64) * 64 ))
if [[ "$start" -le 1 ]]; then
  start=64
fi

echo "galexie-append.sh: archive tip=$archive_tip, starting at $start (margin=$CHECKPOINT_MARGIN)"
exec /usr/local/bin/galexie append --config-file "$CONF" --start "$start"
