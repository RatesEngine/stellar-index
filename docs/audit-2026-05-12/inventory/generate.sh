#!/usr/bin/env bash
# Inventory generator for the 2026-05-12 cold + adversarial audit.
#
# Generates everything an auditor needs to walk the workspace:
#   - repo-snapshot.md          (commit, file/test/Go counts, dirty caveat)
#   - area-counts.md            (per-top-level file counts)
#   - file-coverage.tsv         (every tracked file, terminal-status placeholder)
#   - api-route-inventory.md    (every HTTP route registered in internal/api)
#   - migration-inventory.md    (every migration up/down with line counts)
#   - source-decoder-inventory.md (every internal/sources/* package)
#   - workflow-inventory.md     (every .github/workflows/*.yml + trigger)
#   - runbook-inventory.md      (every runbook + matching alert rule)
#   - adr-inventory.md          (every ADR + status field)
#   - external-source-inventory.md (every internal/sources/external/* adapter)
#   - alert-rule-inventory.md   (every Prometheus rule)
#   - metric-name-inventory.md  (every metric name registered by code)
#   - dependency-inventory.md   (go.mod direct deps + pkg/client deps + web pnpm deps)
#   - docker-systemd-inventory.md (Dockerfiles + systemd units)
#   - webfrontend-inventory.md  (web/{explorer,dashboard,status} pages)

set -uo pipefail

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
repo_root="$(cd "${script_dir}/../../.." && pwd)"
out_dir="${script_dir}"

cd "${repo_root}"

commit_sha="$(git rev-parse HEAD)"
status_short="$(git status --short)"

if [[ -z "${status_short}" ]]; then
  worktree_state="clean"
else
  worktree_state="dirty"
fi

if command -v rg >/dev/null 2>&1; then
  GREP_RECURSIVE() { rg -nN --no-heading "$@"; }
  GREP_FILES() { rg --files --hidden "$@"; }
else
  GREP_RECURSIVE() { grep -RnE --no-filename "$@"; }
  GREP_FILES() {
    # very-rough fallback for `rg --files`
    local pattern="*" include_root="."
    while [[ $# -gt 0 ]]; do
      case "$1" in
        -g) shift; pattern="$1"; shift ;;
        --hidden) shift ;;
        *) include_root="$1"; shift ;;
      esac
    done
    find . -type f -name "${pattern##*/}" -not -path './.git/*'
  }
fi

tracked_files="$(git ls-files | sort)"
file_count="$(printf '%s\n' "${tracked_files}" | wc -l | tr -d ' ')"
go_file_count="$(printf '%s\n' "${tracked_files}" | grep -c '\.go$' || true)"
test_file_count="$(printf '%s\n' "${tracked_files}" | grep -c '_test\.go$' || true)"
doc_file_count="$(find docs -type f | wc -l | tr -d ' ')"
sql_migration_count="$(find migrations -maxdepth 1 -type f -name '*.up.sql' | wc -l | tr -d ' ')"
workflow_count="$(find .github/workflows -maxdepth 1 -type f | wc -l | tr -d ' ')"
cmd_count="$(find cmd -maxdepth 1 -mindepth 1 -type d | wc -l | tr -d ' ')"
source_dir_count="$(find internal/sources -maxdepth 1 -mindepth 1 -type d | wc -l | tr -d ' ')"
external_source_dir_count="$(find internal/sources/external -maxdepth 1 -mindepth 1 -type d 2>/dev/null | wc -l | tr -d ' ')"
adr_count="$(find docs/adr -maxdepth 1 -name '0*.md' | wc -l | tr -d ' ')"
runbook_count="$(find docs/operations/runbooks -maxdepth 1 -name '*.md' -not -name '_*' | wc -l | tr -d ' ')"
alert_rule_file_count="$(find deploy/monitoring/rules -maxdepth 1 -type f | wc -l | tr -d ' ')"
go_pkg_count="$(go list ./... 2>/dev/null | wc -l | tr -d ' ')"

