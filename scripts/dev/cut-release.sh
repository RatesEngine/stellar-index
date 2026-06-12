#!/usr/bin/env bash
# cut-release.sh — guard-rail script for cutting a SemVer binary release.
#
# Usage:
#   scripts/dev/cut-release.sh vX.Y.Z [--dry-run]
#
# What it does (in order):
#   1. Validate the supplied tag matches SemVer (vX.Y.Z[-pre][+build])
#   2. Verify we're on `main` and the working tree is clean
#   3. Verify `git pull --ff-only origin main` is a no-op (we're up to date)
#   4. Verify the tag doesn't already exist locally or on origin
#   5. Verify CHANGELOG.md has a `## [vX.Y.Z] — YYYY-MM-DD` section
#      that is non-empty (release.yml's auto-notes extraction reads
#      this; an empty section produces an empty release page)
#   6. Run `bash scripts/dev/verify.sh` (the standard pre-push gate)
#   7. Echo what would happen — then either bail (--dry-run) or:
#      a. `git tag <tag>`
#      b. `git push origin <tag>`
#
# release.yml fires on the tag push and produces the GitHub Release +
# container images. See docs/operations/release-process.md for the
# full runbook this script implements.

set -euo pipefail

TAG="${1:-}"
DRY_RUN="${2:-}"

if [[ -z "$TAG" ]]; then
  echo "Usage: $0 vX.Y.Z [--dry-run]" >&2
  exit 2
fi

# Step 1 — tag shape
if ! [[ "$TAG" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?(\+[A-Za-z0-9.-]+)?$ ]]; then
  echo "ERR: '$TAG' does not match SemVer vX.Y.Z[-prerelease][+build]" >&2
  exit 1
fi

cd "$(git rev-parse --show-toplevel)"

# Step 2 — branch + clean tree
branch="$(git branch --show-current)"
if [[ "$branch" != "main" ]]; then
  echo "ERR: not on main (currently on '$branch'). Switch with 'git checkout main' first." >&2
  exit 1
fi
if [[ -n "$(git status --porcelain)" ]]; then
  echo "ERR: working tree has uncommitted changes:" >&2
  git status --short >&2
  exit 1
fi

# Step 3 — up to date with origin. Use the gh API to read the
# remote tip rather than `git fetch` so the script works against
# both SSH-keyed and HTTPS-credential-via-gh setups (some envs
# carry gh creds but no SSH key, and vice versa).
local_sha=$(git rev-parse main)
if command -v gh >/dev/null 2>&1; then
  remote_sha=$(gh api repos/{owner}/{repo}/commits/main --jq '.sha' 2>/dev/null || true)
  if [[ -z "$remote_sha" ]]; then
    echo "WARN: could not read origin/main via gh; falling back to git fetch" >&2
    git fetch --quiet origin main 2>/dev/null || true
    remote_sha=$(git rev-parse origin/main 2>/dev/null || echo unknown)
  fi
else
  git fetch --quiet origin main
  remote_sha=$(git rev-parse origin/main)
fi
if [[ "$local_sha" != "$remote_sha" ]]; then
  echo "ERR: main is not in sync with origin/main:" >&2
  echo "  local:  $local_sha" >&2
  echo "  remote: $remote_sha" >&2
  echo "Run: git pull --ff-only origin main" >&2
  exit 1
fi

# Step 4 — tag doesn't already exist
if git rev-parse "$TAG" >/dev/null 2>&1; then
  echo "ERR: tag '$TAG' already exists locally" >&2
  exit 1
fi
# Use the gh API for the remote-tag-exists check too, for the
# same SSH-vs-HTTPS reason as step 3.
if command -v gh >/dev/null 2>&1; then
  if gh api "repos/{owner}/{repo}/git/ref/tags/$TAG" >/dev/null 2>&1; then
    echo "ERR: tag '$TAG' already exists on origin" >&2
    exit 1
  fi
elif git ls-remote --exit-code --tags origin "refs/tags/$TAG" >/dev/null 2>&1; then
  echo "ERR: tag '$TAG' already exists on origin" >&2
  exit 1
fi

# Step 5 — CHANGELOG section exists and is non-empty.
# Uses awk's index() instead of $0 ~ regex so dot/dash chars in
# the tag (e.g. v0.5.0-rc.1) don't collapse to a regex char class.
changelog_section=$(awk -v marker="## [$TAG]" '
  index($0, marker) == 1 { capture = 1; next }
  capture && /^## \[/    { exit }
  capture                { print }
' CHANGELOG.md)
if [[ -z "$(printf '%s' "$changelog_section" | tr -d '[:space:]')" ]]; then
  echo "ERR: no non-empty '## [$TAG] — YYYY-MM-DD' section in CHANGELOG.md" >&2
  echo "" >&2
  echo "Cut a release-prep PR first that:" >&2
  echo "  1. Replaces '## [Unreleased]' with '## [$TAG] — $(date -u +%Y-%m-%d)'" >&2
  echo "  2. Adds a fresh '## [Unreleased]' block above it" >&2
  echo "" >&2
  echo "See docs/operations/release-process.md §Cut step 2." >&2
  exit 1
fi

# Step 6 — verify gate
echo "→ Running scripts/dev/verify.sh (this can take a minute)..."
if ! bash scripts/dev/verify.sh >/dev/null 2>&1; then
  echo "ERR: scripts/dev/verify.sh FAILED. Re-run it directly to see the failures." >&2
  exit 1
fi
echo "  OK"

# Step 7 — go / no-go
echo ""
echo "Ready to cut release $TAG"
echo "  branch:        $branch ($local_sha)"
echo "  tag:           $TAG"
echo "  CHANGELOG:     $(printf '%s' "$changelog_section" | head -3 | sed 's/^/    /')"
echo ""

if [[ "$DRY_RUN" == "--dry-run" ]]; then
  echo "[--dry-run] would run:"
  echo "  git tag $TAG"
  echo "  git push origin $TAG"
  echo ""
  echo "Then release.yml would fire and produce a GitHub Release at:"
  echo "  https://github.com/StellarAtlas/stellar-atlas/releases/tag/$TAG"
  exit 0
fi

read -r -p "Proceed with tag + push? [y/N] " ans
if [[ "$ans" != "y" && "$ans" != "Y" ]]; then
  echo "Aborted."
  exit 1
fi

git tag "$TAG"
# Resolve origin's URL so the push works whether origin is SSH-keyed,
# HTTPS-via-gh-credentials, or some mix. `git push origin $TAG`
# would also work in most setups, but using the explicit URL skips
# the ambient SSH-key requirement on hosts that only have gh creds.
origin_url=$(git remote get-url origin 2>/dev/null || echo "")
case "$origin_url" in
  git@github.com:*)
    https_url="https://github.com/${origin_url#git@github.com:}"
    git push "$https_url" "$TAG"
    ;;
  https://*)
    git push "$origin_url" "$TAG"
    ;;
  *)
    git push origin "$TAG"
    ;;
esac

echo ""
echo "Tag $TAG pushed. release.yml will fire shortly."
echo "Track progress: gh run list --workflow release.yml --limit 5"
echo "Release page:   https://github.com/StellarAtlas/stellar-atlas/releases/tag/$TAG"
