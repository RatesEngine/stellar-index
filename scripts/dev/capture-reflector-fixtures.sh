#!/usr/bin/env bash
# Capture real Reflector UpdateEvent fixtures from a live stellar-rpc.
#
# Writes a JSON fixture per matched event to
# test/fixtures/reflector/<wasm_hash>/<ledger>_<tx>.json. Fixtures are
# consumed by internal/sources/reflector/decode_test.go (TODO:
# integration-tag replay path) — SDK-encoded tests in that file cover
# the wire-format path today; these real captures become the
# regression corpus once landed.
#
# Usage:
#   scripts/dev/capture-reflector-fixtures.sh \
#     [-e http://127.0.0.1:8000] \
#     [-c CALI2BYU...] \
#     [-n 10] \
#     [-s <start_ledger>]
#
# Flags:
#   -e  stellar-rpc endpoint (default: http://127.0.0.1:8000).
#       On r1 this is the local stellar-rpc admin port.
#   -c  Reflector contract ID (default: DEX variant from VERSIONS.md).
#       Other variants: CAFJZQWS… (CEX), CBKGPWGK… (FX).
#   -n  Max events to capture (default: 10). Reflector emits one
#       update every ~5 min per variant; 10 covers ~50 min of data.
#   -s  Start ledger (default: latestLedger - 200). Bump backwards if
#       you need fixtures across a longer window.
#
# Environment:
#   JQ               Path to jq binary. Default: `jq`. Required.
#   CURL             Path to curl binary. Default: `curl`. Required.
#
# Prerequisites:
#   - jq + curl on PATH.
#   - Network path to the stellar-rpc endpoint. On r1 the local
#     endpoint (http://127.0.0.1:8000) is always reachable; remote
#     probing requires a tunnel or VPN.
#
# Output format (per fixture):
#   {
#     "contract_id":  "C…",
#     "wasm_hash":    "…",                 -- from getLedgerEntries
#     "ledger":       52003412,
#     "tx_hash":      "deadbeef…",
#     "ledger_closed_at": "2026-04-23T…Z",
#     "topics":       ["AAAADwAA…","AAAADwAA…","AAAABQAA…"],
#     "value":        "AAAAEAAA…"
#   }
#
# See docs/architecture/contract-schema-evolution.md for WHY fixtures
# are pinned per WASM hash (we need to keep decoding old ledger ranges
# after the contract upgrades).

set -euo pipefail

ENDPOINT="${ENDPOINT:-http://127.0.0.1:8000}"
CONTRACT_ID="${CONTRACT_ID:-CALI2BYU2JE6WVRUFYTS6MSBNEHGJ35P4AVCZYF3B6QOE3QKOB2PLE6M}"
MAX_EVENTS="${MAX_EVENTS:-10}"
START_LEDGER=""
JQ="${JQ:-jq}"
CURL="${CURL:-curl}"

while getopts "e:c:n:s:h" opt; do
  case "$opt" in
    e) ENDPOINT="$OPTARG" ;;
    c) CONTRACT_ID="$OPTARG" ;;
    n) MAX_EVENTS="$OPTARG" ;;
    s) START_LEDGER="$OPTARG" ;;
    h|*)
      sed -n '2,/^set/p' "$0" | sed 's/^# \{0,1\}//'
      exit 0 ;;
  esac
done

command -v "$JQ" >/dev/null || { echo "jq not found" >&2; exit 127; }
command -v "$CURL" >/dev/null || { echo "curl not found" >&2; exit 127; }

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
FIXTURES_ROOT="$REPO_ROOT/test/fixtures/reflector"
mkdir -p "$FIXTURES_ROOT"

# Pre-encoded Symbol("REFLECTOR") / Symbol("update") — these are the
# same constants the decoder uses for classification. Hand-written
# here to avoid a Go build step in a shell script; keep in sync
# with internal/scval/scval_test.go TestGolden_symbolBytes.
TOPIC0='AAAADwAAAAlSRUZMRUNUT1IAAAA='
TOPIC1='AAAADwAAAAZ1cGRhdGUAAA=='

rpc_call() {
  local method="$1"
  local params="$2"
  "$CURL" -sS -X POST "$ENDPOINT" \
    -H 'Content-Type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$method\",\"params\":$params}"
}

