#!/usr/bin/env bash
#
# sla-probe.sh — wrapper around `ratesengine-sla-probe` that
# reports pass/fail to Healthchecks.io. Distinct from smoke.sh:
# that one verifies the API surface (schema + data integrity);
# this one drives load and asserts the RFP latency + freshness
# SLAs are met (p95 ≤ 200 ms, p99 ≤ 500 ms, freshness ≤ 30 s).
#
# Exit code from the binary = 0 (pass) or 1 (at least one SLA
# violated). Both branches POST the full JSON report body to
# Healthchecks.io so operators reading the dashboard see exactly
# which percentile / endpoint tripped.
#
# URL comes from /etc/default/ratesengine-healthchecks
# (HEALTHCHECKS_URL_SLA_PROBE). Empty URL silently runs the probe
# for journal-only coverage.

set -uo pipefail

PROBE_BIN="${PROBE_BIN:-/usr/local/bin/ratesengine-sla-probe}"
BASE_URL="${SLA_PROBE_BASE_URL:-http://localhost:3000/v1}"
DURATION="${SLA_PROBE_DURATION:-30s}"
CONCURRENCY="${SLA_PROBE_CONCURRENCY:-2}"
PAIR="${SLA_PROBE_PAIR:-native,fiat:USD}"
URL="${HEALTHCHECKS_URL_SLA_PROBE:-}"

if [ ! -x "$PROBE_BIN" ]; then
  echo "sla-probe: $PROBE_BIN not found or not executable" >&2
  exit 0
fi

# Run the probe. JSON report on stdout; pass=0, fail=1 on exit.
OUT="$(
  "$PROBE_BIN" \
    -base-url "$BASE_URL" \
    -duration "$DURATION" \
    -concurrency "$CONCURRENCY" \
    -pair "$PAIR" \
    -report-format json 2>&1
)"
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

# Always exit 0 from the timer's perspective — failures route
# via the /fail webhook + journalctl, same contract as the other
# Healthchecks-driven timers.
exit 0
