#!/usr/bin/env sh
set -eu

root="$(git rev-parse --show-toplevel)"
out="$root/docs/audit-2026-05-12-codex/inventory"

mkdir -p "$out"

git -C "$root" ls-files \
  ':!:docs/audit-2026-05-12-codex/**' \
  | sort \
  | awk 'BEGIN {
      OFS="\t";
      print "path","top_level","audit_unit","file_kind","status","evidence_refs","cross_refs","notes"
    }
    {
      path=$0;
      n=split(path, parts, "/");
      top=parts[1];
      if (n == 1) {
        unit=path;
      } else if (top == "cmd" && n >= 2) {
        unit=parts[1] "/" parts[2];
      } else if (top == "internal" && n >= 3) {
        unit=parts[1] "/" parts[2] "/" parts[3];
      } else if (top == "web" && n >= 2) {
        unit=parts[1] "/" parts[2];
      } else if (top == "configs" && n >= 2) {
        unit=parts[1] "/" parts[2];
      } else if (top == "deploy" && n >= 2) {
        unit=parts[1] "/" parts[2];
      } else if (top == "docs" && n >= 2) {
        unit=parts[1] "/" parts[2];
      } else if (top == "test" && n >= 2) {
        unit=parts[1] "/" parts[2];
      } else if (top == ".github" && n >= 2) {
        unit=parts[1] "/" parts[2];
      } else {
        unit=top;
      }
      kind="unknown";
      if (path ~ /_test\.go$/) kind="test";
      else if (path ~ /\.go$/) kind="runtime";
      else if (path ~ /\.tsx?$/) kind="frontend";
      else if (path ~ /\.sql$/) kind="migration_or_sql";
      else if (path ~ /\.(ya?ml|toml|cfg|conf|config\.js|config\.mjs)$/) kind="config";
      else if (path ~ /\.(md|txt)$/) kind="documentation";
      else if (path ~ /\.(sh)$/) kind="script";
      else if (path ~ /(Dockerfile)$/) kind="deploy";
      else if (path ~ /\.(json)$/) kind="fixture_or_config";
      else if (path ~ /\.(svg|png|jpg|jpeg|ico)$/) kind="asset";
      print path, top, unit, kind, "todo", "", "", "cold audit pending"
    }' > "$out/file-coverage.tsv"

{
  echo "# Area Counts"
  echo
  echo "Generated from \`git ls-files\` excluding \`docs/audit-2026-05-12-codex/**\`."
  echo
  echo "| Top-Level Area | Tracked Files |"
  echo "| --- | ---: |"
  git -C "$root" ls-files ':!:docs/audit-2026-05-12-codex/**' \
    | awk -F/ '{ count[$1]++ } END { for (area in count) print area "\t" count[area] }' \
    | sort \
    | awk -F '\t' '{ print "| `" $1 "` | " $2 " |" }'
} > "$out/area-counts.md"

{
  echo "# Repo Snapshot"
  echo
  echo "| Item | Value |"
  echo "| --- | --- |"
  printf '| Generated At | `%s` |\n' "$(date -u '+%Y-%m-%dT%H:%M:%SZ')"
  printf '| Commit | `%s` |\n' "$(git -C "$root" rev-parse HEAD)"
  printf '| Branch | `%s` |\n' "$(git -C "$root" rev-parse --abbrev-ref HEAD)"
  printf '| Tracked Files | `%s` |\n' "$(git -C "$root" ls-files ':!:docs/audit-2026-05-12-codex/**' | wc -l | tr -d ' ')"
  printf '| Dirty Files After Plan Creation | `%s` |\n' "$(git -C "$root" status --short | wc -l | tr -d ' ')"
  echo
  echo "## Dirty Worktree"
  echo
  git -C "$root" status --short
} > "$out/repo-snapshot.md"
