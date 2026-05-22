#!/usr/bin/env bash
# ADR-0027 §Trim operator helper: compute TRIM_CUTOFF for the
# monthly galexie-archive-trim.service from the indexer cursor.
#
# The trim-galexie-archive subcommand requires an explicit
# --older-than-ledger <seq>. Translating "90 days ago" into a
# concrete ledger sequence has to happen at run time — systemd
# can't do arithmetic in the unit file. This helper does it by
# reading the indexer's latest cursor from postgres, subtracting
# 90 days of ledgers (17280/day × 90 = 1,555,200), and writing
# the result to /run/galexie-archive-trim.env which the service
# loads via EnvironmentFile.
#
# Mirrors the compute-archive-to.sh pattern (F-1205): use the same
# DSN as the application binaries (sourced from /etc/default/
# ratesengine) rather than peer-auth, which fails under systemd's
# restricted user-switch context.
#
# Safety:
#   - bails when the cursor hasn't advanced enough for 90 days of
#     headroom to make sense (a brand-new node has nothing to trim).
#   - bails when the resulting cutoff is below ledger 2 (the
#     first real ledger; ledger 1 is empty by Stellar design).
set -euo pipefail

. /etc/default/ratesengine

# 90 days @ 5s ledger close = 17280 ledgers/day × 90 = 1,555,200.
HOT_WINDOW_LEDGERS=1555200

TIP=$(psql "$RATESENGINE_POSTGRES_DSN" -tA -c \
  'SELECT GREATEST(MAX(last_ledger), 0) FROM ingestion_cursors WHERE last_ledger > 0' \
  2>/dev/null | tr -d '[:space:]')

if [ -z "$TIP" ] || [ "$TIP" = "0" ]; then
  echo "compute-trim-cutoff: indexer cursor not advanced; bailing" >&2
  exit 1
fi

CUTOFF=$((TIP - HOT_WINDOW_LEDGERS))

if [ "$CUTOFF" -lt 2 ]; then
  echo "compute-trim-cutoff: tip=$TIP gives cutoff=$CUTOFF — not enough history yet; bailing" >&2
  exit 1
fi

echo "TRIM_CUTOFF=$CUTOFF" > /run/galexie-archive-trim.env
echo "compute-trim-cutoff: tip=$TIP cutoff=$CUTOFF (90d hot window)" >&2
