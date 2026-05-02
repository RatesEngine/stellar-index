#!/usr/bin/env bash
# wait-pr-checks.sh — block until a PR's CI checks settle, emitting
# stdout events as state changes. Designed for use as a Monitor
# command:
#
#   Monitor(command="bash scripts/dev/wait-pr-checks.sh 389",
#           description="CI for PR 389",
#           timeout_ms=900000)
#
# Stdout events (one per line):
#
#   START: pr=<N> total=<T>            — emitted immediately at boot
#                                        so Monitor sees activity even
#                                        when there's no transition.
#   <check name>: <bucket>             — one line each time a check
#                                        moves out of pending.
#   HEARTBEAT: pending=<P> done=<D>/<T> — emitted every heartbeat-poll
#                                        interval (default every 4
#                                        polls = 2min at 30s) so a
#                                        long all-pending wait still
#                                        shows progress to Monitor.
#   ALL CHECKS DONE                    — terminal success.
#   CHECKS FAILED: <names…>            — terminal failure (any
#                                        fail/failure/cancelled).
#   ERROR: gh failed N times           — terminal abort.
#
# Exit codes: 0 = all pass, 1 = at least one fail, 2 = abort.
#
# Usage:
#   wait-pr-checks.sh <pr-number> [poll-seconds] [heartbeat-every-N-polls]
#
# Defaults: poll-seconds=30, heartbeat-every-N-polls=4 (so a heartbeat
# fires every 2min). Lower poll-seconds for faster watches; raise the
# heartbeat divisor if Monitor is auto-stopping for too many events.

# Note: deliberately no `-e` — gh's transient failures are handled
# inline; we don't want one bad poll to kill the watch. `-u` catches
# unset vars; pipefail catches mid-pipe failures we'd otherwise miss.
set -uo pipefail

# Force line-buffered stdout so each printf reaches Monitor instantly
# regardless of stdio buffer state. Without this, in some environments
# the script can write transitions and have them sit in a 4KB buffer
# until the next poll, which looks like a stall. stdbuf is GNU
# coreutils — present on Linux, absent on stock macOS — so guard.
if command -v stdbuf >/dev/null 2>&1; then
  exec > >(stdbuf -oL cat)
fi

if [[ $# -lt 1 ]]; then
  echo "usage: wait-pr-checks.sh <pr-number> [poll-seconds] [heartbeat-every-N-polls]" >&2
  exit 64
fi

pr=$1
poll=${2:-30}
heartbeat_every=${3:-4}

errors=0
max_errors=5
poll_count=0
prev=""
seen_terminal=""
total=0

emit() { printf '%s\n' "$*"; }

# Boot state — query once before entering the loop so START carries
# the actual check count (Monitor's first event is then immediately
# informative). If even the first call fails, fall back to total=0
# so the loop gets a chance to recover.
boot_state=$(gh pr checks "$pr" --json name,bucket 2>/dev/null || true)
if [[ -n "$boot_state" ]]; then
  total=$(jq 'length' <<<"$boot_state" 2>/dev/null || echo 0)
fi
emit "START: pr=$pr total=$total"

while true; do
  poll_count=$((poll_count + 1))

  if ! state=$(gh pr checks "$pr" --json name,bucket 2>/dev/null); then
    errors=$((errors + 1))
    if [[ $errors -ge $max_errors ]]; then
      emit "ERROR: gh failed $errors times"
      exit 2
    fi
    sleep "$poll"
    continue
  fi
  errors=0

  # Recompute total on every poll — robust to checks added late by
  # workflow_run dispatchers; without this we'd miss them.
  total=$(jq 'length' <<<"$state" 2>/dev/null || echo 0)

  cur=$(jq -r '.[] | select(.bucket != "pending") | "\(.name): \(.bucket)"' <<<"$state" | sort)
  # Diff prev → cur: each new line in cur is a fresh terminal state.
  if new=$(comm -13 <(printf '%s\n' "$prev") <(printf '%s\n' "$cur")); then
    if [[ -n "$new" ]]; then
      while IFS= read -r line; do
        [[ -z "$line" ]] && continue
        emit "$line"
      done <<<"$new"
    fi
  fi
  prev=$cur

  done_count=$(printf '%s\n' "$cur" | grep -cv '^$' || true)
  pending_count=$((total - done_count))

  # Terminal? Require total > 0 so we don't exit on a transient
  # zero-checks state during workflow boot.
  if [[ $total -gt 0 && $pending_count -le 0 ]]; then
    if jq -e 'any(.bucket == "fail" or .bucket == "failure" or .bucket == "cancelled")' <<<"$state" >/dev/null 2>&1; then
      failed=$(jq -r '.[] | select(.bucket == "fail" or .bucket == "failure" or .bucket == "cancelled") | .name' <<<"$state" | tr '\n' ' ')
      emit "CHECKS FAILED: $failed"
      exit 1
    fi
    emit "ALL CHECKS DONE"
    exit 0
  fi

  # Heartbeat: emit every Nth poll while pending so Monitor never
  # goes >2min without a stdout event.
  if [[ $((poll_count % heartbeat_every)) -eq 0 ]]; then
    emit "HEARTBEAT: pending=$pending_count done=$done_count/$total"
  fi

  sleep "$poll"
done
