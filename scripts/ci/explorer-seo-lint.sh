#!/usr/bin/env bash
#
# explorer-seo-lint.sh — assert every INDEXABLE built page carries the on-page
# SEO basics: a non-empty <title>, a meta description, and a canonical link.
# Catches metadata regressions as the explorer grows (SEO plan WS15). Pages that
# are noindex (entity shells, /dashboard, /embed, /signin, …) are exempt.
#
# Usage: scripts/ci/explorer-seo-lint.sh [out-dir]
set -uo pipefail

OUT="${1:-web/explorer/out}"
if [ ! -d "$OUT" ]; then
  echo "explorer-seo-lint: '$OUT' not found — build the explorer first." >&2
  exit 2
fi

fail=0
checked=0
while IFS= read -r f; do
  # Skip noindex pages — they intentionally lack canonical/full metadata.
  if grep -qiE 'name="robots"[^>]*content="[^"]*noindex' "$f"; then
    continue
  fi
  checked=$((checked + 1))
  miss=""
  grep -qE "<title>[^<]+</title>" "$f" || miss="$miss title"
  grep -qiE 'name="description"[^>]*content="[^"]' "$f" || miss="$miss description"
  grep -qiE 'rel="canonical"' "$f" || miss="$miss canonical"
  if [ -n "$miss" ]; then
    echo "::error::${f#"$OUT"/} missing:$miss" >&2
    fail=$((fail + 1))
  fi
done < <(find "$OUT" -name '*.html')

echo "explorer-seo-lint: checked ${checked} indexable pages, ${fail} with missing metadata."
[ "$fail" -eq 0 ] || {
  echo "Every indexable page needs a <title>, meta description, and canonical." >&2
  exit 1
}
