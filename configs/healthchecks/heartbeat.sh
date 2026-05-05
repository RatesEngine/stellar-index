#!/usr/bin/env bash
# Per-binary Healthchecks.io heartbeat.
#
# Hits the binary's metrics scrape endpoint to confirm it's alive,
# then pings the corresponding Healthchecks.io URL. The service
# name (indexer / aggregator / api) is the systemd template
# instance — passed via %i.
#
# URLs come from /etc/default/ratesengine-healthchecks (off-disk
# in git). Empty URL silently skips the ping — lets the timers
# install before the operator has provisioned the URLs.

set -uo pipefail

SERVICE="${1:-${SERVICE_NAME:-}}"
if [ -z "$SERVICE" ]; then
  echo "usage: $0 <indexer|aggregator|api>" >&2
  exit 64
fi

# Env vars (HEALTHCHECKS_URL_*) come from the systemd unit's
# EnvironmentFile= directive, which loads
# /etc/default/ratesengine-healthchecks as root before dropping
# privileges. Do NOT re-source it here — the unprivileged service
# user can't read the 0600 secret file.

# Port mapping (matches configs/prometheus/prometheus.r1.yml).
case "$SERVICE" in
  indexer)    PORT="${INDEXER_METRICS_PORT:-9464}";;
  aggregator) PORT="${AGGREGATOR_METRICS_PORT:-9465}";;
  api)        PORT="${API_METRICS_PORT:-3000}";;
  *)
    echo "heartbeat: unknown service $SERVICE" >&2
    exit 64
    ;;
esac

# URL var name: HEALTHCHECKS_URL_INDEXER, _AGGREGATOR, _API.
URL_VAR="HEALTHCHECKS_URL_$(echo "$SERVICE" | tr '[:lower:]' '[:upper:]')"
PING_URL="${!URL_VAR:-}"

# Probe the metrics endpoint with a 5 s budget. Curl exit codes
# distinguish "process not listening" (7) from "process up, slow"
# (28). Both indicate degraded — the difference matters for
# operators reading logs but not for the heartbeat decision.
PROBE_RC=0
curl -sSf --max-time 5 -o /dev/null "http://localhost:${PORT}/metrics" || PROBE_RC=$?

if [ "$PROBE_RC" -ne 0 ]; then
  echo "heartbeat: $SERVICE probe FAILED on :${PORT} (rc=$PROBE_RC)" >&2
  if [ -n "$PING_URL" ]; then
    # Healthchecks.io's /fail endpoint records a failure — the
    # check turns red on the dashboard immediately, no waiting
    # for the grace period.
    curl -fsS --max-time 5 -o /dev/null --retry 2 "${PING_URL}/fail" || true
  fi
  exit 0
fi

# Probe succeeded — ping the heartbeat URL. POST body carries a
# short health summary so the dashboard's "last ping" entry is
# useful at a glance.
if [ -n "$PING_URL" ]; then
  curl -fsS --max-time 5 -o /dev/null --retry 2 \
    -d "ratesengine-${SERVICE} ok :${PORT}" \
    "$PING_URL" || true
fi

exit 0
