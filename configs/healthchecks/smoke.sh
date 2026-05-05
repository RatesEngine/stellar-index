#!/usr/bin/env bash
#
# smoke.sh — wrapper around scripts/dev/r1-smoke.sh that reports
# the result to Healthchecks.io. Distinct from heartbeat.sh: that
# probes the metrics port (process up); this one verifies the
# public API surface (schema + data integrity).
#
# Catches regressions the per-binary heartbeats can't see — e.g.
# /v1/price returning a 200 with malformed JSON, /v1/coins missing
# the `data` field, an OpenAPI-spec change that breaks downstream
# clients.
#
# URL comes from /etc/default/ratesengine-healthchecks
# (HEALTHCHECKS_URL_SMOKE). Empty URL silently runs the smoke
# script for journal-only coverage.

set -uo pipefail

SMOKE_SCRIPT="${SMOKE_SCRIPT:-/opt/ratesengine/healthchecks/r1-smoke.sh}"
URL="${HEALTHCHECKS_URL_SMOKE:-}"

if [ ! -x "$SMOKE_SCRIPT" ]; then
  echo "smoke: $SMOKE_SCRIPT not found or not executable" >&2
  exit 0
fi

# Run the smoke script. Captures its exit code (= number of failed
# checks per the script's contract) and the full output for the
# Healthchecks.io ping body — operators reading the dashboard see
# exactly which checks tripped without leaving the page.
OUT="$(bash "$SMOKE_SCRIPT" 2>&1)"
RC=$?

if [ -n "$URL" ]; then
  if [ "$RC" -eq 0 ]; then
    curl -fsS --max-time 10 -o /dev/null --retry 2 \
      --data-binary "$OUT" \
      "$URL" || true
  else
    curl -fsS --max-time 10 -o /dev/null --retry 2 \
      --data-binary "$OUT" \
      "${URL}/fail" || true
  fi
fi

# Always exit 0 from the timer's perspective — same contract as
# heartbeat.sh. Failures route via the /fail webhook + journalctl.
exit 0
