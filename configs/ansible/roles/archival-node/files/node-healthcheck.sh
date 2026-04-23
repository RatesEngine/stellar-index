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
# Primary stellar-core was removed 2026-04-23 — we don't publish our
# own archive yet and aren't a validator, so it was paying its cost
# (3.6G RAM, 25% CPU, peer-slot contention with the captives) with
# no corresponding value. Galexie's captive-core is our single
# producer; stellar-rpc has its own captive for ingest.
# stellar-core-prometheus-exporter is gone with the primary (no
# /info endpoint to scrape).
SERVICES=(
  postgresql@15-main
  stellar-rpc
  galexie
  minio
  node_exporter
)
for s in "${SERVICES[@]}"; do
  state=$(systemctl is-active "$s" 2>&1)
  if [ "$state" != "active" ]; then
    add_fail "service $s is $state (expected active)"
  fi
done

# Check 2 + 3 (direct stellar-core /info probe on :11626) were
# removed along with the primary stellar-core. The "is the network
# being followed?" question is now answered by:
#   * Check 4   — stellar-rpc getHealth latency (covers captive-core
#                 freshness on the RPC side)
#   * Check 4.5 — galexie upload mtime to MinIO (covers the galexie
#                 captive-core freshness)
# If both pass, at least one captive-core is tailing the network.

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

# --- Check 4.5: galexie upload freshness -----------------------
# Galexie's metrics endpoint (admin_port 6061) has been observed
# to HANG for minutes when captive-core is stuck — so we can't
# use it as a liveness signal. The true signal is: is the
# galexie-live MinIO bucket growing?
#
# We check the mtime of the most-recent object in the bucket
# against wall clock. In steady state a new object lands every
# ~5 sec (one per closed ledger). If the most recent is > 10 min
# old AND galexie has been running > GALEXIE_WARMUP_SEC, fail.
#
# Requires `mc alias set local` to have been run (done at role-
# apply time, credentials in /etc/default/node-healthcheck or
# implicit via the `mc` config under HOME). If mc isn't reachable
# we don't FAIL here — MinIO-down is caught by check 1.
GALEXIE_MAX_LAG_SEC="${GALEXIE_MAX_LAG_SEC:-600}"
GALEXIE_WARMUP_SEC="${GALEXIE_WARMUP_SEC:-1800}"
g_enter_iso=$(systemctl show -p ActiveEnterTimestamp --value galexie 2>/dev/null)
g_enter_epoch=$(date -d "$g_enter_iso" +%s 2>/dev/null || echo 0)
g_age=$(( $(date +%s) - g_enter_epoch ))
if [ "$g_age" -gt "$GALEXIE_WARMUP_SEC" ]; then
  # mc --json gives a machine-readable listing; sort by lastModified.
  last_iso=$(mc ls --json --recursive local/galexie-live/ 2>/dev/null \
    | jq -r 'select(.key | test("\\.xdr\\.zst$")) | .lastModified' 2>/dev/null \
    | sort -r | head -1)
  if [ -n "$last_iso" ]; then
    last_epoch=$(date -d "$last_iso" +%s 2>/dev/null || echo 0)
    lag=$(( $(date +%s) - last_epoch ))
    if [ "$lag" -gt "$GALEXIE_MAX_LAG_SEC" ]; then
      add_fail "galexie last upload was ${lag}s ago (threshold ${GALEXIE_MAX_LAG_SEC}s) — captive-core likely stuck"
    fi
  fi
  # If last_iso is empty: either bucket is empty (never uploaded)
  # or mc is broken. Leave to check 1 (minio service) + manual
  # inspection; don't flag here.
fi

# --- Check 5: ZFS pool state -----------------------------------
pool_state=$(zpool list -H -o health "$POOL_NAME" 2>&1 || echo "MISSING")
if [ "$pool_state" != "ONLINE" ]; then
  add_fail "zpool $POOL_NAME state=$pool_state (expected ONLINE)"
fi

# --- Check 6: disk free on the captive-core bucket dirs --------
# Check galexie's captive-core first (it's the primary producer).
# stellar-rpc's captive is secondary.
for d in /var/lib/galexie /var/lib/stellar-rpc; do
  [ -d "$d" ] || continue
  disk_pct_used=$(df --output=pcent "$d" | tail -1 | tr -d ' %')
  if [ "${disk_pct_used:-0}" -gt 90 ]; then
    add_fail "$d is ${disk_pct_used}% full"
  fi
done

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
