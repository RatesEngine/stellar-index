#!/bin/bash
# Pretty status snapshot for the galexie backfill. Run standalone or
# with "watch -n 5 galexie-backfill-status" for a live view.
set -u
CYAN="\033[36m"; YELLOW="\033[33m"; GREEN="\033[32m"; RED="\033[31m"
MAGENTA="\033[35m"; BLUE="\033[34m"; RESET="\033[0m"; BOLD="\033[1m"
LOG=/var/log/galexie-backfill.log

# ─── Start time ─────────────────────────────────────────────
start_line=$(head -1 $LOG 2>/dev/null)
start_ts=$(echo "$start_line" | grep -oE "^time=\"[^\"]+\"" | sed "s/time=//;s/\"//g" | head -1)
if [ -n "$start_ts" ]; then
    start_epoch=$(date -d "$start_ts" +%s 2>/dev/null || echo "")
    now_epoch=$(date +%s)
    if [ -n "$start_epoch" ]; then
        elapsed_s=$((now_epoch - start_epoch))
        elapsed_h=$((elapsed_s / 3600))
        elapsed_m=$(((elapsed_s % 3600) / 60))
    fi
fi

# ─── Phase + progress ───────────────────────────────────────
tail_log=$(tail -30 $LOG 2>/dev/null)
dl_line=$(echo "$tail_log" | grep -oE "ledger files [0-9]+/[0-9]+" | tail -1)
dl_cur=$(echo "$dl_line" | grep -oE "[0-9]+/" | head -1 | tr -d "/")
dl_total=$(echo "$dl_line" | grep -oE "/[0-9]+" | head -1 | tr -d "/")
phase="unknown"
if echo "$tail_log" | grep -q "Applying ledger"; then
    phase="3 — ledger apply (writing LCMs)"
elif echo "$tail_log" | grep -q "Applying buckets"; then
    phase="2 — bucket apply (state rebuild)"
elif [ -n "$dl_line" ]; then
    phase="1 — history download"
fi

# ─── Bucket counts ──────────────────────────────────────────
archive_count=$(mc ls --quiet --recursive local/galexie-archive/ 2>/dev/null | wc -l)
archive_size=$(mc du --quiet local/galexie-archive/ 2>/dev/null | awk "{print \$1}" | head -1)
live_tip=$(mc ls --quiet --recursive local/galexie-live/ 2>/dev/null | awk "{print \$NF}" | grep xdr.zst | sed -E "s/.*--([0-9]+)\.xdr\.zst/\1/" | sort -n | tail -1)

# ─── Host ───────────────────────────────────────────────────
load=$(uptime | grep -oE "load average: [^\"]+$" | sed "s/load average: //")
zfs_free=$(zfs list -Ho avail data/galexie 2>/dev/null)
srv_hist=$(df -h /srv/history-archive 2>/dev/null | tail -1 | awk "{print \$3 \" used, \" \$4 \" free (\" \$5 \")\"}")
zpool_io=$(zpool iostat -y data 1 1 2>/dev/null | tail -1 | awk "{print \"r=\" \$4 \"/s w=\" \$5 \"/s r-bw=\" \$6 \" w-bw=\" \$7}")

# ─── Processes ──────────────────────────────────────────────
backfill_pid=$(pgrep -f "galexie scan-and-fill" | head -1)
live_pid=$(pgrep -f "galexie append" | head -1)
captive_backfill=$(pgrep -af "stellar-core.*catchup" | head -1 | awk "{print \$1}")
mirror_pid=$(pgrep -f "mc mirror.*galexie-archive" | head -1)
backfill_cpu=""
if [ -n "$backfill_pid" ]; then
    backfill_cpu=$(ps -o %cpu,rss --no-headers -p "$backfill_pid" 2>/dev/null)
fi
captive_cpu=""
if [ -n "$captive_backfill" ]; then
    captive_cpu=$(ps -o %cpu,rss --no-headers -p "$captive_backfill" 2>/dev/null)
