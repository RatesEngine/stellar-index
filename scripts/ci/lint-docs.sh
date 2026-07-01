#!/usr/bin/env bash
# Doc-code consistency linter for Stellar Index.
#
# Runs in CI; fails the build if docs have drifted from code.
# Based on the pattern from ~/code/loop-app/scripts/lint-docs.sh —
# adapted for our Go + OpenAPI + Stellar-specific surface.
#
# Design principles (docs/engineering-standards.md §5):
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
  # CS-131: [a-z0-9_]+ (was [a-z_]+, which silently skipped digit-bearing
  # tags like s3_*, sep10, sep41, phase2 — a rename of one would stay green).
  grep -oE 'toml:"[a-z0-9_]+"' internal/config/config.go | \
    sed -E 's/toml:"([a-z0-9_]+)"/\1/' | sort -u | while read -r tomlname; do
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
if [ -d internal/api/v1 ] && [ -f openapi/stellar-index.v1.yaml ]; then
  # Forward: handlers that aren't in the spec (client misses them).
  # CS-052: match BOTH HandleFunc("VERB /v1…") and mux.Handle("VERB /v1…")
  # — the latter is used for middleware-wrapped routes and previously slipped
  # past this check (that's how the undocumented staff route escaped).
  # internal_routes_re allow-lists routes deliberately kept out of the public
  # spec (staff/PII endpoints); add a route here with a reason to exempt it.
  internal_routes_re='^/account/admin/'  # staff-only lookup, intentionally not public
  grep -rhoE 'Handle(Func)?\("[A-Z]+ /v1[^"]*"' internal/api/v1/ 2>/dev/null | \
    sed -E 's|.*"[A-Z]+ /v1||; s|"$||' | \
    sed -E 's|^$|/|' | \
    sort -u | while IFS= read -r route; do
      [ -z "$route" ] && continue
      if [[ "$route" =~ $internal_routes_re ]]; then continue; fi
      # OpenAPI path entries look like `  /ohlc:` at 2-space indent.
      if ! grep -qE "^  ${route}:" openapi/stellar-index.v1.yaml; then
        err "Route '$route' is registered in handlers but missing from OpenAPI spec"
      fi
  done

  # Reverse: spec entries that have no handler (clients 404).
  # The planned_regex below is the explicit allow-list of
  # "documented but not yet shipped" — deliberately adjusted in
  # a docs PR when endpoints land or get cut. Empty today —
  # every spec path has a handler. If you add a new doc-but-stub
  # endpoint, add it here and remove it once the handler lands.
  planned_regex='^$'
  grep -oE "^  /[^:]+:" openapi/stellar-index.v1.yaml | \
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
        # CS-052: check both HandleFunc( and mux.Handle( registrations.
        if grep -qrF "HandleFunc(\"${method} /v1${route}\"" internal/api/v1/ 2>/dev/null \
          || grep -qrF "Handle(\"${method} /v1${route}\"" internal/api/v1/ 2>/dev/null; then
          found=1
          break
        fi
      done
      if [ "$found" -eq 0 ]; then
        err "OpenAPI path '$route' has no handler. Add a handler or add it to planned_regex in lint-docs.sh"
      fi
  done
fi

# ─── 2b. Generated API reference must be in sync with the source spec ────────
#
# `make docs-api` copies openapi/stellar-index.v1.yaml verbatim next to the
# rendered index.html. CI's `openapi` job enforces this too — but that job is
# PR-only + path-filtered, so a direct-to-main push that edits the spec
# without re-running `make docs-api` slips the desync onto main (observed
# once: the reference was 66 paths while the spec shipped 73). This lint
# runs inside verify.sh on every push, so the gap is closed locally
# regardless of the CI trigger.

echo "Checking generated API reference sync..."
if [ -f openapi/stellar-index.v1.yaml ] && [ -f docs/reference/api/stellar-index.v1.yaml ]; then
  if ! diff -q openapi/stellar-index.v1.yaml docs/reference/api/stellar-index.v1.yaml >/dev/null 2>&1; then
    err "docs/reference/api/stellar-index.v1.yaml is out of sync with openapi/stellar-index.v1.yaml — run 'make docs-api' and commit the result"
  fi
fi

# ─── 3. Every Prometheus metric must be documented in metrics reference ─────

echo "Checking metrics registry..."
if [ -d internal/obs ] && [ -f docs/reference/metrics/README.md ]; then
  # Every metric registered in internal/obs must appear in the
  # reference doc. Scope is all prometheus `Name: "..."` fields —
  # not just stellarindex_*/ctx_* — so `http_requests_total` and
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
  "stellarindex\.ctx\.io"         # old placeholder domain
  "ctx-indexer\|ctx-aggregator\|ctx-api\|ctx-ops\|ctx-migrate" # old binary names (we use stellarindex- prefix now — adjust if you change the policy)
  "CTX Rates"                    # old project name (now "Stellar Index")
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
    if [ "$status" = "Superseded" ] && { [ "$superseded_by" = "null" ] || [ -z "$superseded_by" ]; }; then
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
# the `stellarindex_ingestion_insert_errors` drift on 2026-04-23 —
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

