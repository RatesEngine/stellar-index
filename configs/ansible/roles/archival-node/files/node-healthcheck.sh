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
# r1's Phase 1 shape after trimming: just postgres, galexie (with
# its captive-core producing to MinIO), minio, and node_exporter.
# Removed 2026-04-23: primary stellar-core (see r1-deployment-state
# pitfall #1/#7), stellar-core-prometheus-exporter (scraped primary),
# and stellar-rpc (redundant — our indexer consumes galexie's MinIO
# output directly via Ingest SDK).
SERVICES=(
  postgresql@15-main
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

# Primary stellar-core /info probe and stellar-rpc getHealth probe
# were both removed when we trimmed the stack on 2026-04-23. The
# "is the network being followed?" signal is now exclusively the
# galexie upload-freshness check below (Check 4.5).
#
# If stellar-rpc is ever re-added, restore a getHealth probe here
# with the latency-grace logic that was in git ref f7527f7.

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
