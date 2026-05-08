#!/usr/bin/env bash
#
# r1-smoke.sh — exercise the launch-critical API surface against
# a deployment. Defaults to R1 over its internal address; pass
# API_BASE_URL to point elsewhere (e.g. http://localhost:3000 in
# dev or https://api.ratesengine.net post-DNS-cutover).
#
# Each check runs independently — one failing endpoint doesn't
# short-circuit the others, so a single run reports every break.
# Exit code is the number of failed checks.
#
# Usage:
#   bash scripts/dev/r1-smoke.sh                       # public defaults
#   API_BASE_URL=http://localhost:3000 bash scripts/dev/r1-smoke.sh
#   ssh root@136.243.90.96 'bash -s' < scripts/dev/r1-smoke.sh
#
# Designed to be safe to run from cron / a healthcheck timer:
# every endpoint is GET, response bodies are truncated, no auth
# is required (anonymous tier covers everything checked).
#
# Two flavours of check:
#   - check NAME PATH [-- jq-test]                  — wants HTTP 200
#   - expect_status STATUS NAME PATH [-- jq-test]  — wants any status
# Use expect_status for *behaviour pins* — asserting documented 4xx
# responses (e.g. invalid-cursor, invalid-limit, coin-not-found) so
# a regression that flips a documented 400 back to a silent 200
# (the class of bug that motivated #1134) fails the smoke instead
# of sailing past liveness checks.

set -uo pipefail

API_BASE_URL="${API_BASE_URL:-http://localhost:3000}"
# Per-request timeout. Default 10s sits comfortably above the 8s
# server-side context.WithTimeout ceiling shipped across the cold-
# path handlers (#1082, #1099-#1107) — a request that crosses 10s
# wall-clock is genuinely hung (504-class), not just "scanning a
# cold hypertable for the first time today." 5s, the previous
# default, false-positived on /v1/markets?limit=5 cold-cache
# responses (typical 6-8s) and was filling Healthchecks.io with
# spurious failures.
TIMEOUT="${SMOKE_TIMEOUT:-10}"
FAILS=0

# ANSI colour helpers — disabled when stdout isn't a TTY (cron,
# Healthchecks.io captures, log files).
if [ -t 1 ]; then
  GREEN="$(printf '\033[32m')"; RED="$(printf '\033[31m')"; DIM="$(printf '\033[2m')"; OFF="$(printf '\033[0m')"
else
  GREEN=""; RED=""; DIM=""; OFF=""
fi

# check NAME PATH [-- jq-test]   — runs GET ${BASE}${PATH} under
# the per-request TIMEOUT budget (10s default; see SMOKE_TIMEOUT
# above), asserts HTTP 200, optionally pipes the body to jq for a
# field assertion. The jq-test block is `jq -e '.<expr>'` — a
# falsy / nonexistent value fails the check.
check() {
  expect_status 200 "$@"
}

# expect_status STATUS NAME PATH [-- jq-test]
#
# Variant of [check] that asserts an arbitrary HTTP status. Used for
# negative checks like:
#
#   expect_status 400 "coins bad cursor" "/v1/coins?cursor=garbage" \
#     -- '.type | endswith("/invalid-cursor")'
#
# The jq-test runs against the response body — useful for asserting
# the problem+json error type, not just "some 4xx". Behavioural
# pinning catches regressions that flip a documented 400 into a
# silent 200-with-empty-body (the class of bug that motivated this
# helper — see #1134 / #1135 for context).
expect_status() {
  local want_status="$1" name="$2" path="$3"
  shift 3
  local jq_check=""
  if [ "${1:-}" = "--" ]; then
    shift
    jq_check="$1"
  fi

  local body status
  # User-Agent: ratesengine-smoke/N — the API's obs.HTTPMetrics
  # middleware excludes synthetic traffic from histograms so the
  # SLO recording rule isn't polluted by the smoke timer's cold-
  # cache fan-out (every 5 min × N endpoints).
  body="$(curl -sS -m "$TIMEOUT" -A "ratesengine-smoke/1" -w "\n%{http_code}" "${API_BASE_URL}${path}" 2>&1)" || {
    printf "  %sFAIL%s %-32s %s%s%s\n" "$RED" "$OFF" "$name" "$DIM" "curl error" "$OFF"
    FAILS=$((FAILS + 1))
    return
  }
  status="$(echo "$body" | tail -1)"
  body="$(echo "$body" | sed '$d')"

  if [ "$status" != "$want_status" ]; then
    printf "  %sFAIL%s %-32s %sHTTP %s (want %s) — %s%s\n" \
      "$RED" "$OFF" "$name" "$DIM" "$status" "$want_status" "${body:0:80}" "$OFF"
    FAILS=$((FAILS + 1))
    return
  fi

  if [ -n "$jq_check" ]; then
    if ! echo "$body" | jq -e "$jq_check" >/dev/null 2>&1; then
      printf "  %sFAIL%s %-32s %sjq assertion failed: %s%s\n" \
        "$RED" "$OFF" "$name" "$DIM" "$jq_check" "$OFF"
      FAILS=$((FAILS + 1))
      return
    fi
  fi

  printf "  %sok%s   %-32s %s%s%s\n" "$GREEN" "$OFF" "$name" "$DIM" "${path}" "$OFF"
}