# ─── 11. Runbook body references to `stellarindex_source_*` metrics ─────────
#
# Narrow rule: only `stellarindex_source_*` (the namespace fully
# owned by internal/obs/metrics.go). External-exporter metrics
# (stellarindex_stellar_core_*, pgbackrest_*, etc.) are intentionally
# out of scope — those live in node-side exporters we don't control.
#
# Caught `stellarindex_source_last_event_age_seconds` drift on
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
    (grep -hE 'Name:[[:space:]]*"stellarindex_source_[a-z_]+"' internal/obs/metrics.go 2>/dev/null || true) | \
      sed -E 's|.*"(stellarindex_source_[a-z_]+)".*|\1|'
    (grep -rhE '^[[:space:]]*-[[:space:]]*alert:[[:space:]]*stellarindex_source_' deploy/monitoring/rules/ 2>/dev/null || true) | \
      sed -E 's|.*alert:[[:space:]]*||'
  } | sort -u > "$allowed"

  # Extract every stellarindex_source_* token from runbook bodies.
  (grep -rhoE 'stellarindex_source_[a-z_]+' docs/operations/runbooks/ 2>/dev/null || true) | \
    sort -u | while IFS= read -r metric; do
      [ -z "$metric" ] && continue
      if ! grep -qxF "$metric" "$allowed"; then
        err "runbook references unknown metric '$metric' (not in internal/obs or rules/)"
      fi
    done
  rm -f "$allowed"
fi

# ─── 12. Every runbook referenced from alerts-catalog must exist ────────────
#
# Symmetric counterpart to §9 (which checks rule-file → runbook). The
# catalog is the operator-facing index; a stale `runbooks/X.md` link
# in it means oncall clicks through to a 404. Caught nothing yet —
# verified clean as of 2026-04-27 — but adding the check before the
# next runbook reorganisation introduces drift.

echo "Checking alerts-catalog runbook link freshness..."
if [ -f docs/operations/alerts-catalog.md ] && [ -d docs/operations/runbooks ]; then
  grep -oE 'runbooks/[a-z0-9-]+\.md' docs/operations/alerts-catalog.md | sort -u | while IFS= read -r path; do
    [ -z "$path" ] && continue
    if [ ! -f "docs/operations/$path" ]; then
      err "alerts-catalog references missing runbook: docs/operations/$path"
    fi
  done
fi

# ─── 13. Every operational runbook should be referenced ────────────────────
#
# Orphan runbooks are stale by definition — a runbook nobody can
# find isn't a runbook. Allow-list the four that intentionally
# stand alone (template, bring-up procedures, dead-man's switch).
# All other docs/operations/runbooks/*.md must appear in either
# alerts-catalog.md or sev-playbook.md or be cross-referenced from
# another runbook (chained-procedure case).

# ─── Prometheus rules: assert each multi-host rule has an R1 sibling ──
#
# Multi-host rules in `deploy/monitoring/rules/` use underscored job
# names (matching the ansible multi-host scrape config). R1's single-
# host overlay at `configs/prometheus/rules.r1/` mirrors the same
# alerts with hyphenated job names. Silent drift between the two —
# editing the multi-host file alone leaves R1 with a stale rule. This
# check flags any multi-host file that has no matching R1 sibling so
# reviewers catch the drift at CI time.

echo "Checking Prometheus rule pairing (multi-host ↔ R1 overlay)..."
if [ -d deploy/monitoring/rules ] && [ -d configs/prometheus/rules.r1 ]; then
  for r in deploy/monitoring/rules/*.yml; do
    fname="${r##*/}"
    if [ ! -f "configs/prometheus/rules.r1/$fname" ]; then
      err "Multi-host rule file $r has no configs/prometheus/rules.r1/$fname sibling. Either add an R1 overlay or remove this rule."
    fi
  done
fi

echo "Checking runbook orphans..."
if [ -d docs/operations/runbooks ]; then
  for r in docs/operations/runbooks/*.md; do
    fname="${r##*/}"
    case "$fname" in
      _template.md|README.md|bootstrap-archival-node.md|first-archival-node-deployment.md|deadmansswitch.md) continue ;;
    esac
    # Look for a reference in alerts-catalog, sev-playbook, or peer runbooks.
    if ! grep -qrF "runbooks/$fname" docs/operations/ 2>/dev/null; then
      err "orphan runbook with no referrer: $r — link from alerts-catalog, sev-playbook, or another runbook"
    fi
  done
fi

# ─── 14. Alert-runbook section presence ────────────────────────────────────
#
# Per the runbook template at docs/operations/runbooks/_template.md
# (wave 78 refresh + wave 81 normalisation), `## At a glance` and
# `## Related` are the two universally-required sections on every
# alert runbook. Without `## At a glance` an operator paged at
# 3 AM has to read the body prose to learn severity / MTTR /
# impact; without `## Related` the cross-link graph rots and
# adjacent runbooks become undiscoverable.
#
# Exclude procedural runbooks (bring-up, disaster recovery,
# SEV-comms procedures, one-off operator notes) — they're
# legitimately shaped differently. The allow-list mirrors the
# orphan-lint exclusions above plus three procedural runbooks
# (`dr-activation`, `sev-status-page-update`, the dated operator
# note) flagged as not-alert-shaped during the wave-81 survey.

