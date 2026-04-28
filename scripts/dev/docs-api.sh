#!/usr/bin/env bash
# Regenerate the rendered API reference from openapi/rates-engine.v1.yaml.
#
# Uses Redocly CLI via npx so contributors don't need a global install
# (only Node is required). Pinned version so the rendered output stays
# reproducible — bumping requires updating REDOCLY_VERSION below + the
# CI guard.

set -euo pipefail

REDOCLY_VERSION="2.30.1"

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$REPO_ROOT"

if ! command -v npx >/dev/null 2>&1; then
  echo "npx not found — install Node (https://nodejs.org/) to regenerate the API reference."
  echo "Source of truth: openapi/rates-engine.v1.yaml"
  exit 1
fi

mkdir -p docs/reference/api

npx --yes "@redocly/cli@${REDOCLY_VERSION}" build-docs \
  openapi/rates-engine.v1.yaml \
  --output docs/reference/api/index.html \
  2>&1 | tail -5

cat > docs/reference/api/README.md <<'EOF'
<!-- GENERATED FILE - DO NOT EDIT. Source: openapi/rates-engine.v1.yaml -->
---
title: Generated API reference
last_verified: 2026-04-28
status: generated
---

# API reference

GENERATED FILE — do not edit by hand. Source of truth:
[`openapi/rates-engine.v1.yaml`](../../../openapi/rates-engine.v1.yaml).

The rendered reference is [`index.html`](index.html). Open it
directly in a browser, or serve via GitHub Pages.

To regenerate: `make docs-api`. CI verifies the rendered output
is in sync with the spec on every PR that touches either side.
EOF

echo "✓ docs/reference/api/index.html regenerated (${REDOCLY_VERSION})"
