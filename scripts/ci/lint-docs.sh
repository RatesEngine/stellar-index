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

echo "Checking API routes vs OpenAPI..."
if [ -d internal/api/v1 ] && [ -f openapi/rates-engine.v1.yaml ]; then
  grep -rE "^\s*(Get|Post|Put|Delete|Patch)\(" internal/api/v1/ 2>/dev/null | \
    sed -E 's|.*"(/v1[^"]*)".*|\1|' | sort -u | while read -r route; do
      # Normalise any path params ({foo}) — OpenAPI uses same syntax.
      if ! grep -qE "^\s+\"$route\"" openapi/rates-engine.v1.yaml; then
        err "Route '$route' is registered in handlers but missing from OpenAPI spec"
      fi
  done
fi

# ─── 3. Every Prometheus metric must be documented in metrics reference ─────

echo "Checking metrics registry..."
if [ -d internal/obs ] && [ -f docs/reference/metrics/README.md ]; then
  grep -rhE 'Name:\s*"(ctx|ratesengine)_[a-z_]+"' internal/obs/ 2>/dev/null | \
    sed -E 's|.*Name:\s*"([^"]+)".*|\1|' | sort -u | while read -r metric; do
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
find docs/reference/api docs/reference/config docs/reference/metrics \
     -type f -name '*.md' 2>/dev/null | while read -r f; do
  if ! head -1 "$f" | grep -qF "GENERATED FILE"; then
    err "Generated file '$f' is missing the 'GENERATED FILE - DO NOT EDIT' banner at line 1"
  fi
done

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

# ─── Summary ────────────────────────────────────────────────────────────────

count=$(cat "$ERROR_FILE")
rm "$ERROR_FILE"

if [ "$count" -gt 0 ]; then
  echo ""
  echo "❌ Doc lint failed with $count error(s)."
  exit 1
fi
echo "✅ Doc lint passed."
