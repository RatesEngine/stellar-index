#!/usr/bin/env bash
# Doc-code consistency linter for Rates Engine.
#
# Runs in CI; fails the build if docs have drifted from code.
# Based on the pattern from ~/code/loop-app/scripts/lint-docs.sh —
# adapted for our Go + OpenAPI + Stellar-specific surface.
#
# Design principles (docs/discovery/engineering-standards.md §5):
#
#   1. Never two sources of truth.
#   2. Explain why, not what.
#   3. Decisions go in ADRs; narrative docs don't record decisions.
#   4. Every config option / metric / endpoint must round-trip
#      between code and reference docs.
#
# This script enforces (1) + (4). The others are reviewer-enforced.

set -euo pipefail

REPO_ROOT=$(cd "$(dirname "$0")/../.." && pwd)
cd "$REPO_ROOT"

ERROR_FILE=$(mktemp)
echo "0" > "$ERROR_FILE"

err() {
  echo "  ERROR: $1" >&2
  count=$(cat "$ERROR_FILE")
  echo "$((count + 1))" > "$ERROR_FILE"
}

# ─── 1. Every config `toml:"..."` tag must appear in the generated ref ──────
#
# The generated reference (docs/reference/config/README.md) uses the
# TOML field name (`toml:"xxx_yyy"` → "xxx_yyy" in the table), not the
# Go field identifier. So we check TOML names, not Go field names —
# the wire contract is what operators see.

echo "Checking config reference sync..."
if [ -f internal/config/config.go ] && [ -f docs/reference/config/README.md ]; then
  # Extract every `toml:"name"` tag value from config.go. Keeps only
  # the name (no commas, no omitempty).
  grep -oE 'toml:"[a-z_]+"' internal/config/config.go | \
    sed -E 's/toml:"([a-z_]+)"/\1/' | sort -u | while read -r tomlname; do
      if ! grep -qF "$tomlname" docs/reference/config/README.md; then
        err "Config TOML key '$tomlname' in config.go missing from docs/reference/config/README.md — run 'make docs-config' to regen"
      fi
  done
fi

# ─── 2. Every API route handler must be in OpenAPI ──────────────────────────
#
# Matches the idiom the v1 Server uses:
#   s.mux.HandleFunc("GET /v1/<path>", s.handleX)
# The OpenAPI spec lists routes WITHOUT the /v1 prefix (that's the
# server's base URL), so we strip /v1 before comparing.

echo "Checking API routes vs OpenAPI..."
if [ -d internal/api/v1 ] && [ -f openapi/rates-engine.v1.yaml ]; then
  # Forward: handlers that aren't in the spec (client misses them).
  grep -rhoE 'HandleFunc\("[A-Z]+ /v1[^"]*"' internal/api/v1/ 2>/dev/null | \
    sed -E 's|.*"[A-Z]+ /v1||; s|"$||' | \
    sed -E 's|^$|/|' | \
    sort -u | while IFS= read -r route; do
      [ -z "$route" ] && continue
      # OpenAPI path entries look like `  /ohlc:` at 2-space indent.
      if ! grep -qE "^  ${route}:" openapi/rates-engine.v1.yaml; then
        err "Route '$route' is registered in handlers but missing from OpenAPI spec"
      fi
  done

  # Reverse: spec entries that have no handler (clients 404).
  # The planned_regex below is the explicit allow-list of
  # "documented but not yet shipped" — deliberately adjusted in
  # a docs PR when endpoints land or get cut.
  planned_regex='^/(price/batch|price/stream|history/since-inception|pairs|oracle/lastprice|oracle/prices|oracle/x_last_price|account/me|account/usage|account/keys|assets/\{asset_id\}/metadata)$'
  grep -oE "^  /[^:]+:" openapi/rates-engine.v1.yaml | \
    sed -E 's|^  ||; s|:$||' | sort -u | while IFS= read -r route; do
      [ -z "$route" ] && continue
      if [[ "$route" =~ $planned_regex ]]; then
        continue
      fi
      # Fixed-string search (no regex) so Go 1.22 path params
      # like /assets/{asset_id} don't get interpreted as regex
      # quantifiers. Enumerate methods we use today — extend the
      # list when we add write verbs.
      found=0
      for method in GET POST PUT PATCH DELETE; do
        if grep -qrF "HandleFunc(\"${method} /v1${route}\"" internal/api/v1/ 2>/dev/null; then
          found=1
          break
        fi
      done
      if [ "$found" -eq 0 ]; then
        err "OpenAPI path '$route' has no handler. Add a handler or add it to planned_regex in lint-docs.sh"
      fi
  done
