#!/usr/bin/env bash
# Local sequential quality checks — run this before every push.
#
# CI runs these jobs in parallel; verify.sh is the strictly-sequential
# local equivalent that surfaces failures one at a time. Pattern
# borrowed from loop-app/scripts/verify.sh.

set -euo pipefail

cd "$(dirname "$0")/../.."

echo "=== Format ==="        && make fmt
echo "=== Vet ==="           && make vet
echo "=== Lint ==="          && make lint
echo "=== Docs ==="          && ./scripts/ci/lint-docs.sh
echo "=== Imports ==="       && ./scripts/ci/lint-imports.sh
echo "=== OpenAPI URLs ===" && go run ./scripts/ci/lint-openapi-urls openapi/rates-engine.v1.yaml
# Prometheus rule files. Graceful-skip when promtool isn't
# installed locally — CI installs it explicitly. The Makefile
# target hard-fails on missing promtool; verify.sh wraps it with
# an existence check so local-dev `bash scripts/dev/verify.sh`
# keeps working without a full Prometheus install.
if command -v promtool >/dev/null 2>&1; then
    echo "=== Monitoring ===" && make monitoring-check
else
    echo "=== Monitoring (skipped — promtool not installed; install via 'brew install prometheus' or the Prometheus GH release) ==="
fi
echo "=== Test ==="          && make test
# Compile-only: catches interface-extension breakage in
# build-tagged integration adapters without spinning testcontainers.
# Real `make test-integration` lives outside verify because Docker
# isn't always available locally.
echo "=== Integration build ===" && make test-integration-build
# Showcase typecheck + lint + build. Graceful-skip when pnpm
# isn't installed locally — CI runs the same gate via the
# `web/explorer` job, so a local skip just defers the check.
# The build catches Next.js output: 'export' constraints
# (e.g. dynamic = 'force-static' on sitemap/robots) that
# typecheck alone misses.
if command -v pnpm >/dev/null 2>&1 && [ -f web/explorer/pnpm-lock.yaml ]; then
    echo "=== Showcase typecheck ===" && make web-typecheck
    echo "=== Showcase lint ==="      && make web-lint
    echo "=== Showcase build ==="     && \
        NEXT_PUBLIC_API_BASE_URL=http://api.local-stub.invalid make web-build >/dev/null
else
    echo "=== Showcase (skipped — pnpm not installed; install via 'brew install pnpm' or 'corepack enable') ==="
fi
# Dashboard SPA — same pnpm gate. Skipped silently when the
# lockfile is missing (e.g. fresh checkouts that haven't installed).
if command -v pnpm >/dev/null 2>&1 && [ -f web/dashboard/pnpm-lock.yaml ]; then
    echo "=== Dashboard typecheck ===" && make dashboard-typecheck
    echo "=== Dashboard lint ==="      && make dashboard-lint
    echo "=== Dashboard build ==="     && \
        NEXT_PUBLIC_API_BASE_URL=http://api.local-stub.invalid make dashboard-build >/dev/null
fi
echo ""
echo "✅ ALL CHECKS PASSED"
