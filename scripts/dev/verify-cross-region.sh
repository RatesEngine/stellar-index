#!/usr/bin/env bash
# verify-cross-region.sh — assert closed-bucket VWAP values are
# byte-identical across R1 / R2 / R3 (per ADR-0015).
#
# This is the load-bearing cross-region consistency claim: a
# request to api.ratesengine.net resolves to whichever region is
# closest, but the response should be identical regardless. If a
# customer in EU sees a different price than a customer in APAC
# for the same closed bucket + same pair, ADR-0015 is broken and
# the multi-region story is fiction.
#
# Usage:
#   ./scripts/dev/verify-cross-region.sh
#       # default pair set, default per-region URLs
#   PAIRS='native,fiat:USD;USDC-G...,fiat:USD' \
#     ./scripts/dev/verify-cross-region.sh
#   R1=https://api-r1.ratesengine.net \
#     R2=https://api-r2.ratesengine.net \
#     R3=https://api-r3.ratesengine.net \
#     ./scripts/dev/verify-cross-region.sh
#
# Exit:
#   0 — every (region × pair) combination returned a price, AND
#       every pair's three regional prices are byte-identical.
#   1 — any pair has divergent prices across regions.
#   2 — at least one region failed to respond for a pair.

set -euo pipefail

# ─── Per-region URLs (override via env) ──────────────────────
R1="${R1:-https://api-r1.ratesengine.net}"
R2="${R2:-https://api-r2.ratesengine.net}"
R3="${R3:-https://api-r3.ratesengine.net}"

# ─── Pair set (override via PAIRS env, semicolon-separated) ──
DEFAULT_PAIRS="native,fiat:USD"
PAIRS="${PAIRS:-${DEFAULT_PAIRS}}"

# ─── Pretty-print helpers ────────────────────────────────────
green() { printf '\033[32m%s\033[0m\n' "$*"; }
red()   { printf '\033[31m%s\033[0m\n' "$*"; }
bold()  { printf '\033[1m%s\033[0m\n' "$*"; }

# ─── Workspace ───────────────────────────────────────────────
TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

# ─── Test loop ───────────────────────────────────────────────
PASS=0
FAIL_DIVERGE=0
FAIL_UNREACH=0

bold "▶ verify-cross-region against:"
echo "    R1=$R1"
echo "    R2=$R2"
echo "    R3=$R3"
echo

IFS=';' read -ra PAIR_LIST <<<"$PAIRS"
for pair in "${PAIR_LIST[@]}"; do
    pair="${pair## }"; pair="${pair%% }"   # trim
    [ -z "$pair" ] && continue
    base="${pair%%,*}"
    quote="${pair##*,}"

    bold "Pair: ${base}/${quote}"

    # Fetch /v1/price from each region. Plain shell vars (no
    # associative arrays — bash 3.2 on macOS doesn't have them).
    p1=""; p2=""; p3=""
    a1=""; a2=""; a3=""
    r1ok=no; r2ok=no; r3ok=no

    if ! command -v jq >/dev/null 2>&1; then
        red "  ✗ jq not installed; can't parse responses"
        exit 2
    fi

    for region_label in R1 R2 R3; do
        case "$region_label" in
            R1) host="$R1" ;;
            R2) host="$R2" ;;
            R3) host="$R3" ;;
        esac
        out_file="${TMPDIR}/${region_label}-${base//\//_}-${quote//\//_}.json"
        http_code=$(curl -sk -o "$out_file" -w "%{http_code}" \
            "${host}/v1/price?base=${base}&quote=${quote}" \
            --max-time 10 || echo 000)

        if [ "$http_code" != "200" ]; then
            red "  ✗ ${region_label} (${host}) returned http=${http_code}"
            FAIL_UNREACH=$((FAIL_UNREACH + 1))
            continue
        fi

        price=$(jq -r '.data.price' "$out_file" 2>/dev/null || echo "")
        as_of_v=$(jq -r '.as_of' "$out_file" 2>/dev/null || echo "")
        if [ -z "$price" ] || [ "$price" = "null" ]; then
            red "  ✗ ${region_label} returned 200 but body has no .data.price"
            FAIL_UNREACH=$((FAIL_UNREACH + 1))
            continue
        fi

        case "$region_label" in
            R1) p1="$price"; a1="$as_of_v"; r1ok=yes ;;
            R2) p2="$price"; a2="$as_of_v"; r2ok=yes ;;
            R3) p3="$price"; a3="$as_of_v"; r3ok=yes ;;
        esac
    done

    # Did we get a price from every region?
    if [ "$r1ok" != "yes" ] || [ "$r2ok" != "yes" ] || [ "$r3ok" != "yes" ]; then
        red "  → SKIP consistency check (not all regions reachable)"
        echo
        continue
    fi
    if [ "$p1" = "$p2" ] && [ "$p2" = "$p3" ]; then
        green "  ✓ all three regions: ${p1}"
        echo "    R1 as_of=${a1}"
        echo "    R2 as_of=${a2}"
        echo "    R3 as_of=${a3}"
        PASS=$((PASS + 1))
    else
        red "  ✗ DIVERGENCE — closed-bucket consistency violated (ADR-0015):"
        echo "    R1: price=${p1}  as_of=${a1}"
        echo "    R2: price=${p2}  as_of=${a2}"
        echo "    R3: price=${p3}  as_of=${a3}"
        echo "    If as_of timestamps differ across regions by more"
        echo "    than the bucket window (typically 1m), one region"
        echo "    is replication-lagged. Investigate before launch."
        FAIL_DIVERGE=$((FAIL_DIVERGE + 1))
    fi
    echo
done

# ─── Summary ─────────────────────────────────────────────────
echo "─────────────────────────────────────────"
TOTAL=$((PASS + FAIL_DIVERGE + FAIL_UNREACH))
if [ "$FAIL_DIVERGE" -eq 0 ] && [ "$FAIL_UNREACH" -eq 0 ]; then
    green "$PASS / $TOTAL pairs consistent across R1/R2/R3"
    bold "ADR-0015 closed-bucket consistency holds. ✓"
    exit 0
fi
if [ "$FAIL_DIVERGE" -gt 0 ]; then
    red "$FAIL_DIVERGE pairs DIVERGED — ADR-0015 contract broken."
fi
if [ "$FAIL_UNREACH" -gt 0 ]; then
    red "$FAIL_UNREACH region/pair attempts UNREACHABLE — incomplete check."
    bold "Run again once the affected region is healthy."
fi
echo
bold "Cross-reference docs/operations/multi-region-cutover.md §Stage 5."
[ "$FAIL_DIVERGE" -gt 0 ] && exit 1
exit 2
