#!/usr/bin/env bash
#
# audit-public-api.sh — exercise every public anonymous-tier
# endpoint with the example values published in openapi/. The
# Scalar docs UI's "Send" button uses these examples verbatim; if
# they don't return 200, the docs are misleading users.
#
# Reported 2026-05-08: many Scalar default test requests returned
# 4xx because OpenAPI examples used short symbols like `USDC` /
# `XLM` against handlers that strict-validate canonical asset
# IDs. This script catches that class of regression.
#
# Usage:
#   bash scripts/dev/audit-public-api.sh
#   API_BASE_URL=https://api.ratesengine.net bash scripts/dev/audit-public-api.sh
#
# Exit code = number of failed checks (0 = all green). Bodies of
# failed responses are printed so the caller can see why.

set -uo pipefail

API_BASE_URL="${API_BASE_URL:-https://api.ratesengine.net}"
TIMEOUT="${AUDIT_TIMEOUT:-10}"
FAILS=0
TOTAL=0

if [ -t 1 ]; then
  GREEN="$(printf '\033[32m')"
  RED="$(printf '\033[31m')"
  DIM="$(printf '\033[2m')"
  OFF="$(printf '\033[0m')"
else
  GREEN=""; RED=""; DIM=""; OFF=""
fi

# USDC issuer (Centre) — the canonical USDC contract on Stellar.
# Pinned here so a single edit covers every audit case using USDC
# as base or quote. Same value lives as the default example in
# openapi/rates-engine.v1.yaml#components.parameters.Base.
readonly USDC="USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

audit() {
  local label="$1" path="$2"
  local body status
  TOTAL=$((TOTAL + 1))
  body=$(/usr/bin/curl -sS -m "$TIMEOUT" -A "ratesengine-audit/1" \
    -w "\n%{http_code}" "${API_BASE_URL}${path}" 2>&1) || {
    printf "  %sFAIL%s %-44s %scurl error%s\n" "$RED" "$OFF" "$label" "$DIM" "$OFF"
    FAILS=$((FAILS + 1))
    return
  }
  status=$(echo "$body" | tail -1)
  body=$(echo "$body" | sed '$d')
  if [ "$status" = "200" ]; then
    printf "  %sok  %s %-44s %s%s%s\n" "$GREEN" "$OFF" "$label" "$DIM" "$path" "$OFF"
  else
    detail=$(echo "$body" | python3 -c \
      "import sys,json; d=json.load(sys.stdin); print(d.get('detail',d.get('title','?'))[:160])" \
      2>/dev/null)
    [ -z "$detail" ] && detail=$(echo "$body" | head -c 120)
    printf "  %sFAIL%s %-44s %sHTTP %s — %s%s\n" "$RED" "$OFF" "$label" "$DIM" "$status" "$detail" "$OFF"
    FAILS=$((FAILS + 1))
  fi
}

echo "Audit: ${API_BASE_URL}"
echo

# Health + meta
audit "healthz"                "/v1/healthz"
audit "readyz"                 "/v1/readyz"
audit "version"                "/v1/version"
audit "status"                 "/v1/status"
audit "network/stats"          "/v1/network/stats"
audit "diagnostics/cursors"    "/v1/diagnostics/cursors"
audit "incidents"              "/v1/incidents"

# Catalogue
# /v1/coins + /v1/currencies were removed in rc.48; the unified
# /v1/assets surface replaces both. F-1204 (codex audit-2026-05-12).
audit "assets (5)"             "/v1/assets?limit=5"
audit "assets/{id}=native"     "/v1/assets/native"
audit "assets metadata=native" "/v1/assets/native/metadata"
audit "assets verified"        "/v1/assets/verified"
audit "assets/{slug}=xlm"      "/v1/assets/xlm"
audit "assets/{slug}=usdc"     "/v1/assets/usdc"
audit "assets/{slug}=euro"     "/v1/assets/euro"
audit "markets (5)"            "/v1/markets?limit=5"
audit "sources"                "/v1/sources"
audit "issuers (5)"            "/v1/issuers?limit=5"
audit "sac-wrappers"           "/v1/sac-wrappers"
audit "lending/pools"          "/v1/lending/pools?limit=5"
audit "oracle/streams"         "/v1/oracle/streams"

# Pricing — every documented example uses canonical asset IDs.
audit "price native/USD"       "/v1/price?asset=native&quote=fiat:USD"
audit "price USDC/native"      "/v1/price?asset=${USDC}&quote=native"
audit "price/tip native"       "/v1/price/tip?asset=native"
audit "price/batch"            "/v1/price/batch?asset_ids=native,${USDC}"
audit "ohlc USDC/native"       "/v1/ohlc?base=${USDC}&quote=native"
audit "vwap USDC/native"       "/v1/vwap?base=${USDC}&quote=native"
audit "twap USDC/native"       "/v1/twap?base=${USDC}&quote=native"
audit "history USDC/native"    "/v1/history?base=${USDC}&quote=native&limit=5"
audit "history-since-inception" "/v1/history/since-inception?asset=native&quote=${USDC}"
audit "chart native/USD"       "/v1/chart?asset=native&quote=fiat:USD&timeframe=24h&granularity=1h"
audit "pairs base=native"      "/v1/pairs?base=native&quote=${USDC}"
audit "observations native"    "/v1/observations?asset=native&limit=5"

# Oracle
audit "oracle/latest native"   "/v1/oracle/latest?asset=native"
audit "oracle/lastprice"       "/v1/oracle/lastprice?asset=crypto:XLM"
audit "oracle/prices native"   "/v1/oracle/prices?asset=native"
audit "oracle/x_last_price"    "/v1/oracle/x_last_price?base=native&quote=${USDC}"

echo
if [ "$FAILS" -eq 0 ]; then
  printf "%s%d / %d audit checks passed%s\n" "$GREEN" "$TOTAL" "$TOTAL" "$OFF"
else
  printf "%s%d / %d audit checks failed%s\n" "$RED" "$FAILS" "$TOTAL" "$OFF"
fi
exit "$FAILS"
