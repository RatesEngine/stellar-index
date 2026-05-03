#!/usr/bin/env bash
set -euo pipefail

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

tracked_files="$(git ls-files | sort)"
file_count="$(printf '%s\n' "${tracked_files}" | wc -l | tr -d ' ')"
go_file_count="$(rg --files --hidden -g '*.go' | wc -l | tr -d ' ')"
test_file_count="$(rg --files --hidden -g '*_test.go' | wc -l | tr -d ' ')"
doc_file_count="$(find docs -type f | wc -l | tr -d ' ')"
sql_migration_count="$(find migrations -maxdepth 1 -type f -name '*.sql' | wc -l | tr -d ' ')"
workflow_count="$(find .github/workflows -maxdepth 1 -type f | wc -l | tr -d ' ')"
cmd_count="$(find cmd -maxdepth 1 -mindepth 1 -type d | wc -l | tr -d ' ')"
source_dir_count="$(find internal/sources -maxdepth 1 -mindepth 1 -type d | wc -l | tr -d ' ')"
go_pkg_count="$(go list ./... 2>/dev/null | wc -l | tr -d ' ')"

{
  printf '# Repo Snapshot\n\n'
  printf -- '- Audit date: `2026-05-02`\n'
  printf -- '- Commit SHA: `%s`\n' "${commit_sha}"
  printf -- '- Worktree state: `%s`\n' "${worktree_state}"
  printf -- '- Tracked files: `%s`\n' "${file_count}"
  printf -- '- Go files: `%s`\n' "${go_file_count}"
  printf -- '- Test files: `%s`\n' "${test_file_count}"
  printf -- '- Docs files under `docs/`: `%s`\n' "${doc_file_count}"
  printf -- '- SQL migration files: `%s`\n' "${sql_migration_count}"
  printf -- '- Workflow files: `%s`\n' "${workflow_count}"
  printf -- '- Runtime/ops binaries: `%s`\n' "${cmd_count}"
  printf -- '- Source-family directories: `%s`\n' "${source_dir_count}"
  printf -- '- Go packages: `%s`\n' "${go_pkg_count}"
  printf '\n## Dirty Worktree Detail\n\n'
  if [[ -z "${status_short}" ]]; then
    printf 'Worktree is clean at generation time.\n'
  else
    printf '```text\n%s\n```\n' "${status_short}"
  fi
  printf '\n## Scope Notes\n\n'
  printf -- '- Generated from the local checkout only.\n'
  printf -- '- Local markdown is not accepted as fact until reconciled against code.\n'
  printf -- '- Hosted GitHub settings and live third-party services are not proven by this artifact.\n'
} > "${out_dir}/repo-snapshot.md"

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

{
  printf 'path\ttop_level\taudit_unit\tstatus\tevidence_refs\tcross_refs\tnotes\n'
  printf '%s\n' "${tracked_files}" | awk -F/ '
    {
      if (NF == 1) {
        top = "(root)"
        unit = $1
      } else if ($1 == "internal" && NF >= 3) {
        top = $1
        unit = $1 "/" $2 "/" $3
      } else if (($1 == "cmd" || $1 == "docs" || $1 == "deploy" || $1 == "configs" || $1 == "test" || $1 == ".github" || $1 == "scripts") && NF >= 2) {
        top = $1
        unit = $1 "/" $2
      } else {
        top = $1
        unit = $1 "/" $2
      }
      printf("%s\t%s\t%s\ttodo\t\t\t\n", $0, top, unit)
    }
  '
} > "${out_dir}/file-coverage.tsv"