fi
mirror_cpu=""
mirror_files=""
mirror_last=""
mirror_log=/var/log/galexie-mirror.log
if [ -n "$mirror_pid" ]; then
    mirror_cpu=$(ps -o %cpu,rss --no-headers -p "$mirror_pid" 2>/dev/null)
fi
if [ -f "$mirror_log" ]; then
    mirror_files=$(wc -l < "$mirror_log" 2>/dev/null)
    mirror_last=$(tail -1 "$mirror_log" 2>/dev/null | grep -oE "pubnet/[A-F0-9]+--[0-9-]+/[A-F0-9]+--[0-9]+\.xdr\.zst" | head -1)
fi

# ─── Progress bar ───────────────────────────────────────────
bar_width=40
if [ -n "$dl_cur" ] && [ -n "$dl_total" ] && [ "$dl_total" != "0" ]; then
    pct=$((dl_cur * 100 / dl_total))
    filled=$((dl_cur * bar_width / dl_total))
    bar=""
    for ((i=0;i<bar_width;i++)); do
        if [ $i -lt $filled ]; then bar="${bar}█"; else bar="${bar}░"; fi
    done
fi

# ─── Rate / ETA ────────────────────────────────────────────
rate_file=/var/lib/galexie-backfill-rate
prev_line=""
[ -f "$rate_file" ] && prev_line=$(cat "$rate_file")
prev_epoch=$(echo "$prev_line" | awk "{print \$1}")
prev_dl=$(echo "$prev_line" | awk "{print \$2}")
rate_str="—"
eta_str="—"
if [ -n "$prev_epoch" ] && [ -n "$prev_dl" ] && [ -n "$dl_cur" ]; then
    dt=$((now_epoch - prev_epoch))
    ddl=$((dl_cur - prev_dl))
    if [ "$dt" -gt 0 ] && [ "$ddl" -gt 0 ]; then
        rate=$((ddl / dt))
        rate_str="${rate} files/sec"
        remaining=$((dl_total - dl_cur))
        eta_s=$((remaining / rate))
        eta_h=$((eta_s / 3600))
        eta_m=$(((eta_s % 3600) / 60))
        eta_str="${eta_h}h ${eta_m}m"
    fi
fi
# Update rate file for next invocation (only if enough time has passed)
if [ -z "$prev_epoch" ] || [ $((now_epoch - prev_epoch)) -ge 5 ]; then
    echo "$now_epoch $dl_cur" > "$rate_file"
fi

# ─── Mirror rate / ETA ─────────────────────────────────────
mirror_rate_file=/var/lib/galexie-mirror-rate
prev_mirror_line=""
[ -f "$mirror_rate_file" ] && prev_mirror_line=$(cat "$mirror_rate_file")
prev_m_epoch=$(echo "$prev_mirror_line" | awk "{print \$1}")
prev_m_files=$(echo "$prev_mirror_line" | awk "{print \$2}")
mirror_rate_str="—"
mirror_eta_str="—"
if [ -n "$prev_m_epoch" ] && [ -n "$prev_m_files" ] && [ -n "$mirror_files" ] && [ "$mirror_files" -gt 0 ]; then
    mdt=$((now_epoch - prev_m_epoch))
    mdf=$((mirror_files - prev_m_files))
    if [ "$mdt" -gt 0 ] && [ "$mdf" -gt 0 ]; then
        mirror_rate=$((mdf / mdt))
        mirror_rate_str="${mirror_rate} files/sec"
        # AWS public bucket has ~62.3M ledgers (one .xdr.zst each, plus
        # ~975 .config.json manifests). Use that as the ETA target.
        target_total=62300000
        remaining=$((target_total - archive_count))
        if [ "$remaining" -gt 0 ] && [ "$mirror_rate" -gt 0 ]; then
            meta_s=$((remaining / mirror_rate))
            meta_h=$((meta_s / 3600))
            meta_m=$(((meta_s % 3600) / 60))
            mirror_eta_str="${meta_h}h ${meta_m}m"
        fi
    fi
fi
if [ -n "$mirror_files" ] && { [ -z "$prev_m_epoch" ] || [ $((now_epoch - prev_m_epoch)) -ge 5 ]; }; then
    echo "$now_epoch $mirror_files" > "$mirror_rate_file"