# Resolve start ledger.
if [[ -z "$START_LEDGER" ]]; then
  latest_resp="$(rpc_call getLatestLedger '{}')"
  LATEST="$(echo "$latest_resp" | "$JQ" -r '.result.sequence')"
  [[ "$LATEST" == "null" || -z "$LATEST" ]] && { echo "getLatestLedger failed: $latest_resp" >&2; exit 1; }
  START_LEDGER=$((LATEST - 200))
  echo "latest ledger: $LATEST → starting from $START_LEDGER"
fi

# Resolve contract WASM hash — pins the fixture to a version per
# docs/architecture/contract-schema-evolution.md.
instance_key_payload="$("$JQ" -nc --arg c "$CONTRACT_ID" '
  {keys: [("AAAAB" + "...placeholder..." )]}  # NOTE: real getLedgerEntries key
')"
# Using a simpler approach: query getContractInfo via getLedgerEntries
# would need an ScAddress-encoded key. For the capture script, we
# accept a pre-supplied WASM_HASH env var so the script stays simple.
WASM_HASH="${WASM_HASH:-unknown-wasm-hash}"
if [[ "$WASM_HASH" == "unknown-wasm-hash" ]]; then
  echo "note: WASM_HASH env var not set — fixtures will land under /unknown-wasm-hash/." >&2
  echo "      to pin: WASM_HASH=<hex> scripts/dev/capture-reflector-fixtures.sh …" >&2
fi
OUT_DIR="$FIXTURES_ROOT/$WASM_HASH"
mkdir -p "$OUT_DIR"

# Fetch events matching REFLECTOR/update topic, scoped to the
# contract so we don't pull the entire network's event stream.
# Third topic slot is a wildcard — real events carry
# topic[2] = u64 timestamp (ms), and stellar-rpc's length-aware
# filter drops events whose topic array is longer than the filter
# array unless the tail slot is "*" (WildCardExactOne per
# go-stellar-sdk/protocols/rpc/get_events.go:21).
params="$("$JQ" -nc \
  --arg c "$CONTRACT_ID" \
  --arg t0 "$TOPIC0" \
  --arg t1 "$TOPIC1" \
  --argjson start "$START_LEDGER" \
  --argjson limit "$MAX_EVENTS" '{
    startLedger: $start,
    filters: [{
      type: "contract",
      contractIds: [$c],
      topics: [[$t0, $t1, "*"]]
    }],
    pagination: {limit: $limit}
  }')"

resp="$(rpc_call getEvents "$params")"

err="$(echo "$resp" | "$JQ" -r '.error // empty')"
[[ -n "$err" ]] && { echo "getEvents error: $err" >&2; exit 1; }

count="$(echo "$resp" | "$JQ" '.result.events | length')"
echo "captured $count events for $CONTRACT_ID from ledger $START_LEDGER"
[[ "$count" == "0" ]] && exit 0

echo "$resp" | "$JQ" -c '.result.events[]' | while read -r evt; do
  ledger="$(echo "$evt" | "$JQ" -r '.ledger')"
  tx="$(echo "$evt" | "$JQ" -r '.txHash')"
  closed="$(echo "$evt" | "$JQ" -r '.ledgerClosedAt')"
  topics="$(echo "$evt" | "$JQ" -c '.topic // .topics')"
  # stellar-rpc exposes `value` as a string (base64 ScVal) in the
  # current API shape. Older protocol versions wrapped it as
  # {xdr: "..."} — tolerate both for capture-script portability.
  value="$(echo "$evt" | "$JQ" -r 'if (.value|type) == "object" then .value.xdr else .value end')"

  out="$OUT_DIR/${ledger}_${tx:0:12}.json"
  "$JQ" -n \
    --arg c "$CONTRACT_ID" \
    --arg w "$WASM_HASH" \
    --argjson l "$ledger" \
    --arg t "$tx" \
    --arg ts "$closed" \
    --argjson tp "$topics" \
    --arg v "$value" \
    '{
      contract_id: $c,
      wasm_hash: $w,
      ledger: $l,
      tx_hash: $t,
      ledger_closed_at: $ts,
      topics: $tp,
      value: $v
    }' > "$out"
  echo "  wrote $out"
done

echo "done → $OUT_DIR"
