#!/usr/bin/env bash
# Apply alertmanager.r1.yml to R1.
#
# Reads webhook URLs from /etc/default/alertmanager-secrets, injects
# them into the config (using a Python YAML walker so empty URLs
# leave the receiver as a no-op stub instead of breaking the file),
# validates with amtool, atomic-installs to
# /etc/prometheus/alertmanager.yml, then reloads alertmanager.
#
# Required env vars (in /etc/default/alertmanager-secrets):
#   HEALTHCHECKS_DEADMANSSWITCH_URL — e.g. https://hc-ping.com/<uuid>
#   SLACK_WEBHOOK_URL              — e.g. https://hooks.slack.com/services/T.../...
#
# Either may be empty — the corresponding receiver becomes a no-op
# stub (alerts accumulate in the Alertmanager UI but don't fan out).

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" &>/dev/null && pwd)"
SOURCE="${SCRIPT_DIR}/alertmanager.r1.yml"
ENV_FILE="${ALERTMANAGER_SECRETS:-/etc/default/alertmanager-secrets}"
TARGET="${TARGET:-/etc/prometheus/alertmanager.yml}"

if [ ! -f "$SOURCE" ]; then
  echo "error: source config not found: $SOURCE" >&2
  exit 1
fi

# shellcheck disable=SC1090
[ -f "$ENV_FILE" ] && . "$ENV_FILE"

RENDERED="$(mktemp)"
trap 'rm -f "$RENDERED"' EXIT

HEALTHCHECKS_URL="${HEALTHCHECKS_DEADMANSSWITCH_URL:-}" \
SLACK_URL="${SLACK_WEBHOOK_URL:-}" \
SOURCE="$SOURCE" \
RENDERED="$RENDERED" \
python3 - <<'PY'
import os
import re
import sys

src = open(os.environ["SOURCE"]).read()

hc = os.environ.get("HEALTHCHECKS_URL", "").strip()
slack = os.environ.get("SLACK_URL", "").strip()

# Substitute placeholders. When a URL is empty, drop the entire
# parent block (webhook_configs / slack_configs) so amtool doesn't
# reject the empty value.

def strip_block(text, marker, parent_key):
    """Remove a contiguous indented block under parent_key when its
    placeholder marker is empty."""
    pattern = rf"\s+{re.escape(parent_key)}:\n(?:^[ \t]+.*\n?)+"
    # Only strip when the marker is present (i.e., we know we
    # haven't already replaced it with a real URL).
    new = re.sub(pattern, "\n", text, flags=re.MULTILINE)
    return new

if hc:
    src = src.replace("${HEALTHCHECKS_DEADMANSSWITCH_URL}", hc)
else:
    src = strip_block(src, "${HEALTHCHECKS_DEADMANSSWITCH_URL}", "webhook_configs")

if slack:
    src = src.replace("${SLACK_WEBHOOK_URL}", slack)
else:
    src = strip_block(src, "${SLACK_WEBHOOK_URL}", "slack_configs")

open(os.environ["RENDERED"], "w").write(src)
PY

if ! amtool check-config "$RENDERED"; then
  echo "error: alertmanager config failed validation" >&2
  exit 1
fi

install -m 0644 -o root -g root "$RENDERED" "$TARGET"
systemctl reload prometheus-alertmanager
echo "alertmanager: applied $TARGET, reload OK"
