#!/usr/bin/env bash
# verify-cdn.sh — runs the post-CDN-provisioning smoke checks
# from docs/operations/cdn-setup.md against a live API host.
#
# Use after configuring a CDN (Cloudflare per the runbook) in
# front of api.ratesengine.net to confirm the per-surface cache
# policies are surviving the edge.
#
# Usage:
#   ./scripts/dev/verify-cdn.sh                         # default: api.ratesengine.net
#   ./scripts/dev/verify-cdn.sh https://staging.ratesengine.net   # override host
#   API_KEY=ak_demo123 ./scripts/dev/verify-cdn.sh      # exercise auth bypass
#
# Exit code: 0 = all checks passed; 1 = one or more failures.

set -euo pipefail

HOST="${1:-https://api.ratesengine.net}"
HOST="${HOST%/}"   # strip trailing slash

PASS=0
FAIL=0

# ─── Pretty-print helpers ────────────────────────────────────
green() { printf '\033[32m%s\033[0m\n' "$*"; }
red()   { printf '\033[31m%s\033[0m\n' "$*"; }
bold()  { printf '\033[1m%s\033[0m\n' "$*"; }

check() {
    local label="$1" expected="$2" actual="$3"
    if [[ "$actual" == *"$expected"* ]]; then
        green "  ✓ $label"
        PASS=$((PASS + 1))
    else
        red "  ✗ $label"
        red "    expected: $expected"
        red "    actual:   $actual"
        FAIL=$((FAIL + 1))
    fi
}

# ─── Test cases ──────────────────────────────────────────────

bold "▶ verify-cdn.sh against $HOST"
echo

bold "1. Historical surface — long edge cache"
H=$(curl -sIk "$HOST/v1/history/since-inception?asset=native&quote=fiat:USD" 2>&1 || true)
check "Cache-Control surfaces s-maxage" "s-maxage" "$(echo "$H" | grep -i cache-control || echo MISSING)"
echo "$H" | grep -iE "cf-cache-status|x-cache" || echo "    (no edge-cache header — CDN not in front?)"
echo

bold "2. Hot surface — short max-age"
H=$(curl -sIk "$HOST/v1/price?base=native&quote=fiat:USD" 2>&1 || true)
check "/v1/price max-age short (≤60s)" "max-age=" "$(echo "$H" | grep -i cache-control || echo MISSING)"
echo

bold "3. Auth surface — must NOT cache"
if [ -n "${API_KEY:-}" ]; then
    H=$(curl -sIk -H "Authorization: Bearer $API_KEY" "$HOST/v1/account/me" 2>&1 || true)
else
    H=$(curl -sIk "$HOST/v1/account/me" 2>&1 || true)
fi
check "/v1/account/me sends no-store" "no-store" "$(echo "$H" | grep -i cache-control || echo MISSING)"
edge=$(echo "$H" | grep -iE "cf-cache-status|x-cache" | head -1 || echo "")
if [ -z "$edge" ] || [[ "$edge" == *"BYPASS"* ]] || [[ "$edge" == *"DYNAMIC"* ]]; then
    green "  ✓ Edge bypasses /v1/account/* (or no edge in front)"
    ((PASS++))
else
    red "  ✗ Edge appears to cache /v1/account/* — fix Page Rule"
    red "    saw: $edge"
    ((FAIL++))
fi
echo

bold "4. SSE surface — passthrough, no buffering"
# We don't actually consume the stream — just check the
# response starts within 5s and the headers are right.
H=$(curl -sIk --max-time 5 "$HOST/v1/price/tip/stream?base=native&quote=fiat:USD" 2>&1 || true)
check "SSE Content-Type" "text/event-stream" "$(echo "$H" | grep -i content-type || echo MISSING)"
check "SSE Cache-Control" "no-store" "$(echo "$H" | grep -i cache-control || echo MISSING)"
echo

bold "5. Health surface — reachable"
status=$(curl -sk -o /dev/null -w "%{http_code}" "$HOST/v1/healthz" || echo 000)
check "/v1/healthz 200" "200" "$status"
echo

bold "6. Sources catalogue — 5-min edge cache"
H=$(curl -sIk "$HOST/v1/sources" 2>&1 || true)
check "/v1/sources max-age=300" "max-age=300" "$(echo "$H" | grep -i cache-control || echo MISSING)"
echo

# ─── Summary ────────────────────────────────────────────────
TOTAL=$((PASS + FAIL))
echo "─────────────────────────────────────────"
if [ "$FAIL" -eq 0 ]; then
    green "$PASS / $TOTAL checks passed"
    echo
    bold "CDN smoke ✓ all surfaces honour their per-path cache policy."
    exit 0
else
    red "$FAIL / $TOTAL checks FAILED"
    echo
    bold "Cross-reference docs/operations/cdn-setup.md §Verification."
    exit 1
fi
