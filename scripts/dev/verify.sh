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
echo ""
echo "✅ ALL CHECKS PASSED"
