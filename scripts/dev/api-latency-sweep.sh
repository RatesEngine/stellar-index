#!/usr/bin/env bash
#
# api-latency-sweep.sh — granular latency profile of the entire
# anonymous public GET surface. The "kitchen sink": hit every
# non-streaming, non-auth endpoint N times, compute p50/p95/p99/max,
# rank slowest-first, flag anything over the SLO (p95 < 200 ms)
# and a hard "cause for concern" ceiling (>1 s).
#
# This is a DIAGNOSTIC, not a contract test — r1-smoke.sh pins
# behaviour/shape; this measures *speed* and surfaces what to fix.
# Non-2xx is reported (ok%), never a hard fail by itself; the exit
# code counts only endpoints over the CRIT latency ceiling so cron /
# Healthchecks.io can consume it.
#
# Isolation matters:
#   - Run ON the host against localhost  → pure server compute
#       ssh root@r1 'bash -s' < scripts/dev/api-latency-sweep.sh
#   - Run from a VPS against the public URL → + network + edge/Caddy
#       API_BASE_URL=https://api.stellarindex.io bash …api-latency-sweep.sh
#   - Point at r2/r3 the same way → cross-region comparison
#
# Usage / knobs (env):
#   API_BASE_URL   default http://localhost:3000
#   ITERS          samples per endpoint (default 20)
#   SWEEP_TIMEOUT  per-request cap, seconds (default 15)
#   CACHE_BUST=1   append a unique query param each hit → exposes the
#                  UNCACHED server cost (the "should be cached but
#                  isn't" signal). Off = realistic warm behaviour.
#   WARMUP=1       discard the first hit per endpoint (prime caches)
#   JSON=1         emit a JSON array instead of the table (machine /
#                  cross-region diffing)
#
# Flags:  --spec-check   list OpenAPI GET paths NOT covered here
#                         (anti-rot; run from a repo checkout) then exit
#
set -uo pipefail

API_BASE_URL="${API_BASE_URL:-http://localhost:3000}"
ITERS="${ITERS:-20}"
TIMEOUT="${SWEEP_TIMEOUT:-15}"
CACHE_BUST="${CACHE_BUST:-0}"
WARMUP="${WARMUP:-0}"
JSON="${JSON:-0}"

# SLO p95 target = 200 ms. >1 s = "cause for concern" (CRIT).
WARN_MS=200
CRIT_MS=1000

if [ -t 1 ] && [ "$JSON" != "1" ]; then
  G="$(printf '\033[32m')"; Y="$(printf '\033[33m')"; R="$(printf '\033[31m')"
  B="$(printf '\033[1m')"; D="$(printf '\033[2m')"; O="$(printf '\033[0m')"
else G=""; Y=""; R=""; B=""; D=""; O=""; fi

# A real on-chain asset that exists on pubnet with history (the USDC
# issuer r1-smoke.sh uses) + an issuer strkey for the issuer route.
U="USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
ISS="GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

# name|path  — the anonymous, non-streaming GET surface. Streaming
# SSE (/v1/*/stream, /v1/oracle/streams) and auth-gated routes
# (/v1/account/*, /v1/dashboard/*, /v1/auth/*, /v1/signup/*) are
# intentionally excluded (they'd hang or 401 — meaningless here).
# --spec-check diffs this against the OpenAPI spec so it can't rot.
ENDPOINTS=(
  "healthz|/v1/healthz"
  "readyz|/v1/readyz"
  "version|/v1/version"
  "status|/v1/status"
  "methodology|/v1/methodology"
  "sources|/v1/sources"
  "assets|/v1/assets?limit=25"
  "assets-verified|/v1/assets/verified"
  "asset-canonical|/v1/assets/native"
  "asset-slug|/v1/assets/usdc"
  "asset-network|/v1/assets/usdc/stellar"
  "asset-metadata|/v1/assets/native/metadata"
  "price|/v1/price?asset=native&quote=fiat:USD"
  "price-tip|/v1/price/tip?asset=native&quote=fiat:USD"
  "price-batch|/v1/price/batch?pairs=native/fiat:USD,${U}/native"
  "vwap|/v1/vwap?base=${U}&quote=native"
  "twap|/v1/twap?base=${U}&quote=native"
  "ohlc|/v1/ohlc?base=${U}&quote=native"
  "chart|/v1/chart?base=${U}&quote=native"
  "history|/v1/history?base=${U}&quote=native&limit=10"
  "history-since-inception|/v1/history/since-inception?base=${U}&quote=native"
  "observations|/v1/observations?base=${U}&quote=native&limit=10"
  "oracle-latest|/v1/oracle/latest?asset=native"
  "oracle-lastprice|/v1/oracle/lastprice?asset=native"
  "oracle-prices|/v1/oracle/prices"
  "oracle-x-last-price|/v1/oracle/x_last_price?base=native&quote=fiat:USD"
  "markets|/v1/markets?limit=25"
  "pools|/v1/pools"
  "lending-pools|/v1/lending/pools"
  "pairs|/v1/pairs"
  "sac-wrappers|/v1/sac-wrappers"
  "issuers|/v1/issuers?limit=25"
  "issuer-one|/v1/issuers/${ISS}"
  "changes|/v1/changes/asset/native"
  "diagnostics-cursors|/v1/diagnostics/cursors"
  "diagnostics-ingestion|/v1/diagnostics/ingestion"
  "incidents|/v1/incidents"
  "incidents-atom|/v1/incidents.atom"
  "network-stats|/v1/network/stats"
)

if [ "${1:-}" = "--spec-check" ]; then
  command -v python3 >/dev/null || { echo "python3 required for --spec-check"; exit 2; }
  covered="$(printf '%s\n' "${ENDPOINTS[@]}" | sed 's/^[^|]*|//; s/?.*//')"
  python3 - "$covered" <<'PY'