fi

# ─── 3. Every Prometheus metric must be documented in metrics reference ─────

echo "Checking metrics registry..."
if [ -d internal/obs ] && [ -f docs/reference/metrics/README.md ]; then
  # Every metric registered in internal/obs must appear in the
  # reference doc. Scope is all prometheus `Name: "..."` fields —
  # not just ratesengine_*/ctx_* — so `http_requests_total` and
  # `http_request_duration_seconds` (unprefixed per standard
  # Prometheus convention) are also enforced.
  # BSD sed (macOS default) doesn't support \s — use [[:space:]].
  grep -rhE 'Name:[[:space:]]*"[a-z][a-z0-9_]+"' internal/obs/ 2>/dev/null | \
    sed -E 's|.*Name:[[:space:]]*"([^"]+)".*|\1|' | sort -u | while read -r metric; do
      if ! grep -qF "$metric" docs/reference/metrics/README.md; then
        err "Metric '$metric' is registered in code but not in docs/reference/metrics/README.md"
      fi
  done
fi

# ─── 4. No references to deleted files / renamed concepts ───────────────────

echo "Checking for stale references..."
stale_patterns=(
  "horizon\.stellar\.org"        # Horizon deprecated — ADR-0001
  "ratesengine\.ctx\.io"         # old placeholder domain
  "ctx-indexer\|ctx-aggregator\|ctx-api\|ctx-ops\|ctx-migrate" # old binary names (we use ratesengine- prefix now — adjust if you change the policy)
  "CTX Rates"                    # old project name (now "Rates Engine")
)
for pattern in "${stale_patterns[@]}"; do
  matches=$(grep -rnE "$pattern" \
    README.md \
    CLAUDE.md \
    AGENTS.md \
    CONTRIBUTING.md \
    SECURITY.md \
    CODE_OF_CONDUCT.md \
    CHANGELOG.md \
    docs/reference/ \
    docs/architecture/ \
    docs/operations/ \
    docs/development/ \
    2>/dev/null | grep -v "node_modules\|_archive/\|discovery/" || true)
  if [ -n "$matches" ]; then
    err "Stale reference to '$pattern' in active docs:"
    echo "$matches" | sed 's/^/    /' >&2
  fi
done

# ─── 5. No forbidden tech-debt markers without issue links ──────────────────

echo "Checking TODO discipline..."
# Every TODO/FIXME/XXX in Go code must be of the form TODO(#N):
if [ -d internal ] || [ -d cmd ]; then
  bad_todos=$(grep -rnE '//\s*(TODO|FIXME|XXX)[^(]' \
    internal/ cmd/ pkg/ 2>/dev/null | \
    grep -vE '//\s*(TODO|FIXME|XXX)\(#[0-9]+\)' || true)
  if [ -n "$bad_todos" ]; then
    err "TODO/FIXME/XXX without linked issue number (must be 'TODO(#123): …'):"
    echo "$bad_todos" | sed 's/^/    /' >&2
  fi
fi

# ─── 6. Frontmatter freshness on 'current' docs ─────────────────────────────

echo "Checking doc frontmatter freshness..."
today=$(date -u +%s)
stale_threshold=$((90 * 24 * 60 * 60))   # 90 days in seconds
fail_threshold=$((180 * 24 * 60 * 60))   # 180 days — hard fail