# repo-snapshot.md
{
  printf '# Repo Snapshot\n\n'
  printf -- '- Audit date: `2026-05-12`\n'
  printf -- '- Commit SHA: `%s`\n' "${commit_sha}"
  printf -- '- Worktree state: `%s`\n' "${worktree_state}"
  printf -- '- Tracked files: `%s`\n' "${file_count}"
  printf -- '- Go files (excl discovery + node_modules): `%s`\n' "${go_file_count}"
  printf -- '- Test files: `%s`\n' "${test_file_count}"
  printf -- '- Docs files under `docs/`: `%s`\n' "${doc_file_count}"
  printf -- '- SQL up-migrations: `%s`\n' "${sql_migration_count}"
  printf -- '- Workflow files: `%s`\n' "${workflow_count}"
  printf -- '- Runtime/ops binaries: `%s`\n' "${cmd_count}"
  printf -- '- Source-family directories: `%s`\n' "${source_dir_count}"
  printf -- '- External adapter directories: `%s`\n' "${external_source_dir_count}"
  printf -- '- ADRs: `%s`\n' "${adr_count}"
  printf -- '- Runbooks: `%s`\n' "${runbook_count}"
  printf -- '- Alert-rule files: `%s`\n' "${alert_rule_file_count}"
  printf -- '- Go packages: `%s`\n' "${go_pkg_count}"
  printf '\n## Dirty Worktree Detail\n\n'
  if [[ -z "${status_short}" ]]; then
    printf 'Worktree is clean at generation time.\n'
  else
    printf '```text\n%s\n```\n' "${status_short}"
  fi
  printf '\n## Scope Notes\n\n'
  printf -- '- Generated from the local checkout only.\n'
  printf -- '- Live R1 state is captured separately under `evidence/r1-probes/`.\n'
  printf -- '- Local markdown is not accepted as fact until reconciled against code.\n'
  printf -- '- Hosted GitHub settings, Cloudflare, GHCR, Healthchecks.io, Stripe, Resend are excluded by default — see `06-exclusions-register.md`.\n'
} > "${out_dir}/repo-snapshot.md"

