#!/usr/bin/env bash
# public-export.sh — produce a clean, scrubbed, single-commit export of
# the repo ready to push to the public open-source repository: completely
# open source, publicly accessible and reproducible.
#
# Strategy: a FRESH single-commit history rather than the source repo's
# history — so no historical secret (e.g. a key that push-protection
# once caught) and no internal security-evidence dirs can ever be reached
# via `git log`.
#
# What it does:
#   1. `git archive HEAD` → a tree snapshot (no .git, no history).
#   2. Drops any internal-only content that may still be in the tree
#      (audit working dirs, local operator overlays).
#   3. Genericises the production host IP → a placeholder.
#   4. Verifies the export still builds (`go build ./...`) and verifies
#      no obvious secret slipped through.
#   5. Leaves the scrubbed tree at $OUT, ready for the operator to:
#        cd $OUT && git init && git add -A \
#          && git commit -m "Stellar Index v1.0.0 — initial public release" \
#          && git branch -M main \
#          && git remote add origin git@github.com:StellarIndex/stellar-index.git \
#          && git push -u origin main && git tag v1.0.0 && git push --tags
set -euo pipefail

OUT="${1:-/tmp/stellar-index-public}"
HOST_PLACEHOLDER='<R1_HOST>'
PROD_IP='136.243.90.96'

cd "$(git rev-parse --show-toplevel)"
echo "== public-export: snapshotting HEAD → $OUT =="
rm -rf "$OUT"; mkdir -p "$OUT"
git archive HEAD | tar -x -C "$OUT"

echo "== dropping internal-only content =="
# Audit working dirs: internal security evidence + r1 infra findings.
rm -rf "$OUT"/docs/audit-* 2>/dev/null || true
# Local operator overlay if it ever lands in the tree.
rm -f "$OUT"/configs/ansible/inventory/r1.yml \
      "$OUT"/configs/ansible/inventory/r2.yml \
      "$OUT"/configs/ansible/inventory/r3.yml 2>/dev/null || true

echo "== genericising production host IP =="
# Only rewrite occurrences in text files; binaries excluded by grep -I.
grep -rIl --binary-files=without-match "$PROD_IP" "$OUT" 2>/dev/null | while read -r f; do
  sed -i.bak "s/${PROD_IP}/${HOST_PLACEHOLDER}/g" "$f" && rm -f "$f.bak"
done

echo "== secret sweep on the export tree =="
HITS=$(grep -rIn --binary-files=without-match \
  -E 'BEGIN [A-Z ]*PRIVATE KEY|AKIA[A-Z0-9]{16}|rek_[A-Za-z0-9]{30,}|xoxb-[0-9]' \
  "$OUT" 2>/dev/null | grep -vE 'rek_\.\.\.|example|fixture|_test\.go|test\.js' || true)
if [[ -n "$HITS" ]]; then
  echo "!! POSSIBLE SECRETS in export — review before pushing:"; echo "$HITS"; exit 2
fi
echo "   clean."

echo "== build verification =="
( cd "$OUT" && go build ./... ) && echo "   go build ./... OK"

echo "== residual IP check =="
if grep -rIl "$PROD_IP" "$OUT" >/dev/null 2>&1; then
  echo "!! production IP still present:"; grep -rIl "$PROD_IP" "$OUT"; exit 3
fi
echo "   no production IP in export."

echo
echo "== DONE. Scrubbed export at: $OUT =="
echo "   $(find "$OUT" -type f | wc -l | tr -d ' ') files."
echo "   Operator next steps are in the header of this script."
