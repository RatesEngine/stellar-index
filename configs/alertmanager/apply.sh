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
#   DISCORD_WEBHOOK_URL_PAGES       — e.g. https://discord.com/api/webhooks/<id>/<token>
#   DISCORD_WEBHOOK_URL_ALERTS      — e.g. https://discord.com/api/webhooks/<id>/<token>
#
# Any may be empty — the corresponding receiver's *_configs block is
# dropped, so the receiver becomes a no-op stub (alerts accumulate in
# the Alertmanager UI but don't fan out). Point both Discord URLs at
# the same webhook if you only want one channel.

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
DISCORD_PAGES_URL="${DISCORD_WEBHOOK_URL_PAGES:-}" \
DISCORD_ALERTS_URL="${DISCORD_WEBHOOK_URL_ALERTS:-}" \
SOURCE="$SOURCE" \
RENDERED="$RENDERED" \
python3 - <<'PY'
import os

src = open(os.environ["SOURCE"]).read()

subs = {
    "${HEALTHCHECKS_DEADMANSSWITCH_URL}": os.environ.get("HEALTHCHECKS_URL", "").strip(),
    "${DISCORD_WEBHOOK_URL_PAGES}":       os.environ.get("DISCORD_PAGES_URL", "").strip(),
    "${DISCORD_WEBHOOK_URL_ALERTS}":      os.environ.get("DISCORD_ALERTS_URL", "").strip(),
}


def strip_configs_block_for_marker(text, marker):
    """Remove the `*_configs:` block that contains `marker`, leaving
    the bare `- name: <receiver>` as a valid no-op stub. Line-based +
    marker-specific so one empty URL never collateral-strips another
    receiver's identically-keyed block (two discord_configs blocks)."""
    lines = text.split("\n")
    idx = next((i for i, l in enumerate(lines) if marker in l), None)
    if idx is None:
        return text
    # Walk up to the `*_configs:` header line for this marker.
    hdr = idx
    while hdr >= 0 and not lines[hdr].strip().endswith("_configs:"):
        hdr -= 1
    if hdr < 0:
        return text
    header_indent = len(lines[hdr]) - len(lines[hdr].lstrip())
    # The block body is every following line indented deeper than the
    # header (blank lines included); stop at the next line indented at
    # or above the header (the next receiver / top-level key).
    end = hdr + 1
    while end < len(lines):
        stripped = lines[end].strip()
        if stripped == "":
            end += 1
            continue
        indent = len(lines[end]) - len(lines[end].lstrip())
        if indent > header_indent:
            end += 1
        else:
            break
    del lines[hdr:end]
    return "\n".join(lines)


for marker, url in subs.items():
    if url:
        src = src.replace(marker, url)
    else:
        src = strip_configs_block_for_marker(src, marker)

open(os.environ["RENDERED"], "w").write(src)
PY

if ! amtool check-config "$RENDERED"; then
  echo "error: alertmanager config failed validation" >&2
  exit 1
fi

# CS-121: 0640 (not 0644) so the rendered config — which embeds the Discord
# webhook URLs + the Healthchecks deadman URL (bearer capabilities) — is not
# world-readable. Group is the alertmanager service user's group (prometheus on
# r1) so the service can still read it; override via AM_GROUP if it differs.
install -m 0640 -o root -g "${AM_GROUP:-prometheus}" "$RENDERED" "$TARGET"
systemctl reload prometheus-alertmanager
echo "alertmanager: applied $TARGET, reload OK"
