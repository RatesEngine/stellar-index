#!/usr/bin/env bash
# Regenerate the Postman collection from openapi/stellar-index.v1.yaml.
#
# Writes to examples/postman/stellar-index.postman_collection.json
# — the user-facing canonical path (referenced by README.md, and the
# file users actually download from the repo).
#
# An earlier revision wrote to docs/reference/api/postman-collection.json
# (a gitignored docs-site path) and left the tracked user-facing copy
# drifting silently. Now the script writes the canonical directly.
#
# Uses openapi-to-postmanv2 via npx so contributors don't need a
# global install (only Node is required). Pinned version so the
# generated output stays reproducible — bumping requires updating
# CONVERTER_VERSION below + re-running this script + committing
# the diff.

set -euo pipefail

CONVERTER_VERSION="6.0.1"

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$REPO_ROOT"

if ! command -v npx >/dev/null 2>&1; then
  echo "npx not found — install Node (https://nodejs.org/) to regenerate the Postman collection."
  echo "Source of truth: openapi/stellar-index.v1.yaml"
  exit 1
fi

mkdir -p examples/postman

# openapi-to-postmanv2 prints a sub-summary on success; pipe to
# /dev/null and rely on the exit code so this is silent on success.
TMP=$(mktemp)
npx --yes "openapi-to-postmanv2@${CONVERTER_VERSION}" \
    -s openapi/stellar-index.v1.yaml \
    -o "$TMP" \
    -p \
    >/dev/null

# openapi-to-postmanv2 stamps a fresh UUIDv4 into every "id" field
# on every run. Postman doesn't need them — it regenerates IDs on
# import — and the noise makes the file unmergeable. Strip them
# so the diff is meaningful (only changes when the openapi spec
# itself changes).
if ! command -v jq >/dev/null 2>&1; then
  echo "jq not found — install jq (https://stedolan.github.io/jq/) for ID-stripping"
  exit 1
fi

CANONICAL="examples/postman/stellar-index.postman_collection.json"

jq 'walk(if type == "object" and has("id") then del(.id) else . end)' "$TMP" \
  > "$CANONICAL"
rm -f "$TMP"

echo "Generated $CANONICAL"
echo "  $(wc -c < "$CANONICAL" | tr -d ' ') bytes"
