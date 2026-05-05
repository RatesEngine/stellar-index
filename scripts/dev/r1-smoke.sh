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

set -uo pipefail

API_BASE_URL="${API_BASE_URL:-http://localhost:3000}"
TIMEOUT="${SMOKE_TIMEOUT:-5}"
FAILS=0

# ANSI colour helpers — disabled when stdout isn't a TTY (cron,
# Healthchecks.io captures, log files).
if [ -t 1 ]; then
  GREEN="$(printf '\033[32m')"; RED="$(printf '\033[31m')"; DIM="$(printf '\033[2m')"; OFF="$(printf '\033[0m')"
else
  GREEN=""; RED=""; DIM=""; OFF=""
fi

# check NAME PATH [-- jq-test]   — runs GET ${BASE}${PATH} with a 5s
# budget, asserts HTTP 200, optionally pipes the body to jq for a
# field assertion. The jq-test block is `jq -e '.<expr>'` — a
# falsy / nonexistent value fails the check.
check() {
  local name="$1" path="$2"
  shift 2
  local jq_check=""
  if [ "${1:-}" = "--" ]; then
    shift
    jq_check="$1"
  fi

  local body status
  body="$(curl -sS -m "$TIMEOUT" -w "\n%{http_code}" "${API_BASE_URL}${path}" 2>&1)" || {
    printf "  %sFAIL%s %-32s %s%s%s\n" "$RED" "$OFF" "$name" "$DIM" "curl error" "$OFF"
    FAILS=$((FAILS + 1))
    return
  }
  status="$(echo "$body" | tail -1)"
  body="$(echo "$body" | sed '$d')"

  if [ "$status" != "200" ]; then
    printf "  %sFAIL%s %-32s %sHTTP %s — %s%s\n" "$RED" "$OFF" "$name" "$DIM" "$status" "${body:0:80}" "$OFF"
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
check "coins (top 5)"      "/v1/coins?limit=5"  -- '.data | length > 0'
check "assets (5)"         "/v1/assets?limit=5" -- '.data | length > 0'
check "markets (5)"        "/v1/markets?limit=5"
check "sources"            "/v1/sources"
echo

echo "  Pricing"
check "price native/USD"   "/v1/price?asset=native&quote=fiat:USD" -- '.data.price | tonumber > 0'
check "ohlc USDC/XLM"      "/v1/ohlc?base=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN&quote=native"
check "history (last 10)"  "/v1/history?base=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN&quote=native&limit=10"
echo

echo "  Diagnostics"
check "cursors"            "/v1/diagnostics/cursors"
check "oracle latest"      "/v1/oracle/latest?asset=native"
echo

if [ "$FAILS" -eq 0 ]; then
  printf "%sAll checks passed.%s\n" "$GREEN" "$OFF"
else
  printf "%s%s check(s) FAILED.%s\n" "$RED" "$FAILS" "$OFF"
fi

exit "$FAILS"
