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
# STATUS may be a single code (`200`) OR a `|`-separated set of
# acceptable codes (`"200|404"`) — the latter for routes whose 4xx
# is data-dependent (e.g. /v1/ohlc returns 404 errors/no-trades when
# the test pair has no recent trades; /v1/oracle/prices returns
# 404 when no oracle has reported for the asset in the window).
# Dual-status acceptance lets the smoke still catch a 5xx regression
# without false-failing on documented-empty windows (F-0156).
#
# The jq-test runs against the response body — useful for asserting
# the problem+json error type, not just "some 4xx". Behavioural
# pinning catches regressions that flip a documented 400 into a
# silent 200-with-empty-body (the class of bug that motivated this
# helper — see #1134 / #1135 for context). When STATUS is multi-
# valued, the jq-test runs against whatever body came back — keep
# it generic ('.type? != null') or omit it for those checks.
expect_status() {
  local want_status="$1" name="$2" path="$3"
  shift 3
  local jq_check=""
  local per_check_timeout="$TIMEOUT"
  while [ "${1:-}" != "" ]; do
    case "$1" in
      --)
        shift
        jq_check="$1"
        shift
        ;;
      --timeout)
        shift
        per_check_timeout="$1"
        shift
        ;;
      *)
        # Unknown trailing arg — leave for the caller to debug.
        break
        ;;
    esac
  done

  local body status
  # User-Agent: ratesengine-smoke/N — the API's obs.HTTPMetrics
  # middleware excludes synthetic traffic from histograms so the
  # SLO recording rule isn't polluted by the smoke timer's cold-
  # cache fan-out (every 5 min × N endpoints).
  #
  # URL is passed through `printf '%s'` rather than expanded inline
  # so any ${path} character that looks shell-special (`-`, `:`,
  # asset_ids with hyphens) is treated as literal. Mitigates F-0157
  # — the `assets/AAAA-G…` behaviour-pin was reporting "curl error"
  # because the asset-not-found resolver branch took 4-5 s on a
  # cold cache, which crossed the original 10 s budget in only the
  # worst case. The per-check `--timeout` flag lets data-dependent
  # paths declare a more generous budget without inflating the
  # global default.
  local url
  url="$(printf '%s%s' "$API_BASE_URL" "$path")"
  body="$(curl -sS -m "$per_check_timeout" -A "ratesengine-smoke/1" -w "\n%{http_code}" "$url" 2>&1)" || {
    printf "  %sFAIL%s %-32s %s%s (timeout=%ss)%s\n" "$RED" "$OFF" "$name" "$DIM" "curl error" "$per_check_timeout" "$OFF"
    FAILS=$((FAILS + 1))
    return
  }
  status="$(echo "$body" | tail -1)"
  body="$(echo "$body" | sed '$d')"

  # Multi-status acceptance: "200|404" matches either.
  local matched=0
  case "|$want_status|" in
    *"|$status|"*) matched=1 ;;
  esac
  if [ "$matched" -ne 1 ]; then
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
# /v1/coins + /v1/currencies removed in rc.48 (28ac6ac9 +
# 80c57e38); every consumer moved to /v1/assets per the F-1201
# audit-2026-05-12 migration. Smoke checks updated to match.
check "assets (5)"         "/v1/assets?limit=5" -- '.data | length > 0'
check "asset native"       "/v1/assets/native"  -- '.data.asset_id == "native"'
check "assets verified"    "/v1/assets/verified" -- '.data | length > 0'
check "markets (5)"        "/v1/markets?limit=5"
check "sources"            "/v1/sources"
check "issuers (5)"        "/v1/issuers?limit=5"
check "lending pools"      "/v1/lending/pools"
check "sac wrappers"       "/v1/sac-wrappers"
echo

echo "  Pricing"
check "price native/USD"   "/v1/price?asset=native&quote=fiat:USD" -- '.data.price | tonumber > 0'
check "price tip native/USD" "/v1/price/tip?asset=native&quote=fiat:USD"
# /v1/ohlc returns 404 errors/no-trades on empty windows per ADR-0018.
# The smoke runs every 5 min — a cold pair with no recent trades is
# the documented contract, not a regression. F-0156: accept both 200
# and 404; only 5xx (route broken) or 400 (param contract slip) fail.
expect_status "200|404" "ohlc USDC/XLM"  "/v1/ohlc?base=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN&quote=native"
check "history (last 10)"  "/v1/history?base=USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN&quote=native&limit=10"
check "pairs native/USD"   "/v1/pairs?base=native&quote=fiat:USD"
echo