echo "Checking alert-runbook section presence..."
if [ -d docs/operations/runbooks ]; then
  for r in docs/operations/runbooks/*.md; do
    fname="${r##*/}"
    case "$fname" in
      _template.md|README.md|bootstrap-archival-node.md|first-archival-node-deployment.md|deadmansswitch.md) continue ;;
      dr-activation.md|sev-status-page-update.md|operator-unblock-2026-05-08.md) continue ;;
    esac
    if ! grep -q "^## At a glance" "$r" 2>/dev/null; then
      err "runbook missing '## At a glance' section: $r — see docs/operations/runbooks/_template.md"
    fi
    if ! grep -q "^## Related" "$r" 2>/dev/null; then
      err "runbook missing '## Related' section: $r — see docs/operations/runbooks/_template.md"
    fi
  done
fi

# ─── 15. Incident post-mortem follow-up forcing function ──────────────────
#
# Fail if any user-facing incident (internal/incidents/data/*.md,
# served by /v1/incidents) is older than 30 days AND still has
# unchecked `[ ]` checkboxes in its body. Closes the meta-failure-
# mode where post-mortem action items rot indefinitely: the
# 2026-05-10 SEV-2 (redis-writes-blocked-disk-full) shipped with 4
# `[ ]` follow-ups and 17 days later the same cascade recurred
# (2026-05-26) with those follow-ups still unchecked. CI now
# enforces the cadence so a future post-mortem either gets its
# items closed within a month, or the unchecked items get
# explicitly rewritten as accepted-debt (`[~]` is treated as
# checked / acknowledged).
#
# Date is sourced from the filename slug `<YYYY-MM-DD>-<slug>.md`
# — matches the convention enforced by `internal/incidents`
# (frontmatter `started_at:` may be richer, but filename is the
# stable surface and the only thing this lint reads).

echo "Checking incident post-mortem follow-ups..."
INCIDENT_DIR="internal/incidents/data"
if [ -d "$INCIDENT_DIR" ]; then
  NOW_EPOCH=$(date -u +%s)
  THIRTY_DAYS_AGO=$((NOW_EPOCH - 30 * 86400))
  for incident in "$INCIDENT_DIR"/*.md; do
    [ -f "$incident" ] || continue
    fname="${incident##*/}"
    # Skip templates / underscored scratch files (mirrors
    # internal/incidents/incidents.go Load() behaviour).
    case "$fname" in
      _*) continue ;;
    esac
    # Extract YYYY-MM-DD from the slug; bail if no leading date.
    date_str=$(echo "$fname" | grep -oE '^[0-9]{4}-[0-9]{2}-[0-9]{2}' || true)
    [ -z "$date_str" ] && continue
    # BSD-first date parse (matches section 6 convention).
    incident_epoch=$(date -u -j -f "%Y-%m-%d" "$date_str" +%s 2>/dev/null || \
                     date -u -d "$date_str" +%s 2>/dev/null || echo "")
    [ -z "$incident_epoch" ] && continue
    if [ "$incident_epoch" -ge "$THIRTY_DAYS_AGO" ]; then
      # Inside the 30-day grace window — unchecked items still OK.
      continue
    fi
    # Count unchecked `[ ]` checkboxes (markdown task-list shape).
    # `[x]` / `[X]` / `[~]` are all treated as done/acknowledged.
    unchecked=$(grep -cE '^[[:space:]]*-[[:space:]]+\[ \]' "$incident" || true)
    if [ "$unchecked" -gt 0 ]; then
      err "incident '$incident' is older than 30 days and has $unchecked unchecked '[ ]' follow-up checkbox(es) — close them, mark as acknowledged with '[~]', or rewrite the action item."
    fi
  done
fi

# ─── 16. Production CSP must not permit http://localhost ──────────────────
#
# An earlier revision left `http://localhost:3000` in the Cloudflare
# Pages CSP `connect-src` of the explorer + status sites as a
# dev-convenience that leaked into production. The Next dev server
# doesn't apply _headers anyway, so the dev-build use case is moot;
# permitting localhost in prod CSP is pure config drift between dev and
# prod. This guard fails CI if it regresses.

for hf in web/explorer/public/_headers web/status/public/_headers; do
  if [ -f "$hf" ] && grep -qE 'Content-Security-Policy:.*localhost' "$hf"; then
    err "$hf permits 'localhost' in a Content-Security-Policy header — forbidden in production builds. Remove the localhost permit; dev work uses 'next dev' which doesn't read _headers."
  fi
done

# ─── Summary ────────────────────────────────────────────────────────────────

count=$(cat "$ERROR_FILE")
rm "$ERROR_FILE"

if [ "$count" -gt 0 ]; then
  echo ""
  echo "❌ Doc lint failed with $count error(s)."
  exit 1
fi
echo "✅ Doc lint passed."