# Iterate over 'current' docs. Docs without frontmatter are ignored at
# this level — we're not forcing frontmatter on every file, only on
# docs in the opt-in 'current' tracking set (architecture/, operations/, adr/).
find docs/architecture docs/operations docs/adr -type f -name '*.md' 2>/dev/null | while read -r f; do
  # Skip generated docs, archive, templates.
  if grep -q "GENERATED FILE - DO NOT EDIT" "$f" 2>/dev/null; then continue; fi
  if [[ "$f" == *"_archive"* ]] || [[ "$f" == *"_template"* ]]; then continue; fi

  # Extract last_verified date from frontmatter if present.
  verified=$(awk '/^last_verified:/{print $2; exit}' "$f" 2>/dev/null | tr -d '"')
  if [ -z "$verified" ]; then continue; fi

  verified_epoch=$(date -u -j -f "%Y-%m-%d" "$verified" +%s 2>/dev/null || \
                   date -u -d "$verified" +%s 2>/dev/null || echo "")
  if [ -z "$verified_epoch" ]; then continue; fi

  age=$((today - verified_epoch))
  if [ "$age" -gt "$fail_threshold" ]; then
    err "Doc '$f' is STALE — last_verified $verified is > 180 days old"
  elif [ "$age" -gt "$stale_threshold" ]; then
    echo "  WARN: doc '$f' last_verified $verified is > 90 days old — refresh soon" >&2
  fi
done

# ─── 7. Generated-file banner intact ────────────────────────────────────────
#
# Only the three generated subdirs under docs/reference/ are machine-
# produced. docs/reference/*.md at the top level is hand-written
# narrative (e.g. api-design.md).

echo "Checking generated-file banners..."
# docs/reference/metrics/README.md is the ONLY hand-written file
# under docs/reference/ — there's no metrics generator yet (would
# need a Prometheus-registry walker). It's still lint-enforced
# for drift via section 3. Exempt only by exact path.
#
# Enumerate only existing subdirs — `find` errors on missing ones
# with set -e + pipefail, silently killing the script before later
# sections run.
gen_dirs=()
for d in docs/reference/api docs/reference/config docs/reference/metrics; do
  [ -d "$d" ] && gen_dirs+=("$d")