fi

# ─── Render ─────────────────────────────────────────────────
printf "${BOLD}${CYAN}═══════════ galexie backfill — genesis → 62,249,727 ═══════════${RESET}\n"
printf "${BOLD}elapsed:${RESET}    %sh %sm  ${BOLD}started:${RESET} %s\n" "${elapsed_h:-?}" "${elapsed_m:-?}" "${start_ts:-unknown}"
printf "${BOLD}phase:${RESET}      ${YELLOW}%s${RESET}\n\n" "$phase"

printf "${BOLD}${BLUE}── history download (scan-and-fill) ──${RESET}\n"
if [ -n "$dl_line" ]; then
    printf "  progress:  %s %s%%  (%s/%s)\n" "${bar:-?}" "${pct:-?}" "$dl_cur" "$dl_total"
    printf "  rate:      %s   ${BOLD}ETA:${RESET} %s\n" "$rate_str" "$eta_str"
else
    printf "  ${YELLOW}(no scan-and-fill activity in recent log)${RESET}\n"
fi
printf "\n"

printf "${BOLD}${BLUE}── mirror (mc mirror, AWS public → galexie-archive) ──${RESET}\n"
if [ -n "$mirror_pid" ] || { [ -n "$mirror_files" ] && [ "${mirror_files:-0}" -gt 0 ]; }; then
    printf "  files copied: %s   rate: %s   ${BOLD}ETA:${RESET} %s\n" "${mirror_files:-0}" "$mirror_rate_str" "$mirror_eta_str"
    [ -n "$mirror_last" ] && printf "  current:      %s\n" "$mirror_last"
else
    printf "  ${YELLOW}(no mirror activity in recent log)${RESET}\n"
fi
printf "\n"

printf "${BOLD}${MAGENTA}── LCM output (galexie-archive bucket) ──${RESET}\n"
printf "  objects:   %s\n" "$archive_count"
printf "  size:      %s\n\n" "${archive_size:-?}"

printf "${BOLD}${GREEN}── host ──${RESET}\n"
printf "  load:      %s\n" "${load:-?}"
printf "  zpool I/O: %s\n" "${zpool_io:-?}"
printf "  /srv/history-archive: %s\n" "${srv_hist:-?}"
printf "  data/galexie free:    %s\n\n" "${zfs_free:-?}"

printf "${BOLD}── processes ──${RESET}\n"
if [ -n "$backfill_pid" ]; then
    printf "  ${GREEN}● backfill (scan-and-fill)${RESET}  PID %s  cpu/mem: %s\n" "$backfill_pid" "${backfill_cpu:-?}"
else
    printf "  ${RED}● backfill (scan-and-fill)${RESET}  ${RED}NOT RUNNING${RESET}\n"
fi
if [ -n "$captive_backfill" ]; then
    printf "  ${GREEN}● captive-core (backfill)${RESET}   PID %s  cpu/mem: %s\n" "$captive_backfill" "${captive_cpu:-?}"
fi
if [ -n "$mirror_pid" ]; then
    printf "  ${GREEN}● mirror (mc mirror)${RESET}        PID %s  cpu/mem: %s\n" "$mirror_pid" "${mirror_cpu:-?}"
else
    printf "  ${RED}● mirror (mc mirror)${RESET}        ${RED}NOT RUNNING${RESET}\n"
fi
if [ -n "$live_pid" ]; then
    printf "  ${GREEN}● galexie live (append)${RESET}     PID %s  live-tip: ledger %s\n" "$live_pid" "${live_tip:-?}"
fi

printf "\n${BOLD}tail of scan-and-fill log (last 3):${RESET}\n"
tail -3 $LOG 2>/dev/null | sed "s/^/  /" | cut -c1-160
if [ -f "$mirror_log" ]; then
    printf "\n${BOLD}tail of mirror log (last 3):${RESET}\n"
    tail -3 "$mirror_log" 2>/dev/null | sed "s/^/  /" | cut -c1-160
fi
