#!/bin/bash
#
# node-healthcheck.sh — periodic health probe for archival-node
# services + network state. Pings a Healthchecks.io-compatible URL
# on success and (with diagnostic body) on failure.
#
# Checks (all must pass for SUCCESS):
#   1. All 7 systemd services are `active`
#   2. stellar-core's /info reports state == "Synced!"
#   3. stellar-core's last-closed-ledger age < threshold
#      (proves we're tracking network head, not stuck on an old one)
#   4. stellar-rpc responds to getHealth JSON-RPC
#   5. ZFS data pool state == ONLINE
#   6. /var/lib/stellar-core has ≥ 10% free capacity
#
# Ping URL lives in /etc/default/node-healthcheck as
# HEALTHCHECK_PING_URL. That file is rendered by Ansible from a
# vault-encrypted variable so the URL (which is effectively a
# shared secret — anyone with it can forge 'healthy' pings) stays
# off-disk in cleartext form in git.
#
# Exit 0 always — we don't want systemd to mark the timer failed;
# the script itself reports failures to healthchecks.io.

set -uo pipefail

# --- Load config ------------------------------------------------
if [ -f /etc/default/node-healthcheck ]; then
  # shellcheck disable=SC1091
  . /etc/default/node-healthcheck
fi

PING_URL="${HEALTHCHECK_PING_URL:-}"
MAX_LEDGER_AGE_SEC="${MAX_LEDGER_AGE_SEC:-90}"
POOL_NAME="${POOL_NAME:-data}"

# If no ping URL is configured, exit 0 silently — lets the unit
# install be idempotent before the secret has been populated.
if [ -z "$PING_URL" ]; then
  echo "node-healthcheck: HEALTHCHECK_PING_URL unset — skipping" >&2
  exit 0
fi

# --- Accumulator -----------------------------------------------
FAILS=()
add_fail() { FAILS+=("$1"); }

# --- Check 1: systemd service liveness -------------------------
SERVICES=(
  postgresql@15-main
  stellar-core
  stellar-rpc
  galexie
  minio
  node_exporter
  stellar-core-prometheus-exporter
)
for s in "${SERVICES[@]}"; do
  state=$(systemctl is-active "$s" 2>&1)
  if [ "$state" != "active" ]; then
    add_fail "service $s is $state (expected active)"
  fi
done

# --- Check 2 + 3: stellar-core sync state + ledger age ---------
if info=$(curl -sfm 5 http://127.0.0.1:11626/info 2>&1); then
  core_state=$(echo "$info" | jq -r '.info.state // ""')
  close_time=$(echo "$info" | jq -r '.info.ledger.closeTime // 0')
  if [ "$core_state" != "Synced!" ]; then
    add_fail "stellar-core state=$core_state (expected Synced!)"
  fi
  now_epoch=$(date +%s)
  age=$(( now_epoch - close_time ))
  if [ "$age" -gt "$MAX_LEDGER_AGE_SEC" ]; then
    add_fail "stellar-core last ledger is ${age}s old (threshold ${MAX_LEDGER_AGE_SEC}s)"
  fi
else
  add_fail "stellar-core /info unreachable on :11626"
fi

# --- Check 4: stellar-rpc is reachable + reasonably fresh ------
# stellar-rpc has two "not broken, just catching up" states we
# must treat as OK during a grace window:
#
#   A) DB empty → error.message contains "data stores are not initialized"
#      Seen on first-ever start (fresh SQLite).
#   B) Latency too high → error.message contains "latency (...) since last
#      known ledger closed is too high"
#      Seen for 3-7 minutes after EVERY captive-core restart, because
#      stellar-core has to download the next checkpoint HAS file from
#      the SDF archives before it can tail the live network.
#
# Both states are bounded by STELLAR_RPC_WARMUP_SEC (default 2 h)
# measured from the service's ActiveEnterTimestamp. After that,
# any non-healthy response is a real failure — captive-core is
# likely stuck on a config issue.
#
# Distinguishes:
#   * transport error (empty/non-JSON/refused) → FAIL (always)
#   * status=healthy → OK (always)
#   * catchup error (A or B) + within grace → OK
#   * catchup error (A or B) + grace exceeded → FAIL
#   * anything else → FAIL
rpc_resp=$(curl -sfm 5 -X POST -H 'Content-Type: application/json' \
  -d '{"jsonrpc":"2.0","id":1,"method":"getHealth"}' \
  http://127.0.0.1:8000 2>&1) || rpc_resp=""
rpc_status=$(echo "$rpc_resp" | jq -r '.result.status // ""' 2>/dev/null)
rpc_error=$(echo "$rpc_resp" | jq -r '.error.message // ""' 2>/dev/null)

# True if the error is a benign "still catching up" state.
is_catchup_err=0
if echo "$rpc_error" | grep -qE 'data stores are not initialized|latency \([^)]+\) since last known ledger closed'; then
  is_catchup_err=1
fi

if [ "$rpc_status" = "healthy" ]; then
  : # fully healthy, no action
elif [ "$is_catchup_err" = 1 ]; then
  WARMUP_MAX="${STELLAR_RPC_WARMUP_SEC:-7200}"
  enter_iso=$(systemctl show -p ActiveEnterTimestamp --value stellar-rpc 2>/dev/null)
  enter_epoch=$(date -d "$enter_iso" +%s 2>/dev/null || echo 0)
  now_epoch=$(date +%s)
  age=$(( now_epoch - enter_epoch ))
  if [ "$age" -gt "$WARMUP_MAX" ]; then
    add_fail "stellar-rpc still catching up after ${age}s (warmup cap ${WARMUP_MAX}s): ${rpc_error}"
  fi
elif [ -z "$rpc_resp" ] || ! echo "$rpc_resp" | jq -e . >/dev/null 2>&1; then
  add_fail "stellar-rpc unreachable or non-JSON response"
else
  add_fail "stellar-rpc unhealthy: status='$rpc_status' error='$rpc_error'"
fi

# --- Check 5: ZFS pool state -----------------------------------
pool_state=$(zpool list -H -o health "$POOL_NAME" 2>&1 || echo "MISSING")
if [ "$pool_state" != "ONLINE" ]; then
  add_fail "zpool $POOL_NAME state=$pool_state (expected ONLINE)"
fi

# --- Check 6: disk free on stellar-core data dir ---------------
disk_pct_used=$(df --output=pcent /var/lib/stellar-core | tail -1 | tr -d ' %')
if [ "${disk_pct_used:-0}" -gt 90 ]; then
  add_fail "/var/lib/stellar-core is ${disk_pct_used}% full"
fi

# --- Report ----------------------------------------------------
if [ ${#FAILS[@]} -eq 0 ]; then
  curl -sfm 10 -o /dev/null "$PING_URL" || true
  exit 0
fi

body="$(printf '%s\n' "${FAILS[@]}")"
echo "node-healthcheck: ${#FAILS[@]} failure(s) — reporting to healthchecks.io" >&2
printf '%s\n' "$body" >&2
curl -sfm 10 -o /dev/null --data-raw "$body" "$PING_URL/fail" || true
exit 0
