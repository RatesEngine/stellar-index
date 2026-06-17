#!/usr/bin/env bash
# Capture real Phoenix swap events from a live stellar-rpc, group
# the 8 per-swap field events by (ledger, tx_hash, op_index), and
# write one fixture JSON per complete swap to
# test/fixtures/phoenix/<wasm_hash>/swap_*.json.
#
# Incomplete groups (fewer than 8 events) are dropped — they'd fail
# the RawSwap.Complete() check at runtime anyway.
#
# Usage:
#   scripts/dev/capture-phoenix-fixtures.sh \
#     [-e http://127.0.0.1:8000] \
#     [-n 40] \
#     [-s <start_ledger>]
#
# Env:
#   WASM_HASH  Directory label for captured fixtures.
#
# See docs/architecture/contract-schema-evolution.md for why fixtures
# are per-WASM-hash.

set -euo pipefail

ENDPOINT="${ENDPOINT:-http://127.0.0.1:8000}"
MAX_EVENTS="${MAX_EVENTS:-40}" # 8 per swap × 5 swaps headroom
START_LEDGER=""
JQ="${JQ:-jq}"
CURL="${CURL:-curl}"
WASM_HASH="${WASM_HASH:-unknown-wasm-hash}"

# Known Phoenix pool contracts. The capture
# script scopes by these contractIds to avoid pulling unrelated
# events. Extend as new pools are deployed.
PHOENIX_POOLS=(
  "CBHCRSVX3ZZ7EGTSYMKPEFGZNWRVCSESQR3UABET4MIW52N4EVU6BIZX"  # XLM/USDC
  "CD5XNKK3B6BEF2N7ULNHHGAMOKZ7P6456BFNIHRF4WNTEDKBRWAE7IAA"  # PHO/USDC
  "CBCZGGNOEUZG4CAAE7TGTQQHETZMKUT4OIPFHHPKEUX46U4KXBBZ3GLH"  # XLM/PHO
  "CBISULYO5ZGS32WTNCBMEFCNKNSLFXCQ4Z3XHVDP4X4FLPSEALGSY3PS"  # XLM/EURC
  "CDQLKNH3725BUP4HPKQKMM7OO62FDVXVTO7RCYPID527MZHJG2F3QBJW"  # USDC/VEUR
)

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
command -v python3 >/dev/null || { echo "python3 not found" >&2; exit 127; }

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
OUT_DIR="$REPO_ROOT/test/fixtures/phoenix/$WASM_HASH"
mkdir -p "$OUT_DIR"

# String("swap") wire bytes — regenerate with:
#   go run scripts/dev/encode-topics -type string swap
TOPIC_SWAP='AAAADgAAAARzd2Fw'

rpc() {
  "$CURL" -sS -X POST "$ENDPOINT" \
    -H 'Content-Type: application/json' \
    -d "{\"jsonrpc\":\"2.0\",\"id\":1,\"method\":\"$1\",\"params\":$2}"
}

if [[ -z "$START_LEDGER" ]]; then
  latest="$(rpc getLatestLedger '{}' | "$JQ" -r '.result.sequence')"
  [[ "$latest" == "null" || -z "$latest" ]] && { echo "getLatestLedger failed" >&2; exit 1; }
  START_LEDGER=$((latest - 500))
  echo "latest ledger: $latest → starting from $START_LEDGER"
fi

# Build contract-ids array + topic filter. Pinning topic[0] =
# String("swap") + wildcard at position 1 catches all 8
# per-field events in a single server-side pass.
pools_json="$("$JQ" -nc --args '$ARGS.positional' "${PHOENIX_POOLS[@]}")"
params="$("$JQ" -nc \
  --argjson start "$START_LEDGER" \
  --argjson limit "$MAX_EVENTS" \
  --arg t0 "$TOPIC_SWAP" \
  --argjson pools "$pools_json" '
    { startLedger: $start,
      filters: [{ type:"contract", contractIds:$pools, topics:[[$t0, "*"]] }],
      pagination: {limit: $limit} }')"

resp="$(rpc getEvents "$params")"
err="$(echo "$resp" | "$JQ" -r '.error // empty')"
if [[ -n "$err" ]]; then echo "getEvents error: $err" >&2; exit 1; fi

count="$(echo "$resp" | "$JQ" '.result.events | length')"
echo "captured $count field events"
[[ "$count" == "0" ]] && exit 0

# Group by (ledger, tx_hash, op_index) and emit one fixture file
# per complete 8-event group. python3 is the path of least
# resistance here — pure jq grouping of 8 events per key with
# payload preservation is fiddly.
python3 - "$resp" "$OUT_DIR" "$WASM_HASH" <<'PY'
import json, sys, os
from collections import defaultdict
resp = json.loads(sys.argv[1])
out_dir = sys.argv[2]
wasm_hash = sys.argv[3]

groups = defaultdict(list)
for e in resp["result"]["events"]:
    groups[(e["ledger"], e["txHash"], e["operationIndex"])].append(e)

saved = 0
skipped_incomplete = 0
for (ledger, tx, op), ee in groups.items():
    if len(ee) != 8:
        skipped_incomplete += 1
        continue
    fx = {
      "wasm_hash": wasm_hash,
      "ledger": ledger,
      "tx_hash": tx,
      "op_index": op,
      "contract_id": ee[0]["contractId"],
      "ledger_closed_at": ee[0]["ledgerClosedAt"],
      "events": [{"topics": e["topic"], "value": e["value"]} for e in ee]
    }
    fname = os.path.join(out_dir, f"swap_{ledger}_{tx[:12]}_op{op}.json")
    with open(fname, "w") as f: json.dump(fx, f, indent=2)
    print(f"  wrote {fname}")
    saved += 1
print(f"saved {saved} complete 8-event swap fixtures ({skipped_incomplete} incomplete groups dropped)")
PY

echo "done → $OUT_DIR"
