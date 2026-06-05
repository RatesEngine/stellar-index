#!/usr/bin/env bash
# ch-backfill-monitor.sh — poll the Phase-3 full backfill and emit one status
# line per interval plus alert/terminal signals. Designed to be driven by the
# Claude Monitor tool (each stdout line becomes a notification) or run standalone.
#
# Emits:
#   PROGRESS  windows=W/T  ledger=N  lake=X  pool_free=Y  rate=R/s
#   DISK-PRESSURE ...   when pool free < $MIN_FREE_TIB (act before the shared
#                       pool starves Postgres/MinIO — they live on it too)
#   ORCHESTRATOR-EXITED — driver process gone; check log for COMPLETE vs FAILED
#   COMPLETE / FAILED   — terminal lines parsed from the driver log
#
# Env: HOST FROM TO WINDOW STATE LOG DRIVER_PAT INTERVAL MIN_FREE_TIB
set -uo pipefail

HOST=${HOST:-root@136.243.90.96}
FROM=${FROM:-1}
TO=${TO:?set TO}
WINDOW=${WINDOW:-1000000}
STATE=${STATE:-/var/lib/ch-backfill/done-windows.txt}
LOG=${LOG:-/var/log/ch-full-backfill.log}
DRIVER_PAT=${DRIVER_PAT:-ch-full-backfill.sh}
INTERVAL=${INTERVAL:-300}
MIN_FREE_TIB=${MIN_FREE_TIB:-2.0}
STEP=${STEP:-10}   # emit a progress line every STEP completed windows (plus the last)

total_windows=$(( (TO - FROM + WINDOW) / WINDOW ))
last_done=-1

while true; do
  out=$(ssh -o ConnectTimeout=15 "$HOST" "
    done=\$(wc -l < '$STATE' 2>/dev/null || echo 0)
    alive=\$(pgrep -f '$DRIVER_PAT' >/dev/null 2>&1 && echo 1 || echo 0)
    lake=\$(clickhouse-client --port 9300 --query \"SELECT formatReadableSize(sum(bytes_on_disk)) FROM system.parts WHERE database='stellar' AND active\" 2>/dev/null)
    free=\$(zfs list -Hp -o avail data 2>/dev/null)
    cur=\$(tail -3 '$LOG' 2>/dev/null | grep -oE 'window [0-9]+-[0-9]+' | tail -1)
    term=\$(grep -E 'ALL WINDOWS COMPLETE|FAILED' '$LOG' 2>/dev/null | tail -1)
    echo \"\$done|\$alive|\$lake|\$free|\$cur|\$term\"
  " 2>/dev/null || echo "SSHFAIL")

  if [ "$out" = "SSHFAIL" ] || [ -z "$out" ]; then
    sleep "$INTERVAL"; continue
  fi

  IFS='|' read -r done alive lake free cur term <<EOF
$out
EOF

  free_tib=$(awk -v b="${free:-0}" 'BEGIN{printf "%.2f", b/1099511627776}')
  # Emit a progress line only at every STEP-th completed window (and the
  # final one) — keeps notifications to a handful of milestones over a
  # multi-day run instead of one per window. Alerts + terminal states below
  # always emit, regardless of STEP.
  if [ "${done:-0}" != "$last_done" ]; then
    last_done=${done:-0}
    if [ "$(( done % STEP ))" -eq 0 ] || [ "${done:-0}" -ge "$total_windows" ]; then
      echo "PROGRESS windows=${done:-0}/${total_windows} at=[${cur:-?}] lake=${lake:-?} pool_free=${free_tib}TiB driver_alive=${alive:-0}"
    fi
  fi

  # Disk-pressure guard — the CH lake shares the ZFS pool with Postgres + MinIO.
  if awk -v f="$free_tib" -v m="$MIN_FREE_TIB" 'BEGIN{exit !(f < m)}'; then
    echo "DISK-PRESSURE pool_free=${free_tib}TiB < ${MIN_FREE_TIB}TiB — pause backfill + reclaim/relocate before the shared pool starves live services"
  fi

  if [ "${term:-}" != "" ]; then
    case "$term" in
      *"ALL WINDOWS COMPLETE"*) echo "COMPLETE $term"; break ;;
      *FAILED*) echo "FAILED $term" ;;
    esac
  fi
  if [ "${alive:-0}" = "0" ] && [ "${term:-}" = "" ]; then
    echo "ORCHESTRATOR-EXITED driver gone with no COMPLETE marker — likely crashed mid-window; re-run ch-full-backfill.sh to resume"
  fi

  sleep "$INTERVAL"
done