# area-counts.md
{
  printf '# Area Counts\n\n'
  printf '| Area | File count |\n'
  printf '| --- | ---: |\n'
  printf '%s\n' "${tracked_files}" | awk -F/ '
    {
      top = ($0 ~ /\//) ? $1 : "(root)"
      counts[top]++
    }
    END {
      for (k in counts) {
        printf("| `%s` | %d |\n", k, counts[k])
      }
    }
  ' | sort
} > "${out_dir}/area-counts.md"

# file-coverage.tsv (v2 — adds file_kind column, supports W26 cross-file roll-up)
# Preserve user-set status / evidence_refs / cross_refs / notes from any
# existing TSV; only regenerate the auto-derived columns (path, top_level,
# audit_unit, file_kind, workstream).
if [[ -f "${out_dir}/file-coverage.tsv" ]]; then
  existing_status="${out_dir}/.file-coverage.preserved.tsv"
  # Map: path -> "status\tevidence_refs\tcross_refs\tnotes" using a delimiter
  # safe across paths.
  awk -F'\t' 'NR>1 {printf "%s\x1e%s\t%s\t%s\t%s\n", $1, $6, $7, $8, $9}' \
    "${out_dir}/file-coverage.tsv" > "${existing_status}"
fi

{
  printf 'path\ttop_level\taudit_unit\tfile_kind\tworkstream\tstatus\tevidence_refs\tcross_refs\tnotes\n'
  printf '%s\n' "${tracked_files}" | awk -F/ '
    {
      path = $0
      if (NF == 1) {
        top = "(root)"; unit = $1
      } else if ($1 == "internal" && NF >= 3) {
        top = $1; unit = $1 "/" $2 "/" $3
      } else if (($1 == "cmd" || $1 == "docs" || $1 == "deploy" || $1 == "configs" || $1 == "test" || $1 == ".github" || $1 == "scripts" || $1 == "web" || $1 == "docker" || $1 == "openapi" || $1 == "pkg" || $1 == "migrations" || $1 == "examples") && NF >= 2) {
        top = $1; unit = $1 "/" $2
      } else {
        top = $1; unit = $1 "/" $2
      }

      # file_kind classification (matches codex 14-value taxonomy)
      kind = "unknown"
      if (path ~ /_test\.go$/) kind = "test"
      else if (path ~ /\.go$/) kind = "runtime"
      else if (path ~ /\.tsx?$/ || path ~ /\.jsx?$/) kind = "frontend"
      else if (path ~ /\.sql$/) kind = "migration"
      else if (path ~ /^docker\//) kind = "deploy"
      else if (path ~ /^deploy\//) kind = "deploy"
      else if (path ~ /^configs\//) kind = "config"
      else if (path ~ /^\.github\/workflows\//) kind = "workflow"
      else if (path ~ /^\.github\//) kind = "policy"
      else if (path ~ /^scripts\//) kind = "script"
      else if (path ~ /\.(ya?ml|toml|cfg|conf|json)$/) kind = "config"
      else if (path ~ /\.(md|txt)$/) kind = "documentation"
      else if (path ~ /\.sh$/) kind = "script"
      else if (path ~ /\/fixtures\//) kind = "fixture"
      else if (path ~ /(\.svg|\.png|\.jpg|\.jpeg|\.ico|\.webp|\.woff2?|\.ttf)$/) kind = "asset"
      else if (path ~ /(go\.sum|pnpm-lock\.yaml)$/) kind = "generated"
      else if (path ~ /^docs\/reference\//) kind = "generated"
      else if (path ~ /^examples\/postman\//) kind = "generated"
      else if (path ~ /^(LICENSE|CODEOWNERS|CODE_OF_CONDUCT|SECURITY|CONTRIBUTING)$/) kind = "policy"

      # workstream assignment
      ws = ""
      if (top == ".github") ws = "W03"
      else if (top == "scripts") ws = "W03"
      else if (top == "docker") ws = "W18"
      else if (top == "deploy" && unit ~ /monitoring/) ws = "W14"
      else if (top == "deploy") ws = "W18"
      else if (top == "configs" && unit ~ /alertmanager|prometheus|loki|healthchecks/) ws = "W14"
      else if (top == "configs") ws = "W05"
      else if (top == "migrations") ws = "W09"
      else if (top == "openapi") ws = "W11"
      else if (top == "pkg") ws = "W11"
      else if (top == "web") ws = "W17"
      else if (top == "examples") ws = "W11"
      else if (top == "test") ws = "W15"
      else if (top == "docs") ws = "W16"
      else if (top == "cmd") ws = "W13"
      else if (top == "(root)") ws = "W01"
      else if (top == "internal") {
        if (unit ~ /^internal\/sources\/external/) ws = "W08"
        else if (unit ~ /^internal\/sources\/(reflector|redstone|band|frankfurter|forex)/) ws = "W10"
        else if (unit ~ /^internal\/sources\/(accounts|trustlines|claimable_balances|sac_balances|sep41_supply)/) ws = "W09"
        else if (unit ~ /^internal\/sources/) ws = "W07"
        else if (unit ~ /^internal\/canonical/) ws = "W05"
        else if (unit ~ /^internal\/(currency|scval|cachekeys|events)/) ws = "W05"
        else if (unit ~ /^internal\/(ledgerstream|dispatcher|pipeline|hashdb|archivecompleteness|consumer)/) ws = "W06"
        else if (unit ~ /^internal\/(aggregate|divergence)/) ws = "W10"
        else if (unit ~ /^internal\/api\/streaming/) ws = "W13"
        else if (unit ~ /^internal\/api\/streampublish/) ws = "W13"
        else if (unit ~ /^internal\/api/) ws = "W11"
        else if (unit ~ /^internal\/(supply|metadata|incidents)/) ws = "W12"
        else if (unit ~ /^internal\/storage\/redisclient/) ws = "W13"
        else if (unit ~ /^internal\/storage/) ws = "W09"
        else if (unit ~ /^internal\/(auth|platform|usage|notify|ratelimit)/) ws = "W19"
        else if (unit ~ /^internal\/obs/) ws = "W14"
        else if (unit ~ /^internal\/(stellarrpc|version|config)/) ws = "W02"
        else ws = "W02"
      } else ws = "W02"

      printf("%s\t%s\t%s\t%s\t%s\ttodo\t\t\t\n", path, top, unit, kind, ws)
    }
  '
} > "${out_dir}/file-coverage.tsv.new"

# Merge preserved user-set status back in (path → status\tevi\txrefs\tnotes).
if [[ -f "${existing_status:-}" ]]; then
  awk -F'\t' -v OFS='\t' '
    NR==FNR {
      # Reading preserved file; key by path
      n = split($1, parts, "\x1e")
      preserved[parts[1]] = parts[2] "\t" $2 "\t" $3 "\t" $4
      next
    }
    {
      if (NR == 1) { print; next }   # header
      key = $1
      if (key in preserved) {
        split(preserved[key], p, "\t")
        if (p[1] != "" && p[1] != "todo") $6 = p[1]
        if (p[2] != "") $7 = p[2]
        if (p[3] != "") $8 = p[3]
        if (p[4] != "") $9 = p[4]
      }
      print
    }
  ' "${existing_status}" "${out_dir}/file-coverage.tsv.new" > "${out_dir}/file-coverage.tsv"
  rm -f "${existing_status}" "${out_dir}/file-coverage.tsv.new"
else
  mv "${out_dir}/file-coverage.tsv.new" "${out_dir}/file-coverage.tsv"
fi

# api-route-inventory.md
{
  printf '# API Route Inventory\n\n'
  printf 'Auto-extracted from `internal/api/v1/`. Reconcile each route against `openapi/rates-engine.v1.yaml` and against the live R1 surface.\n\n'
  printf '| Method | Path | Source file | OpenAPI | Auth | Cache | Notes |\n'
  printf '| --- | --- | --- | --- | --- | --- | --- |\n'
  grep -RnE --include='*.go' -e 'mux\.(Handle|HandleFunc)\(' -e 'r\.(Get|Post|Put|Patch|Delete|Method)\(' -e '\.HandleFunc\(' internal/api 2>/dev/null \
    | awk -F: '{ printf("| | | %s:%s | | | | %s |\n", $1, $2, substr($0, index($0,$3))) }' \
    | head -200
  printf '\n_Reviewer: dedupe, fill Method/Path columns from the source code, and add a row in `04-reconciliation.md` R04 for every route._\n'
} > "${out_dir}/api-route-inventory.md"

# migration-inventory.md
{
  printf '# Migration Inventory\n\n'
  printf '| # | Up file | Up bytes | Down file | Down bytes | Audit unit | Status |\n'
  printf '| --- | --- | ---: | --- | ---: | --- | --- |\n'
  for up in $(ls migrations/*.up.sql | sort); do
    base="${up%.up.sql}"
    down="${base}.down.sql"
    name="$(basename "${up}" .up.sql)"
    up_bytes="$(wc -c <"${up}" | tr -d ' ')"
    if [[ -f "${down}" ]]; then
      down_bytes="$(wc -c <"${down}" | tr -d ' ')"
      down_path="${down}"
    else
      down_bytes="MISSING"
      down_path="—"
    fi
    printf '| %s | `%s` | %s | `%s` | %s | %s | todo |\n' "${name%%_*}" "${up}" "${up_bytes}" "${down_path}" "${down_bytes}" "${name#*_}"
  done
  printf '\n_Reviewer: per W09 protocol §7, walk every up + down sequentially._\n'
} > "${out_dir}/migration-inventory.md"

# source-decoder-inventory.md
{
  printf '# On-Chain Source Decoder Inventory\n\n'
  printf 'For every package under `internal/sources/` that is not under `external/`, capture file count and audit status.\n\n'
  printf '| Package | Files | Test files | BackfillSafe | WASM audit | Status |\n'
  printf '| --- | ---: | ---: | --- | --- | --- |\n'
  for d in internal/sources/*/; do
    name="$(basename "${d}")"
    if [[ "${name}" == "external" ]]; then continue; fi
    files="$(find "${d}" -maxdepth 1 -type f -name '*.go' | wc -l | tr -d ' ')"
    tests="$(find "${d}" -maxdepth 1 -type f -name '*_test.go' | wc -l | tr -d ' ')"
    bsafe='_unknown_'
    if grep -qE "Name:\\s*\"${name}\".*BackfillSafe" internal/sources/external/registry.go 2>/dev/null; then
      bsafe='_in registry — verify_'
    fi
    audit_doc="docs/operations/wasm-audits/${name}.md"
    if [[ -f "${audit_doc}" ]]; then
      audit='exists'
    else
      audit='—'
    fi
    printf '| `%s` | %s | %s | %s | %s | todo |\n' "${name}" "${files}" "${tests}" "${bsafe}" "${audit}"
  done
} > "${out_dir}/source-decoder-inventory.md"

# external-source-inventory.md
{
  printf '# External Source (CEX/FX/aggregator/oracle) Adapter Inventory\n\n'
  printf '| Adapter | Files | Test files | Class (per registry) | Notes |\n'
  printf '| --- | ---: | ---: | --- | --- |\n'
  for d in internal/sources/external/*/; do
    name="$(basename "${d}")"
    files="$(find "${d}" -maxdepth 1 -type f -name '*.go' | wc -l | tr -d ' ')"
    tests="$(find "${d}" -maxdepth 1 -type f -name '*_test.go' | wc -l | tr -d ' ')"
    printf '| `%s` | %s | %s | _verify in registry.go_ | todo |\n' "${name}" "${files}" "${tests}"
  done
  printf '\n## Sibling adapters NOT under external/\n\n'
  printf '| Path | Files | Test files |\n'
  printf '| --- | ---: | ---: |\n'
  for d in internal/sources/forex internal/sources/frankfurter; do
    name="$(basename "${d}")"
    files="$(find "${d}" -maxdepth 1 -type f -name '*.go' 2>/dev/null | wc -l | tr -d ' ')"
    tests="$(find "${d}" -maxdepth 1 -type f -name '*_test.go' 2>/dev/null | wc -l | tr -d ' ')"
    printf '| `%s` | %s | %s |\n' "${d}" "${files}" "${tests}"
  done
} > "${out_dir}/external-source-inventory.md"

# workflow-inventory.md
{
  printf '# CI/CD Workflow Inventory\n\n'
  printf '| Workflow | Triggers | Jobs | Notes |\n'
  printf '| --- | --- | --- | --- |\n'
  for f in .github/workflows/*.yml; do
    name="$(basename "${f}")"
    triggers="$(grep -E '^on:|^  pull_request:|^  push:|^  workflow_dispatch|^  schedule:|^  release:' "${f}" | tr '\n' ',' | sed 's/,$//')"
    jobs="$(grep -E '^[a-zA-Z][a-zA-Z0-9_-]*:$' "${f}" | head -20 | tr '\n' ' ')"
    printf '| `%s` | %s | %s | todo |\n' "${name}" "${triggers}" "${jobs}"
  done
} > "${out_dir}/workflow-inventory.md"

# runbook-inventory.md
{
  printf '# Runbook Inventory\n\n'
  printf 'Every alert rule in `deploy/monitoring/rules/*.yml` should map to a runbook here.\n\n'
  printf '| Runbook | Bytes | Matching alert(s) | Status |\n'
  printf '| --- | ---: | --- | --- |\n'
  for f in $(ls docs/operations/runbooks/*.md | sort); do
    name="$(basename "${f}" .md)"
    if [[ "${name}" == "_template" ]]; then continue; fi
    bytes="$(wc -c <"${f}" | tr -d ' ')"
    matches="$(grep -l "alert: ${name}" deploy/monitoring/rules/*.yml 2>/dev/null | xargs -I{} basename {} | tr '\n' ',' | sed 's/,$//')"
    printf '| `%s` | %s | %s | todo |\n' "${name}" "${bytes}" "${matches}"
  done
} > "${out_dir}/runbook-inventory.md"

# adr-inventory.md
{
  printf '# ADR Inventory\n\n'
  printf '| ADR | Title | Status | Implementation surface | Reconciled (R06) |\n'
  printf '| --- | --- | --- | --- | --- |\n'
  for f in $(ls docs/adr/0*.md | sort); do
    name="$(basename "${f}" .md)"
    status="$(grep -E '^Status:|^status:' "${f}" | head -1 | sed 's/^[Ss]tatus: *//')"
    title="$(grep -E '^title:' "${f}" | head -1 | sed 's/^title: *//')"
    printf '| `%s` | %s | %s | _todo_ | todo |\n' "${name}" "${title}" "${status}"
  done
} > "${out_dir}/adr-inventory.md"

# alert-rule-inventory.md
{
  printf '# Alert Rule Inventory\n\n'
  printf '| File | Alert name | Severity | Has runbook | Has matching metric | Status |\n'
  printf '| --- | --- | --- | --- | --- | --- |\n'
  for f in deploy/monitoring/rules/*.yml; do
    fname="$(basename "${f}")"
    grep -E '^[[:space:]]*- alert:' "${f}" 2>/dev/null \
      | sed -E 's/^[[:space:]-]*alert: *//' \
      | while read -r alert; do
          if [[ -f "docs/operations/runbooks/${alert}.md" ]]; then
            rb="yes"
          else
            rb="no"
          fi
          printf '| `%s` | `%s` | _verify_ | %s | _verify_ | todo |\n' "${fname}" "${alert}" "${rb}"
        done
  done
} > "${out_dir}/alert-rule-inventory.md"

# metric-name-inventory.md
{
  printf '# Metric-Name Inventory\n\n'
  printf 'Auto-extracted from struct fields named `Name:` inside `internal/obs/` (and elsewhere where prom metrics are registered).\n\n'
  printf '| Metric Name | File | Type (counter/gauge/hist) | Notes |\n'
  printf '| --- | --- | --- | --- |\n'
  grep -RnE --include='*.go' 'Name:[[:space:]]*"[a-z][a-z0-9_]+"' internal/ 2>/dev/null | head -200 | \
    awk -F: '{ printf("| %s | %s:%s | _verify_ | todo |\n", $3, $1, $2) }'
} > "${out_dir}/metric-name-inventory.md"

# dependency-inventory.md
{
  printf '# Dependency Inventory\n\n'
  printf '## Go modules (`go.mod` direct + indirect)\n\n'
  printf 'Note: this lists both direct and `// indirect` deps. The audit unit per W04 is the *direct* set + the transitive surface visible to go-mod-verify; both are tabulated together for completeness.\n\n'
  printf '| Module | Version | Notes |\n'
  printf '| --- | --- | --- |\n'
  # Direct deps: lines inside the require ( … ) block that have 2 non-comment tokens
  awk '/^require[[:space:]]*\(/{flag=1;next} flag && /^\)/{flag=0} flag {
    # Strip trailing // comment
    sub(/\/\/.*/, "")
    # Trim whitespace
    gsub(/^[[:space:]]+|[[:space:]]+$/, "")
    if ($0 == "") next
    # Now expect "<module> <version>"
    n = split($0, parts, /[[:space:]]+/)
    if (n >= 2) print "| `"parts[1]"` | `"parts[2]"` | todo |"
  }' go.mod | head -200
  printf '\n## pnpm lockfile dependencies (web/explorer, web/dashboard, web/status)\n\n'
  for d in web/explorer web/dashboard web/status; do
    printf '\n### %s\n\n' "${d}"
    if [[ -f "${d}/package.json" ]]; then
      jq -r '. as $p | (($p.dependencies // {}) + ($p.devDependencies // {})) | to_entries[] | "| `\(.key)` | `\(.value)` |"' "${d}/package.json" 2>/dev/null \
        || cat "${d}/package.json" | head -50
    else
      printf '_no package.json found_\n'
    fi
  done
  printf '\n## VERSIONS.md pinned upstream SHAs\n\n'
  if [[ -f VERSIONS.md ]]; then
    printf 'See `VERSIONS.md` for hand-pinned upstream commit hashes audited per W04.\n'
  fi
} > "${out_dir}/dependency-inventory.md"

# docker-systemd-inventory.md
{
  printf '# Docker + systemd Inventory\n\n'
  printf '## Dockerfiles\n\n'
  printf '| File | Bytes | Base image (FROM) | USER | HEALTHCHECK | Status |\n'
  printf '| --- | ---: | --- | --- | --- | --- |\n'
  for f in docker/*.Dockerfile; do
    bytes="$(wc -c <"${f}" | tr -d ' ')"
    base="$(grep -E '^FROM ' "${f}" | tr '\n' '|' | sed 's/|$//')"
    user="$(grep -E '^USER ' "${f}" | tr '\n' '|' | sed 's/|$//')"
    hc="$(grep -E '^HEALTHCHECK ' "${f}" | tr '\n' '|' | sed 's/|$//')"
    [[ -z "${user}" ]] && user='—'
    [[ -z "${hc}" ]] && hc='—'
    printf '| `%s` | %s | `%s` | `%s` | `%s` | todo |\n' "${f}" "${bytes}" "${base}" "${user}" "${hc}"
  done
  printf '\n## systemd units\n\n'
  printf '| File | Type | Notes |\n'
  printf '| --- | --- | --- |\n'
  for f in deploy/systemd/*; do
    type="${f##*.}"
    printf '| `%s` | %s | todo |\n' "${f}" "${type}"
  done
} > "${out_dir}/docker-systemd-inventory.md"

# webfrontend-inventory.md
{
  printf '# Web Frontend Inventory\n\n'
  for d in web/explorer web/dashboard web/status; do
    printf '\n## %s\n\n' "${d}"
    if [[ ! -d "${d}/src" ]]; then
      printf '_no src directory_\n'
      continue
    fi
    printf '| Page | Notes |\n'
    printf '| --- | --- |\n'
    find "${d}/src" -type f \( -name 'page.tsx' -o -name 'page.jsx' -o -name 'page.ts' -o -name 'page.js' \) | sort | \
      while read -r p; do
        printf '| `%s` | todo |\n' "${p}"
      done
  done
} > "${out_dir}/webfrontend-inventory.md"

echo "inventory regenerated under ${out_dir}/"