done
if [ ${#gen_dirs[@]} -gt 0 ]; then
  find "${gen_dirs[@]}" -type f -name '*.md' 2>/dev/null | while read -r f; do
    if [ "$f" = "docs/reference/metrics/README.md" ]; then
      continue
    fi
    if ! head -1 "$f" | grep -qF "GENERATED FILE"; then
      err "Generated file '$f' is missing the 'GENERATED FILE - DO NOT EDIT' banner at line 1"
    fi
  done
fi

# ─── 8. Every ADR has valid status + not-superseded-unless-noted ────────────

echo "Checking ADR integrity..."
if [ -d docs/adr ]; then
  for adr in docs/adr/[0-9]*.md; do
    [ -f "$adr" ] || continue
    status=$(awk '/^status:/{print $2; exit}' "$adr")
    if [[ ! "$status" =~ ^(Proposed|Accepted|Superseded|Rejected)$ ]]; then
      err "ADR '$adr' has invalid status '$status' (must be Proposed|Accepted|Superseded|Rejected)"
    fi
    superseded_by=$(awk '/^superseded_by:/{print $2; exit}' "$adr" | tr -d '"')
    if [ "$status" = "Superseded" ] && [ "$superseded_by" = "null" -o -z "$superseded_by" ]; then
      err "ADR '$adr' marked Superseded but 'superseded_by' is null"
    fi
  done
fi

# ─── 9. Every alert rule's runbook_url must point to an existing file ──────
#
# Prometheus alert rules ship with `runbook_url` so the pager routes
# oncall to a specific diagnosis page. A 404 runbook URL means the
# responder gets dumped on a GitHub error page at 3 AM — the opposite
# of useful. This check greps every runbook_url out of deploy/
# monitoring/rules/*.yml and asserts the referenced file exists.

echo "Checking alert-rule runbook_url targets..."
if [ -d deploy/monitoring/rules ]; then
  for rule_file in deploy/monitoring/rules/*.yml; do
    # Extract the path suffix after /runbooks/ for every runbook_url.
    grep -oE 'runbook_url:[[:space:]]*https://[^[:space:]]+/docs/operations/runbooks/[^[:space:]]+\.md' "$rule_file" 2>/dev/null | \
      sed -E 's|.*/docs/operations/runbooks/|docs/operations/runbooks/|' | \
      sort -u | \
      while IFS= read -r runbook_path; do
        [ -z "$runbook_path" ] && continue
        if [ ! -f "$runbook_path" ]; then
          err "alert rule references missing runbook: $runbook_path (from $rule_file)"
        fi
      done
  done
fi

# ─── 10. Every alert rule must have a row in the alerts catalogue ──────────
#
# Catalogue is docs/operations/alerts-catalog.md; every rule file's
# `alert: <name>` must appear verbatim somewhere in that doc. Caught
# the `ratesengine_ingestion_insert_errors` drift on 2026-04-23 —
# the alert was live but the catalogue didn't list it.

echo "Checking alerts-catalog drift..."
if [ -d deploy/monitoring/rules ] && [ -f docs/operations/alerts-catalog.md ]; then
  grep -rhE '^[[:space:]]*-[[:space:]]*alert:[[:space:]]*' deploy/monitoring/rules/ 2>/dev/null | \
    sed -E 's|.*alert:[[:space:]]*||' | sort -u | while IFS= read -r alert; do
      [ -z "$alert" ] && continue
      if ! grep -qF "$alert" docs/operations/alerts-catalog.md; then
        err "alert rule '$alert' not listed in docs/operations/alerts-catalog.md"
      fi
    done
fi

# ─── 11. Runbook body references to `ratesengine_source_*` metrics ─────────
#
# Narrow rule: only `ratesengine_source_*` (the namespace fully
# owned by internal/obs/metrics.go). External-exporter metrics
# (ratesengine_stellar_core_*, pgbackrest_*, etc.) are intentionally
# out of scope — those live in node-side exporters we don't control.
#
# Caught `ratesengine_source_last_event_age_seconds` drift on
# 2026-04-23 — runbook referenced a metric name that never existed.

echo "Checking runbook metric-name freshness..."
if [ -d docs/operations/runbooks ] && [ -f internal/obs/metrics.go ]; then
  # Build the allowed set: names registered in obs.metrics.go +
  # alert names in Prometheus rules (runbooks use either). `|| true`
  # because under set -e + pipefail, grep returning 1 for no-match
  # would kill the whole script — we explicitly want an empty set
  # if no matches.
  allowed=$(mktemp)
  {
    (grep -hE 'Name:[[:space:]]*"ratesengine_source_[a-z_]+"' internal/obs/metrics.go 2>/dev/null || true) | \
      sed -E 's|.*"(ratesengine_source_[a-z_]+)".*|\1|'
    (grep -rhE '^[[:space:]]*-[[:space:]]*alert:[[:space:]]*ratesengine_source_' deploy/monitoring/rules/ 2>/dev/null || true) | \
      sed -E 's|.*alert:[[:space:]]*||'
  } | sort -u > "$allowed"

  # Extract every ratesengine_source_* token from runbook bodies.
  (grep -rhoE 'ratesengine_source_[a-z_]+' docs/operations/runbooks/ 2>/dev/null || true) | \
    sort -u | while IFS= read -r metric; do
      [ -z "$metric" ] && continue
      if ! grep -qxF "$metric" "$allowed"; then
        err "runbook references unknown metric '$metric' (not in internal/obs or rules/)"
      fi
    done
  rm -f "$allowed"
fi

# ─── Summary ────────────────────────────────────────────────────────────────

count=$(cat "$ERROR_FILE")
rm "$ERROR_FILE"

if [ "$count" -gt 0 ]; then
  echo ""
  echo "❌ Doc lint failed with $count error(s)."
  exit 1
fi
echo "✅ Doc lint passed."
