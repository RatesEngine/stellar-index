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
echo "=== Test ==="          && make test
echo ""
echo "✅ ALL CHECKS PASSED"
