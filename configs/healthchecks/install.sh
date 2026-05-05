#!/usr/bin/env bash
# Install per-binary Healthchecks.io heartbeats on R1.
#
# Idempotent — re-running re-syncs the script + units. The
# /etc/default/ratesengine-healthchecks env file is created with
# placeholder values on first run (operator fills in the URLs);
# subsequent runs preserve it.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
REPO_ROOT="$(cd -- "$SCRIPT_DIR/../.." &>/dev/null && pwd)"

INSTALL_DIR="/opt/ratesengine/healthchecks"
SYSTEMD_DIR="/etc/systemd/system"
ENV_FILE="/etc/default/ratesengine-healthchecks"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$SCRIPT_DIR/heartbeat.sh" "$INSTALL_DIR/heartbeat.sh"
install -m 0755 "$SCRIPT_DIR/smoke.sh" "$INSTALL_DIR/smoke.sh"
install -m 0755 "$SCRIPT_DIR/sla-probe.sh" "$INSTALL_DIR/sla-probe.sh"
# Smoke wrapper runs r1-smoke.sh — copy it alongside so the unit
# doesn't depend on a checkout being present on R1.
install -m 0755 "$REPO_ROOT/scripts/dev/r1-smoke.sh" "$INSTALL_DIR/r1-smoke.sh"
install -m 0644 "$SCRIPT_DIR/ratesengine-heartbeat@.service" "$SYSTEMD_DIR/"
install -m 0644 "$SCRIPT_DIR/ratesengine-heartbeat@.timer" "$SYSTEMD_DIR/"
install -m 0644 "$SCRIPT_DIR/ratesengine-smoke.service" "$SYSTEMD_DIR/"
install -m 0644 "$SCRIPT_DIR/ratesengine-smoke.timer" "$SYSTEMD_DIR/"
install -m 0644 "$SCRIPT_DIR/ratesengine-sla-probe.service" "$SYSTEMD_DIR/"
install -m 0644 "$SCRIPT_DIR/ratesengine-sla-probe.timer" "$SYSTEMD_DIR/"

# Provision the env file with placeholders if missing. Operator
# pastes the four Healthchecks.io URLs (3 heartbeats + 1 smoke)
# they create on the dashboard, then runs
# `systemctl restart ratesengine-heartbeat@*.timer ratesengine-smoke.timer`.
if [ ! -f "$ENV_FILE" ]; then
  cat > "$ENV_FILE" <<'EOF'
# Healthchecks.io URLs.
#
# Each is a separate "Check" on healthchecks.io. Empty URL silently
# skips the ping (the underlying probe still runs and logs failures
# via journalctl, so the timer is useful even before URLs are wired).
#
# Per-binary heartbeats (60 s cadence, suggested grace 120 s):
HEALTHCHECKS_URL_INDEXER=
HEALTHCHECKS_URL_AGGREGATOR=
HEALTHCHECKS_URL_API=
# API surface smoke test (5 min cadence, suggested grace 10 min):
HEALTHCHECKS_URL_SMOKE=
# SLA probe (RFP latency + freshness; 15 min cadence, grace 30 min):
HEALTHCHECKS_URL_SLA_PROBE=
# SLA probe tuning (defaults match the binary's flag defaults).
# SLA_PROBE_BASE_URL=http://localhost:3000/v1
# SLA_PROBE_DURATION=30s
# SLA_PROBE_CONCURRENCY=2
# SLA_PROBE_PAIR=native,fiat:USD
EOF
  chmod 0600 "$ENV_FILE"
  chown root:root "$ENV_FILE"
  echo "install: created placeholder $ENV_FILE — operator to populate"
fi

systemctl daemon-reload
systemctl enable --now ratesengine-heartbeat@indexer.timer
systemctl enable --now ratesengine-heartbeat@aggregator.timer
systemctl enable --now ratesengine-heartbeat@api.timer
systemctl enable --now ratesengine-smoke.timer
systemctl enable --now ratesengine-sla-probe.timer

echo "install: done"
echo
echo "Next: populate $ENV_FILE with real URLs from healthchecks.io,"
echo "then 'systemctl restart ratesengine-heartbeat@*.timer ratesengine-smoke.timer ratesengine-sla-probe.timer'"
