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
# F-1305 (codex audit-2026-05-13): 30s default keeps the probe
# cheap on a quiet deployment, but on a single-instance host
# under memory pressure (e.g. r1 today) a 30s window of ~800
# requests is too sensitive to single-timeout jitter — one
# straggler at 99.9% availability = unit_failed=1. Operators on
# memory-pressured single-instance hosts should override to
# SLA_PROBE_DURATION=120s (or longer) in /etc/default/ratesengine-
# healthchecks for a smoother percentile.
DURATION="${SLA_PROBE_DURATION:-30s}"
# F-1305 / F-1311 (codex audit-2026-05-13): default concurrency=1
# (was 2). At 2 the probe drives ~2.5k req/s against the API which
# is well above any realistic customer cadence; it tripped both
# the operator-tier rate limit (returning 429s → fake availability
# fail) AND saturated the API's request path itself (p95 climbed
# to ~3.8s under the load — the F-1311 false-positive). At
# concurrency=1 the probe samples at ~800 req over 30s which
# stresses the closed-bucket lookup enough to surface real
# regressions without overwhelming a single-instance deployment.
# Operators on multi-instance HA can override via inventory.
CONCURRENCY="${SLA_PROBE_CONCURRENCY:-1}"
PAIR="${SLA_PROBE_PAIR:-native,fiat:USD}"
URL="${HEALTHCHECKS_URL_SLA_PROBE:-}"
# TEXTFILE_OUTPUT — path under node_exporter's textfile_collector
# dir where the probe writes per-endpoint p50/p95/p99 + freshness
# + verdict metrics. Without this set the alerts in
# deploy/monitoring/rules/sla-probe.yml have no series to evaluate
# against — the gap the 2026-05-12 audit caught as F-1221. The
# archival-node ansible role provisions
# /var/lib/node_exporter/textfile_collector/ + sets the
# --collector.textfile flag on node_exporter; we default-on here
# so a fresh-from-ansible host gets the SLA-evidence chain wired
# automatically. Set to empty to disable the metric emission.
TEXTFILE_OUTPUT="${SLA_PROBE_TEXTFILE_OUTPUT:-/var/lib/node_exporter/textfile_collector/sla_probe.prom}"

# F-1303 (codex audit-2026-05-13): a missing or non-executable
# probe binary is itself a failure — fan out to Healthchecks/fail
# so the SLA-evidence check goes red, otherwise a broken binary
# deploy silently disables the check without anyone noticing.
if [ ! -x "$PROBE_BIN" ]; then
  MSG="sla-probe: $PROBE_BIN not found or not executable"
  echo "$MSG" >&2
  if [ -n "$URL" ]; then
    curl -fsS --max-time 10 -o /dev/null --retry 2 \
      --data-binary "$MSG" \
      "${URL}/fail" || true
  fi
  exit 0
fi

# Build the textfile-output arg conditionally — the binary's flag
# parser rejects empty -textfile-output, so we omit the flag
# entirely when TEXTFILE_OUTPUT is intentionally blank.
TEXTFILE_FLAG=()
if [ -n "$TEXTFILE_OUTPUT" ]; then
  TEXTFILE_FLAG=(-textfile-output "$TEXTFILE_OUTPUT")
fi

# Run the probe. JSON report on stdout; pass=0, fail=1 on exit.
OUT="$(
  "$PROBE_BIN" \
    -base-url "$BASE_URL" \
    -duration "$DURATION" \
    -concurrency "$CONCURRENCY" \
    -pair "$PAIR" \
    -report-format json \
    "${TEXTFILE_FLAG[@]}" 2>&1
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
