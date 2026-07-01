#!/usr/bin/env bash
# i128 / NUMERIC invariant guard (ADR-0003).
#
# The two checks ADR-0003 long CLAIMED it enforced but never actually had
# (see the 2026-07 "REALITY" note in that ADR). Both should stay at zero
# hits — the tree is clean today; this keeps it that way.
#
#   1. Go: reject `int64(<x>.Lo)` / `int(<x>.Lo)` — truncating a 128-bit
#      Soroban value to its low 64 bits, discarding the high word (the
#      classic KALIEN-class precision-loss bug). The correct decode passes
#      lo as uint64 to canonical.FromInt128Parts(int64(p.Hi), uint64(p.Lo)).
#      `\bint(64)?\(` deliberately does NOT match the correct `uint64(p.Lo)`.
#
#   2. Migrations: reject BIGINT / DOUBLE PRECISION / REAL / FLOAT on a
#      column whose name marks it a monetary amount — those are NUMERIC
#      (JSON numbers are IEEE-754 doubles; amounts overflow int64/2^53).
#
# Exit 0 clean, non-zero on any violation. Wired into `make verify`.
set -euo pipefail
cd "$(dirname "$0")/../.."
fail=0

# ── 1. i128 truncation in Go (production code; tests exempt) ──────────────
# Skip comment lines (the ADR/decoder docstrings mention int64(parts.Lo) to
# WARN against it — that's not a violation).
hits=$(grep -rnE '\bint(64)?\([A-Za-z_][A-Za-z0-9_.]*\.Lo\)' \
  --include='*.go' internal/ cmd/ pkg/ 2>/dev/null \
  | grep -v '_test\.go' \
  | grep -vE '^[^:]+:[0-9]+:[[:space:]]*//' || true)
if [ -n "$hits" ]; then
  echo "lint-i128 ❌ i128 truncation — int64(x.Lo) discards the high 64 bits (ADR-0003):" >&2
  echo "$hits" >&2
  echo "  → decode via canonical.FromInt128Parts(int64(p.Hi), uint64(p.Lo))." >&2
  fail=1
fi

# ── 2. amount columns must be NUMERIC (never BIGINT / floating) ───────────
# High-confidence monetary names only, to avoid false-positives on counts /
# sequences / ids that legitimately use bigint. `price` is EXCLUDED — SDEX
# offer prices are stored as a rational (price_n / price_d bigints), a
# legitimate non-NUMERIC representation.
amt='amount|balance|reserve|supply|stroop|wei|circulating|market_cap'
badcols=$(grep -rhniE "\b(${amt})[a-z0-9_]* +(bigint|double precision|real|float)\b" \
  migrations/ 2>/dev/null \
  | grep -viE 'ledger|_seq|_count|count_|_id\b|index|version|decimals|_at\b|epoch|height|comment|--' \
  || true)
if [ -n "$badcols" ]; then
  echo "lint-i128 ❌ monetary column is not NUMERIC (ADR-0003):" >&2
  echo "$badcols" >&2
  echo "  → amounts are NUMERIC; use BIGINT/float only for non-monetary columns." >&2
  fail=1
fi

if [ "$fail" -eq 0 ]; then
  echo "✅ i128/NUMERIC lint passed."
fi
exit "$fail"