import re, sys
covered = set(sys.argv[1].split())
inpaths=False; cur=None; gets=[]
for ln in open('openapi/stellar-index.v1.yaml'):
    if re.match(r'^paths:', ln): inpaths=True; continue
    if inpaths:
        if re.match(r'^[A-Za-z]', ln): break
        m=re.match(r'^  (/\S+):', ln)
        if m: cur=m.group(1); continue
        if re.match(r'^    get:', ln) and cur: gets.append(cur)
SKIP=('stream','/account/','/dashboard/','/auth/','/signup/')
missing=[g for g in gets
         if not any(s in g for s in SKIP)
         and ('/v1'+g) not in covered and g not in covered]
if missing:
    print("Spec GET paths NOT covered by the sweep (templated paths"
          " may be covered with a fixture — verify):")
    for m in missing: print("  ", m)
else:
    print("All non-streaming/non-auth spec GET paths are covered.")
PY
  exit 0
fi

# pctl PCT  < newline-separated sorted-ascending integer ms list
pctl() { sort -n | awk -v p="$1" '
  {a[NR]=$1} END{ if(NR==0){print 0; exit}
  i=int((p/100)*NR + 0.999999); if(i<1)i=1; if(i>NR)i=NR; print a[i] }'; }

CRIT=0
RESULTS=()
for e in "${ENDPOINTS[@]}"; do
  name="${e%%|*}"; path="${e#*|}"
  times=""; okc=0; n=0
  [ "$WARMUP" = "1" ] && curl -s -o /dev/null --max-time "$TIMEOUT" "${API_BASE_URL}${path}" >/dev/null 2>&1
  for ((i=0;i<ITERS;i++)); do
    u="${API_BASE_URL}${path}"
    if [ "$CACHE_BUST" = "1" ]; then
      case "$path" in *\?*) u="${u}&_cb=$RANDOM$i";; *) u="${u}?_cb=$RANDOM$i";; esac
    fi
    read -r code tt < <(curl -s -o /dev/null --max-time "$TIMEOUT" \
      -w '%{http_code} %{time_total}\n' "$u" 2>/dev/null || echo "000 $TIMEOUT")
    ms=$(awk -v t="${tt:-$TIMEOUT}" 'BEGIN{printf "%d", t*1000}')
    times+="${ms}"$'\n'; n=$((n+1))
    case "$code" in 2*) okc=$((okc+1));; esac
  done
  ts="$(printf '%s' "$times" | grep -v '^$')"
  p50=$(printf '%s\n' "$ts" | pctl 50)
  p95=$(printf '%s\n' "$ts" | pctl 95)
  p99=$(printf '%s\n' "$ts" | pctl 99)
  mx=$(printf '%s\n'  "$ts" | sort -n | tail -1)
  okp=$(awk -v o="$okc" -v n="$n" 'BEGIN{printf "%d", (n? o*100/n:0)}')
  [ "$p95" -gt "$CRIT_MS" ] && CRIT=$((CRIT+1))
  RESULTS+=("$p95|$name|$path|$n|$okp|$p50|$p99|$mx")
done

if [ "$JSON" = "1" ]; then
  printf '['
  first=1
  while IFS='|' read -r p95 name path n okp p50 p99 mx; do
    [ $first -eq 1 ] || printf ','
    first=0
    printf '{"name":"%s","path":"%s","n":%s,"ok_pct":%s,"p50_ms":%s,"p95_ms":%s,"p99_ms":%s,"max_ms":%s}' \
      "$name" "$path" "$n" "$okp" "$p50" "$p95" "$p99" "$mx"
  done < <(printf '%s\n' "${RESULTS[@]}" | sort -t'|' -k1,1nr)
  printf ']\n'
  exit "$CRIT"
fi

printf '%s%s vs %s — %s iters/endpoint%s%s\n' "$B" \
  "api-latency-sweep" "$API_BASE_URL" "$ITERS" \
  "$([ "$CACHE_BUST" = 1 ] && echo ' (cache-bust)')" "$O"
printf '%s%-26s %5s %4s %7s %7s %7s %7s%s\n' "$D" \
  "endpoint" "ok%" "n" "p50ms" "p95ms" "p99ms" "maxms" "$O"
while IFS='|' read -r p95 name path n okp p50 p99 mx; do
  if   [ "$p95" -gt "$CRIT_MS" ]; then c="$R"; tag="CRIT"
  elif [ "$p95" -gt "$WARN_MS" ]; then c="$Y"; tag="warn"
  else c="$G"; tag="ok"; fi
  oc=""; [ "$okp" -lt 100 ] && oc="$R"
  printf '%s%-26s%s %s%4s%s %4s %7s %s%7s%s %7s %7s  %s%s%s\n' \
    "$c" "$name" "$O" "$oc" "$okp" "$O" "$n" "$p50" "$c" "$p95" "$O" "$p99" "$mx" "$c" "$tag" "$O"
done < <(printf '%s\n' "${RESULTS[@]}" | sort -t'|' -k1,1nr)

echo
slow=$(printf '%s\n' "${RESULTS[@]}" | sort -t'|' -k1,1nr | head -1)
printf '%sslowest: %s (p95 %s ms)%s\n' "$B" \
  "$(echo "$slow"|cut -d'|' -f2)" "$(echo "$slow"|cut -d'|' -f1)" "$O"
if [ "$CRIT" -eq 0 ]; then
  printf '%sNo endpoint over the %sms concern ceiling.%s\n' "$G" "$CRIT_MS" "$O"
else
  printf '%s%s endpoint(s) over the %sms ceiling — investigate.%s\n' "$R" "$CRIT" "$CRIT_MS" "$O"
fi
exit "$CRIT"