echo "  VWAP / TWAP"
# Cascade-affected (commit a91f901b): Redis MISCONF should surface
# as 503 errors/cache-unavailable, NOT 500 errors/internal. The
# smoke verifies the success path; the chaos suite verifies the
# 503 mapping under fault injection (test/chaos/scenarios/
# 04-redis-misconf.sh). 200|404 because either pair could be a
# no-trades window during cold periods.
expect_status "200|404" "vwap native/USD" "/v1/vwap?base=native&quote=fiat:USD"
expect_status "200|404" "twap native/USD" "/v1/twap?base=native&quote=fiat:USD"
echo

echo "  Oracle (SEP-40 surface)"
# Cascade-affected — same 503-on-MISCONF mapping. /v1/oracle/prices
# and /v1/oracle/lastprice can legitimately 404 when no oracle has
# reported for the asset in the configured window; treat as
# acceptable here, the chaos suite covers the 503 cascade case.
check "oracle latest"      "/v1/oracle/latest?asset=native"
expect_status "200|404" "oracle lastprice native" "/v1/oracle/lastprice?asset=native"
expect_status "200|404" "oracle prices native"    "/v1/oracle/prices?asset=native"
check "oracle streams"     "/v1/oracle/streams"
expect_status "200|404" "oracle x_last_price"     "/v1/oracle/x_last_price?base=native&quote=fiat:USD"
echo

echo "  Diagnostics"
check "cursors"            "/v1/diagnostics/cursors"
# F-0095 / 77bcd8c2: /v1/diagnostics/ingestion now surfaces a stale
# flag on soft-fail builds; the route must remain available.
check "diagnostics ingestion" "/v1/diagnostics/ingestion"
check "ledger tip"         "/v1/ledger/tip" -- '.data.latest_ledger | tonumber > 0'
check "network stats"      "/v1/network/stats"
check "incidents"          "/v1/incidents"
check "incidents.atom"     "/v1/incidents.atom"
echo

echo "  Customer surfaces"
# Positive customer-facing surface: /v1/methodology is the
# integrator-readable methodology slice (R-023). Pin both the
# success path and the price_method=vwap invariant — flipping it
# to "twap" or removing the field would silently break every
# integrator's "verify the rates engine still uses VWAP" check.
check "methodology"        "/v1/methodology" -- '.data.aggregation.price_method == "vwap"'
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
expect_status 400 "assets bad limit"     "/v1/assets?limit=999999" \
  -- '.type | endswith("/invalid-limit")'
# Use a well-formed-but-nonexistent classic asset_id (random
# 4-char code against a real but unrelated G-strkey) so the API
# accepts the shape, falls through to a real catalogue lookup,
# and returns the documented 404 — distinct from the 400 path
# for ill-formed inputs.
expect_status 404 "asset not found"      "/v1/assets/AAAA-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN" \
  --timeout 30 \
  -- '.type | endswith("/asset-not-found")'

# Pins queued for promotion once rc.38 deploys. Each verifies a
# documented behaviour shipped in this session that's currently
# wrong on rc.37 — adding them as live `expect_status` calls now
# would false-fail every smoke run against the unpatched binary.
# Uncomment after rc.38 reaches r1 (signal: `/v1/version` data.version
# == v0.5.0-rc.38). The PRs below are all in main:
#
#   /v1/coins?cursor=garbage 400  (#1134, in main)
#   /v1/markets?cursor=garbage 400 (#1135)
#   /v1/markets?source=fakesrc 400 (#1162)
#   /v1/observations?source=fakesrc 400 (#1164)
#   /v1/oracle/latest?source=fakesrc 400 (#1168)
#   /metrics 404 from public host (#1172 + binary loopback gate #1207)
#   /.well-known/security.txt 200 (#1131)
#   /v1/markets?asset=USDC 400 invalid-asset-id (#1189)
#   /v1/markets?source=binance&asset=native 400 conflicting-filters (#1189)
#   /v1/pools?asset=USDC 400 invalid-asset-id (#1190)
#   /v1/pools?asset=native&base=native 400 conflicting-filters (#1190)
#
# Worked-example template ready to uncomment + adjust:
#
#   expect_status 400 "markets bad cursor" "/v1/markets?cursor=garbage" \
#     -- '.type | endswith("/invalid-cursor")'
#   expect_status 400 "markets unknown source" "/v1/markets?source=does-not-exist" \
#     -- '.type | endswith("/unknown-source")'
#   expect_status 404 "metrics blocked publicly" "/metrics"
#   expect_status 200 "security.txt" "/.well-known/security.txt"
echo

if [ "$FAILS" -eq 0 ]; then
  printf "%sAll checks passed.%s\n" "$GREEN" "$OFF"
else
  printf "%s%s check(s) FAILED.%s\n" "$RED" "$FAILS" "$OFF"
fi

exit "$FAILS"
