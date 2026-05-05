#!/usr/bin/env bash
# Install per-binary Healthchecks.io heartbeats on R1.
#
# Idempotent — re-running re-syncs the script + units. The
# /etc/default/ratesengine-healthchecks env file is created with
# placeholder values on first run (operator fills in the URLs);
# subsequent runs preserve it.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"

INSTALL_DIR="/opt/ratesengine/healthchecks"
SYSTEMD_DIR="/etc/systemd/system"
ENV_FILE="/etc/default/ratesengine-healthchecks"

mkdir -p "$INSTALL_DIR"
install -m 0755 "$SCRIPT_DIR/heartbeat.sh" "$INSTALL_DIR/heartbeat.sh"
install -m 0644 "$SCRIPT_DIR/ratesengine-heartbeat@.service" "$SYSTEMD_DIR/"
install -m 0644 "$SCRIPT_DIR/ratesengine-heartbeat@.timer" "$SYSTEMD_DIR/"

# Provision the env file with placeholders if missing. Operator
# pastes the three Healthchecks.io check URLs they create on the
# dashboard, then runs `systemctl restart ratesengine-heartbeat@*.timer`.
if [ ! -f "$ENV_FILE" ]; then
  cat > "$ENV_FILE" <<'EOF'
# Per-binary Healthchecks.io heartbeat URLs.
#
# Each is a separate "Check" on healthchecks.io. Empty URL silently
# skips the ping (the metrics-endpoint probe still runs and logs
# failures via journalctl, so the timer is useful even before the
# URLs are wired).
#
# Suggested cadence on the dashboard side: schedule 60 s, grace 120 s.
HEALTHCHECKS_URL_INDEXER=
HEALTHCHECKS_URL_AGGREGATOR=
HEALTHCHECKS_URL_API=
EOF
  chmod 0600 "$ENV_FILE"
  chown root:root "$ENV_FILE"
  echo "install: created placeholder $ENV_FILE — operator to populate"
fi

systemctl daemon-reload
systemctl enable --now ratesengine-heartbeat@indexer.timer
systemctl enable --now ratesengine-heartbeat@aggregator.timer
systemctl enable --now ratesengine-heartbeat@api.timer

echo "install: done"
echo
echo "Next: populate $ENV_FILE with real URLs from healthchecks.io,"
echo "then 'systemctl restart ratesengine-heartbeat@*.timer'"
