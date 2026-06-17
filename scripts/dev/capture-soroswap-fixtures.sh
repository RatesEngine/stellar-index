#!/usr/bin/env bash
# Capture real Soroswap pair events (swap + sync) + factory new_pair
# events from a live stellar-rpc. Writes one fixture JSON per event
# under test/fixtures/soroswap/<wasm_hash>/{swap,sync,new_pair}_*.json.
#
# Consumed by internal/sources/soroswap/real_fixture_test.go, which
# pairs swap + sync by (ledger, tx_hash, contract) before decoding.
#
# Usage:
#   scripts/dev/capture-soroswap-fixtures.sh \
#     [-e http://127.0.0.1:8000] \
#     [-n 5] \
#     [-s <start_ledger>]
#
# Flags:
#   -e  stellar-rpc endpoint (default: http://127.0.0.1:8000).
#   -n  Max events per topic (default: 5).
#   -s  Start ledger (default: latestLedger - 200).
#
# Environment:
#   WASM_HASH  Directory label for captured fixtures (default:
#              "unknown-wasm-hash"). Use a tag + date label like
#              `v1-2026-04-23` until the ops CLI can resolve true
#              WASM hashes from the contract's instance entry.
#
# Rationale for per-WASM-hash fixture layout: see
# docs/architecture/contract-schema-evolution.md — a contract
# `update_contract` swap can change body field names / arity, and
# we need decoders pinned to specific WASM versions so backfill
# against old ledgers stays correct.

set -euo pipefail

ENDPOINT="${ENDPOINT:-http://127.0.0.1:8000}"
MAX_EVENTS="${MAX_EVENTS:-5}"
START_LEDGER=""
JQ="${JQ:-jq}"
CURL="${CURL:-curl}"
WASM_HASH="${WASM_HASH:-unknown-wasm-hash}"

# Factory contract ID (constant per docs/protocols/soroswap.md).
FACTORY_ID="CA4HEQTL2WPEUYKYKCDOHCDNIV4QHNJ7EL4J4NQ6VADP7SYHVRYZ7AW2"

while getopts "e:n:s:h" opt; do
  case "$opt" in
    e) ENDPOINT="$OPTARG" ;;
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
OUT_DIR="$REPO_ROOT/test/fixtures/soroswap/$WASM_HASH"
mkdir -p "$OUT_DIR"

# Pre-encoded topic blobs — must match what the package computes
# at init. Regenerate via:
#   go run scripts/dev/encode-topics -type string  SoroswapPair SoroswapFactory
#   go run scripts/dev/encode-topics -type symbol  swap sync new_pair
TOPIC_PREFIX_PAIR='AAAADgAAAAxTb3Jvc3dhcFBhaXI='
TOPIC_PREFIX_FACT='AAAADgAAAA9Tb3Jvc3dhcEZhY3RvcnkA'
TOPIC_SWAP='AAAADwAAAARzd2Fw'
TOPIC_SYNC='AAAADwAAAARzeW5j'
TOPIC_NEW_PAIR='AAAADwAAAAhuZXdfcGFpcg=='

rpc() {
  "$CURL" -sS -X POST "$ENDPOINT" \
    -H 'Content-Type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$1\",\"params\":$2}"
}

if [[ -z "$START_LEDGER" ]]; then
  latest="$(rpc getLatestLedger '{}' | "$JQ" -r '.result.sequence')"
  [[ "$latest" == "null" || -z "$latest" ]] && { echo "getLatestLedger failed" >&2; exit 1; }
  START_LEDGER=$((latest - 200))
  echo "latest ledger: $latest → starting from $START_LEDGER"
fi

capture_topic() {
  local event_name="$1"
  local topic1="$2"
  local prefix_topic="$3"
  local contract_clause="$4"

  local params
  params="$("$JQ" -nc \
    --argjson start "$START_LEDGER" \
    --argjson limit "$MAX_EVENTS" \
    --arg t0 "$prefix_topic" \
    --arg t1 "$topic1" \
    --argjson cc "$contract_clause" '
      { startLedger: $start,
        filters: [(
          {type:"contract", topics:[[$t0, $t1]]}
          + $cc
        )],
        pagination: {limit: $limit}
      }')"

  local resp
  resp="$(rpc getEvents "$params")"
  local err
  err="$(echo "$resp" | "$JQ" -r '.error // empty')"
  if [[ -n "$err" ]]; then
    echo "$event_name: getEvents error: $err" >&2
    return 1
  fi
  local count
  count="$(echo "$resp" | "$JQ" '.result.events | length')"
  echo "  $event_name: $count event(s)"
  [[ "$count" == "0" ]] && return 0

  echo "$resp" | "$JQ" -c '.result.events[]' | while read -r evt; do
    local ledger tx closed topics value fname
    ledger="$(echo "$evt" | "$JQ" -r '.ledger')"
    tx="$(echo "$evt" | "$JQ" -r '.txHash')"
    closed="$(echo "$evt" | "$JQ" -r '.ledgerClosedAt')"
    topics="$(echo "$evt" | "$JQ" -c '.topic // .topics')"
    value="$(echo "$evt" | "$JQ" -r 'if (.value|type) == "object" then .value.xdr else .value end')"
    fname="$OUT_DIR/${event_name}_${ledger}_${tx:0:12}.json"
    contract="$(echo "$evt" | "$JQ" -r '.contractId')"
    "$JQ" -n \
      --arg c "$contract" \
      --arg w "$WASM_HASH" \
      --argjson l "$ledger" \
      --arg t "$tx" \
      --arg ts "$closed" \
      --argjson tp "$topics" \
      --arg v "$value" \
      --arg e "$event_name" \
      '{
        contract_id: $c, wasm_hash: $w, ledger: $l, tx_hash: $t,
        ledger_closed_at: $ts, topics: $tp, value: $v, event_name: $e
      }' > "$fname"
    echo "    wrote $fname"
  done
}

# Pair events: any pair contract (no ContractIDs clause).
capture_topic "swap" "$TOPIC_SWAP" "$TOPIC_PREFIX_PAIR" '{}'
capture_topic "sync" "$TOPIC_SYNC" "$TOPIC_PREFIX_PAIR" '{}'

# Factory event: scoped to the factory contract ID.
factory_clause="$("$JQ" -nc --arg c "$FACTORY_ID" '{contractIds:[$c]}')"
capture_topic "new_pair" "$TOPIC_NEW_PAIR" "$TOPIC_PREFIX_FACT" "$factory_clause"

echo "done → $OUT_DIR"
