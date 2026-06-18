#!/usr/bin/env bash
# lint-metric-refs.sh — F-1329 guard against the "dead alert layer".
#
# Every `stellarindex_*` metric token referenced inside a Prometheus
# rule `expr:` MUST resolve to something that actually emits it — a
# `Name:` field in internal/obs (or any literal use in internal/ cmd/
# scripts/ configs/healthchecks, which covers textfile-collector .prom
# emitters too). A rule that selects a metric nothing produces is dead:
# it can never fire, so the operator gets a false sense of coverage.
# That is exactly how backups could silently stop with zero paging
# (the F-1329 finding).
#
# Scope + conservatism:
#   - Only `stellarindex_*` tokens are enforced. node_/pg_/redis_/
#     pgbackrest_ metrics come from third-party exporters whose full
#     metric set isn't in this repo, so enforcing them would be all
#     false positives. They're handled by promtool + the EXTERNAL_OK
#     reference list below (documentation only).
#   - Tokens that appear ONLY inside a `job=` / `job=~` matcher are
#     ignored — those are scrape-job names (e.g. stellarindex_indexer in
#     the multi-host config), not metric names.
#   - Recording-rule outputs use the `stellarindex:foo:5m` colon style,
#     which never matches the `stellarindex_[a-z0-9_]+` token regex.
#   - KNOWN_INERT lists references that are deliberately kept but cannot
#     fire (no producer exists yet / not applicable on this host). Each
#     entry is documented with an INERT comment in the rule file. Adding
#     a producer = remove the entry. Inventing a metric name that still
#     has no producer is NOT allowed: it would just move from "dead" to
#     "dead AND lying about being live".
#
# Exit code = number of un-accounted-for dead references (0 = clean).

set -uo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root" || exit 1

RULE_DIRS=(
  "deploy/monitoring/rules"
  "configs/prometheus/rules.r1"
)

# Directories scanned for emitters (Go Name: fields + textfile .prom + shell).
EMITTER_PATHS=(internal cmd scripts configs/healthchecks)

# Deliberately-inert references: kept in the rule files (with an INERT
# comment) but no producer exists / not applicable on this host. Keep
# this list in lockstep with the INERT comment blocks in the rule files.
KNOWN_INERT=(
  # storage.yml — TimescaleDB job-scheduler state; needs a custom
  # postgres_exporter query or a textfile SQL exporter (not yet built).
  stellarindex_cagg_last_refresh_unix
  stellarindex_cagg_refresh_interval_seconds
  stellarindex_uncompressed_chunks_older_than_7d
  # stellar.yml — no archive-publish-error counter is wired to Prometheus.
  stellarindex_stellar_archive_publish_errors_total
  # stellar.yml — stellar-core / stellar-rpc metrics come from the
  # external stellar-core-prometheus-exporter, which is NOT deployed on
  # r1 today (removed 2026-04-23; returns at Phase-3 / ADR-0004).
  stellarindex_stellar_core_last_ledger_time_unix
  stellarindex_stellar_core_peer_count
  stellarindex_stellar_rpc_latest_ledger_age_seconds
  # divergence.yml — per-asset prices live in Postgres + Redis, not the
  # Prometheus registry; a gauge would be high-cardinality (out of scope).
  stellarindex_our_price
  stellarindex_reference_price
)

# Third-party exporter metrics intentionally referenced by exprs. Listed
# for documentation only (the lint enforces stellarindex_* exclusively),
# so a future reader knows these are expected-external, not typos.
EXTERNAL_OK=(
  pg_up pg_replication_lag_seconds pg_locks_count pg_settings_max_locks_per_transaction
  pg_settings_max_connections pg_stat_activity_count
  redis_up redis_connected_slaves redis_memory_used_bytes redis_memory_max_bytes
  redis_evicted_keys_total redis_rdb_last_bgsave_status
  pgbackrest_backup_since_last_completion_seconds
  node_zfs_zpool_state node_filesystem_avail_bytes node_filesystem_size_bytes
  node_cpu_seconds_total node_memory_MemTotal_bytes node_memory_MemAvailable_bytes
  node_disk_io_errors_total node_nvme_temperature_celsius
  up
)

in_list() {
  local needle="$1"; shift
  local x
  for x in "$@"; do [[ "$x" == "$needle" ]] && return 0; done
  return 1
}

# Pull stellarindex_* tokens that appear inside a rule's `expr:` region.
# An expr region starts at an `expr:` line and ends at the next sibling
# key (`for:` / `labels:` / `annotations:` / `- alert:` / `- record:`).
# Tokens inside a job= / job=~ matcher are dropped (scrape-job names).
extract_expr_tokens() {
  awk '
    function strip_jobs(s) {
      # remove job="..." and job=~"..." matcher bodies so their tokens
      # (scrape-job names) are not mistaken for metric names.
      gsub(/job[[:space:]]*=~?[[:space:]]*"[^"]*"/, "", s)
      return s
    }
    /^[[:space:]]*expr:[[:space:]]*\|?[[:space:]]*$/ { inexpr=1; next }
    /^[[:space:]]*expr:/ { print strip_jobs(substr($0, index($0,"expr:")+5)); next }
    inexpr {
      if ($0 ~ /^[[:space:]]*(for|labels|annotations):/ || $0 ~ /^[[:space:]]*-[[:space:]]+(alert|record):/) { inexpr=0; next }
      print strip_jobs($0)
    }
  ' "$1" | grep -oE 'stellarindex_[a-zA-Z0-9_]+' || true
}

self_rel="scripts/ci/lint-metric-refs.sh"

is_emitted() {
  # True if the token appears as a literal anywhere an emitter could live.
  # Exclude this script (its KNOWN_INERT array would self-match) so an
  # inert token never looks "emitted" just because it's listed here.
  grep -rlF --include="*.go" --include="*.sh" --include="*.prom" -e "$1" "${EMITTER_PATHS[@]}" 2>/dev/null \
    | grep -vqxF "$self_rel" \
    && return 0
  return 1
}

dead=0

for dir in "${RULE_DIRS[@]}"; do
  [[ -d "$dir" ]] || { echo "lint-metric-refs: missing rule dir: $dir" >&2; exit 2; }
  while IFS= read -r -d '' f; do
    # Unique tokens for this file.
    while IFS= read -r tok; do
      [[ -z "$tok" ]] && continue
      if in_list "$tok" "${KNOWN_INERT[@]}"; then continue; fi
      if is_emitted "$tok"; then continue; fi
      echo "DEAD-REF: $f references '$tok' but nothing emits it (not in KNOWN_INERT)."
      dead=$((dead + 1))
    done < <(extract_expr_tokens "$f" | sort -u)
  done < <(find "$dir" -maxdepth 1 -name '*.yml' -print0)
done

# Reverse guard: a KNOWN_INERT entry that has GAINED a producer should be
# promoted to a live ref (and dropped from the list), not left lying.
for tok in "${KNOWN_INERT[@]}"; do
  if is_emitted "$tok"; then
    echo "STALE-INERT: '$tok' is in KNOWN_INERT but now HAS an emitter — drop it from the list and use the live metric." >&2
    dead=$((dead + 1))
  fi
done

if [[ "$dead" -eq 0 ]]; then
  echo "lint-metric-refs: OK — every stellarindex_* expr token resolves to an emitter or a documented KNOWN_INERT entry."
else
  echo "lint-metric-refs: FAIL — $dead dead/stale metric reference(s)." >&2
fi
exit "$dead"
