#!/usr/bin/env bash
# lint-actions-pinning.sh — enforce SHA-pinning policy for third-
# party GitHub Actions.
#
# F-1216 (codex audit-2026-05-12): without SHA-pinning, a
# compromised tag on a third-party action repo can land arbitrary
# code on every CI run. `actions/*` is hosted by GitHub itself
# (still mutable but a single trust boundary); everything else
# should be pinned by SHA + version comment.
#
# This script enumerates `uses:` lines in .github/workflows/*.yml,
# bucket them by namespace, and:
#
#   - Hard-fails when a NEWLY-introduced third-party `uses:` is
#     tag-pinned (PR-time gate via the PR_DIFF env var below).
#   - Warns on existing tag-pinned third-party `uses:` so operators
#     can SHA-pin them incrementally via Dependabot bumps.
#
# Workflow:
#   1. Dependabot (already configured for github-actions, see
#      .github/dependabot.yml) opens PRs against tag-pinned third-
#      party actions when the upstream cuts a new version.
#   2. The operator reviewing the Dependabot PR resolves the tag
#      to a commit SHA via `gh api repos/<owner>/<repo>/commits/
#      <tag> --jq .sha` and updates the `uses:` line to that SHA
#      (with the version as a trailing comment).
#   3. This script's hard-fail gate prevents new tag-pinned
#      additions from slipping into the tree without that step.
#
# Repository policy half of F-1216 (allowed_actions=selected,
# require_sha_pinning) is configured via the GitHub admin UI; this
# script enforces the workflow-side discipline.
#
# Usage:
#   bash scripts/ci/lint-actions-pinning.sh             # full audit
#   PR_DIFF=1 bash scripts/ci/lint-actions-pinning.sh   # gate mode
#                                                       # (diff vs. main)

set -euo pipefail

cd "$(dirname "$0")/../.."

# Third-party namespaces. Everything outside actions/* is treated
# as third-party for SHA-pinning purposes.
THIRD_PARTY_RE='^(cloudflare|docker|golangci|grafana|pnpm|stoplightio|softprops|peter-evans|ncipollo|google-github-actions|aquasecurity|anchore|github/codeql-action|cycjimmy)/'

# A pin is a SHA when it's 40 hex chars after the `@`.
SHA_RE='@[0-9a-f]{40}([[:space:]]|$)'

WARN=0
FAIL=0

# Files to inspect. Use a tmpfile + while-read to stay portable
# across bash 3.x (macOS default) and bash 4+.
WORKFLOWS=()
while IFS= read -r f; do
  WORKFLOWS+=("$f")
done < <(find .github/workflows -name '*.yml' -type f | sort)

for f in "${WORKFLOWS[@]}"; do
  while IFS= read -r line; do
    if ! [[ "$line" =~ uses:[[:space:]]+([^[:space:]]+) ]]; then
      continue
    fi
    ref="${BASH_REMATCH[1]}"
    # Strip leading dash/whitespace already gone via the regex.
    # actions/* and github/* are first-party-ish — only count
    # third-party namespaces.
    short="${ref%@*}"
    if [[ "$short" == actions/* || "$short" == github/* ]]; then
      continue
    fi
    if ! [[ "$short" =~ $THIRD_PARTY_RE ]]; then
      # Unrecognised third-party namespace — still warn so we
      # notice when a new vendor lands.
      :
    fi
    if [[ "$ref" =~ $SHA_RE ]]; then
      continue # already SHA-pinned
    fi
    echo "WARN: $f tag-pinned third-party action: $ref"
    WARN=$((WARN + 1))
  done < <(grep -nE 'uses:[[:space:]]+' "$f" || true)
done

# PR-mode gate: fail if NEW tag-pinned third-party uses lines
# appear in the diff against `main`.
if [[ -n "${PR_DIFF:-}" ]] && git rev-parse --verify origin/main >/dev/null 2>&1; then
  while IFS= read -r line; do
    if ! [[ "$line" =~ ^\+.*uses:[[:space:]]+([^[:space:]]+) ]]; then
      continue
    fi
    ref="${BASH_REMATCH[1]}"
    short="${ref%@*}"
    if [[ "$short" == actions/* || "$short" == github/* ]]; then
      continue
    fi
    if [[ "$ref" =~ $SHA_RE ]]; then
      continue
    fi
    echo "FAIL: new tag-pinned third-party action introduced: $ref"
    FAIL=$((FAIL + 1))
  done < <(git diff origin/main -- '.github/workflows/*.yml' || true)
fi

if [[ "$FAIL" -gt 0 ]]; then
  echo
  echo "❌ $FAIL new tag-pinned third-party action(s) — SHA-pin via:"
  echo "   gh api repos/<owner>/<repo>/commits/<tag> --jq .sha"
  echo "   then update: uses: vendor/action@<sha> # <tag>"
  exit 1
fi

if [[ "$WARN" -gt 0 ]]; then
  echo
  echo "ℹ️  $WARN existing tag-pinned third-party action(s) — Dependabot"
  echo "   will queue SHA-bumps over time; the PR_DIFF=1 gate blocks new"
  echo "   tag-pinned introductions."
fi
exit 0