echo "Smoke-test: ${API_BASE_URL}"
echo

echo "  Health"
check "healthz"            "/v1/healthz" -- '.data.status == "ok"'
check "readyz"             "/v1/readyz"
check "version"            "/v1/version" -- '.data.version != null'
check "status"             "/v1/status"  -- '.data.overall'
echo

echo "  Catalogue"
check "coins (top 5)"      "/v1/coins?limit=5"  -- '.data.coins | length > 0'
check "coin native"        "/v1/coins/native"   -- '.data.code == "XLM"'
check "assets (5)"         "/v1/assets?limit=5" -- '.data | length > 0'
check "markets (5)"        "/v1/markets?limit=5"
check "sources"            "/v1/sources"
check "issuers (5)"        "/v1/issuers?limit=5"
check "currencies"         "/v1/currencies"     -- '.data.currencies | length > 0'
check "lending pools"      "/v1/lending/pools"
check "sac wrappers"       "/v1/sac-wrappers"
echo

echo "  Pricing"
check "price native/USD"   "/v1/price?asset=native&quote=fiat:USD" -- '.data.price | tonumber > 0'
check "ohlc USDC/XLM"      "/v1/ohlc?base=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN&quote=native"
check "history (last 10)"  "/v1/history?base=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN&quote=native&limit=10"
echo

echo "  Diagnostics"
check "cursors"            "/v1/diagnostics/cursors"
check "oracle latest"      "/v1/oracle/latest?asset=native"
check "network stats"      "/v1/network/stats"
check "incidents"          "/v1/incidents"
check "incidents.atom"     "/v1/incidents.atom"
echo

echo "  Discovery"
# Service-discovery surfaces that crawlers + AI agents rely on to
# find the API. A 404 here is silent — search engines don't always
# retry. (robots.txt is currently served by Cloudflare's
# auto-content-signals path, not the API binary; the check still
# verifies "a 200 reaches the client" which is what crawlers see.)
# /.well-known/security.txt follows once PR #1131 lands in r1.
check "robots.txt"         "/robots.txt"
echo

echo "  Behaviour pins"
# These don't just check liveness — they verify the API still
# returns the documented error envelope, so a regression that
# weakens a documented 4xx into a silent 200 (the class of bug
# behind #1134) would fail the smoke immediately.
expect_status 400 "coins bad limit"      "/v1/coins?limit=999999" \
  -- '.type | endswith("/invalid-limit")'
expect_status 404 "coins not found"      "/v1/coins/this-asset-id-does-not-exist" \
  -- '.type | endswith("/coin-not-found")'
# /v1/coins?cursor=garbage 400 (#1134) follows once the fix is
# deployed — adding the pin now would false-fail every smoke run
# on the older binary. Promote in the follow-up PR cut after
# #1134 lands in r1.
echo

if [ "$FAILS" -eq 0 ]; then
  printf "%sAll checks passed.%s\n" "$GREEN" "$OFF"
else
  printf "%s%s check(s) FAILED.%s\n" "$RED" "$FAILS" "$OFF"
fi

exit "$FAILS"
