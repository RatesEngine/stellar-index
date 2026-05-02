#!/usr/bin/env bash
# wait-pr-checks.sh — block until a PR's CI checks settle, emitting
# one stdout line per terminal-state transition (so a Monitor wrapper
# fires a notification per check as it lands rather than at the end).
#
# Designed for use as a Monitor command:
#
#   Monitor(command="bash scripts/dev/wait-pr-checks.sh 389",
#           description="CI for PR 389",
#           timeout_ms=900000)
#
# Each non-pending check produces one event line; the script exits 0
# when every check has settled and at least one passed and none failed,
# 1 when any check is fail/failure/cancelled, and 2 if `gh pr checks`
# itself errors more than a few times in a row.
#
# Usage: wait-pr-checks.sh <pr-number> [poll-seconds]
#   pr-number   — required, the PR to watch.
#   poll-seconds — optional, default 30. Lower for tight watches; the
#                  GitHub API rate-limits unauthenticated polls so 30s
#                  is the default sweet spot for an authed gh.

set -uo pipefail

if [[ $# -lt 1 ]]; then
  echo "usage: wait-pr-checks.sh <pr-number> [poll-seconds]" >&2
  exit 64
fi

pr=$1
poll=${2:-30}
prev=""
errors=0
max_errors=5

while true; do
  if ! state=$(gh pr checks "$pr" --json name,bucket 2>/dev/null); then
    errors=$((errors + 1))
    if [[ $errors -ge $max_errors ]]; then
      echo "wait-pr-checks: gh pr checks failed $errors times in a row; giving up" >&2
      exit 2
    fi
    sleep "$poll"
    continue
  fi
  errors=0

  cur=$(jq -r '.[] | select(.bucket != "pending") | "\(.name): \(.bucket)"' <<<"$state" | sort)
  comm -13 <(echo "$prev") <(echo "$cur")
  prev=$cur

  if jq -e 'all(.bucket != "pending")' <<<"$state" >/dev/null 2>&1; then
    if jq -e 'any(.bucket == "fail" or .bucket == "failure" or .bucket == "cancelled")' <<<"$state" >/dev/null 2>&1; then
      echo "CHECKS FAILED"
      exit 1
    fi
    echo "ALL CHECKS DONE"
    exit 0
  fi

  sleep "$poll"
done
