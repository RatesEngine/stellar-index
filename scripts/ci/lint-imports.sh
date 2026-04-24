#!/usr/bin/env bash
# Import-boundary lint for Rates Engine.
#
# Enforces architectural layering rules that docs alone can't: a
# build-time check that certain package imports don't leak into
# production code paths. Adding a rule is a ~5-line amendment to
# the RULES array below.
#
# Current rules
# -------------
#
#   A. No `internal/stellarrpc` in production ingest.
#      Reason: stellar-rpc was removed from r1 on 2026-04-23; the
#      production ingest path is Galexie → ledgerstream →
#      dispatcher → decoder. See
#      docs/architecture/ingest-pipeline.md.
#      Allowed in:
#        - internal/stellarrpc/           (the package itself)
#        - cmd/ratesengine-ops/           (`rpc-probe` diag)
#        - scripts/dev/                   (fixture-capture scripts)
#        - internal/sources/*/decode.go   (imports `Event` type
#                                          only; type moves to a
#                                          neutral package in PR
#                                          165b, at which point
#                                          this allowlist entry
#                                          goes away)
#        - *_test.go                      (test files)
#
#   B. No go-stellar-sdk/xdr in production connectors.
#      Reason: ADR-0013 scopes the xdr dep to the internal/scval
#      wrapper. Connectors work with the scval re-exports
#      (ScVal, ScMapEntry aliases).
#      Allowed in:
#        - internal/scval/                (the wrapper)
#        - *_test.go                      (test fixtures often
#                                          construct raw xdr)
#
#   C. No Horizon imports, anywhere.
#      Reason: ADR-0001 rules Horizon out entirely. Preemptive.
#      Allowed in: nowhere.
#
# Usage
# -----
#
#   scripts/ci/lint-imports.sh
#
# Exit 0 on clean, non-zero on any violation. Hooked into
# `make verify` and CI.

set -euo pipefail

REPO_ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
cd "$REPO_ROOT"

# Delegate to a small Python program — walking *.go files and
# parsing import blocks is easier in Python than in bash.
python3 - <<'PY'
import os
import re
import sys
from pathlib import Path

REPO = Path(".").resolve()

# ─── Rule definitions ────────────────────────────────────────────
#
# Each rule is a dict with:
#   name     — short identifier for the error line.
#   banned   — list of package paths that trigger the rule.
#   allow    — list of file-path prefix / glob predicates; if ANY
#              matches, the file is allowed to have the banned
#              import. Matching is substring-prefix for strings
#              starting with "/" or substring-match for globs with
#              "*". "_test.go" matches any file ending in that.
#   why      — human-readable reason; printed on violation.
# ---------------------------------------------------------------

RULES = [
    {
        "name": "A/no-rpc-in-ingest",
        "banned": [
            "github.com/RatesEngine/rates-engine/internal/stellarrpc",
        ],
        "allow": [
            "internal/stellarrpc/",             # the package itself
            "cmd/ratesengine-ops/",             # rpc-probe diag
            "scripts/dev/",                     # fixture-capture
            "/decode.go",                       # source decode.go — uses Event type only (PR 165b will move)
            "/factory_seed.go",                 # cold-start factory state via simulateTransaction (PR 14) — not a runtime decoder
            "_test.go",                         # tests
        ],
        "why": (
            "stellar-rpc was removed from r1 2026-04-23; production ingest "
            "is Galexie → ledgerstream → dispatcher → decoder. See "
            "docs/architecture/ingest-pipeline.md."
        ),
    },
    {
        "name": "B/xdr-scoped-to-scval",
        "banned": [
            "github.com/stellar/go-stellar-sdk/xdr",
        ],
        "allow": [
            "internal/scval/",                  # the Soroban-event scval wrapper
            "internal/ledgerstream/",           # transport layer — exposes xdr.LedgerCloseMeta
            "internal/dispatcher/",             # routes tx / events (PR 165b)
            "internal/sources/sdex/",           # SDEX decodes non-SCVal xdr (classic ops) (PR 165c)
            "cmd/ratesengine-indexer/",         # glue: wires ledgerstream → dispatcher (PR 165d)
            "cmd/ratesengine-ops/",             # verify-decoders mirrors the indexer's ledger plumbing
            "internal/stellarrpc/",             # builds TransactionEnvelope XDR for simulateTransaction (not SCVal)
            "_test.go",                         # fixture construction
        ],
        "why": (
            "ADR-0013 scopes the xdr dependency away from Soroban "
            "event decoders — those go through internal/scval. "
            "Ledger-meta plumbing (ledgerstream, dispatcher, sdex) "
            "legitimately consumes non-SCVal xdr types."
        ),
    },
    {
        "name": "C/no-horizon",
        "banned": [
            "github.com/stellar/go/clients/horizonclient",
            "github.com/stellar/go/protocols/horizon",
            "github.com/stellar/go-stellar-sdk/clients/horizonclient",
            "github.com/stellar/go-stellar-sdk/protocols/horizon",
        ],
        "allow": [],
        "why": "ADR-0001 rules Horizon out entirely.",
    },
]

# ─── Walker ──────────────────────────────────────────────────────

IMPORT_RE = re.compile(r'^\s*(?:[a-zA-Z_][a-zA-Z0-9_]*\s+)?"([^"]+)"')


def file_allowed(rule, rel_path):
    """True if rel_path matches any allowlist predicate for the rule."""
    for pred in rule["allow"]:
        if pred in rel_path:
            return True
    return False


def imports_in(path):
    """Yield import paths from a .go file by parsing the import block."""
    try:
        text = path.read_text()
    except (OSError, UnicodeDecodeError):
        return
    in_block = False
    for line in text.splitlines():
        stripped = line.strip()
        if stripped.startswith("import ("):
            in_block = True
            continue
        if in_block:
            if stripped == ")":
                in_block = False
                continue
            m = IMPORT_RE.match(line)
            if m:
                yield m.group(1)
        elif stripped.startswith("import "):
            # Single-import form: `import "x"` or `import alias "x"`.
            m = IMPORT_RE.match(stripped[len("import "):])
            if m:
                yield m.group(1)


SKIP_DIRS = {".git", "vendor", ".discovery-repos", "node_modules"}


def walk_go_files(root):
    for dirpath, dirnames, filenames in os.walk(root):
        dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
        for fn in filenames:
            if fn.endswith(".go"):
                yield Path(dirpath) / fn


# ─── Main ───────────────────────────────────────────────────────

BASELINE_PATH = REPO / "scripts/ci/lint-imports.baseline"


def load_baseline():
    """Return a set of (rule_name, rel_path) pairs from the baseline file.

    Empty set if the baseline doesn't exist — the lint runs strict.
    """
    if not BASELINE_PATH.exists():
        return set()
    allowed = set()
    for raw in BASELINE_PATH.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith("#"):
            continue
        parts = line.split(None, 1)
        if len(parts) != 2:
            print(f"baseline: malformed line: {raw!r}", file=sys.stderr)
            continue
        allowed.add((parts[0], parts[1]))
    return allowed


def main():
    baseline = load_baseline()
    observed = set()  # (rule_name, rel_path) pairs we actually hit
    regressions = []  # violations NOT in baseline — real fails

    for go_file in walk_go_files(REPO):
        rel = str(go_file.relative_to(REPO))
        imports = list(imports_in(go_file))
        for rule in RULES:
            for imp in imports:
                if imp not in rule["banned"]:
                    continue
                if file_allowed(rule, rel):
                    continue
                pair = (rule["name"], rel)
                observed.add(pair)
                if pair not in baseline:
                    regressions.append((rule, rel, imp))

    # Stale baseline entries — no longer violating. Force shrink.
    stale = baseline - observed

    if regressions:
        for rule, rel, imp in regressions:
            print(f"import-lint [{rule['name']}] ❌ {rel} imports {imp}",
                  file=sys.stderr)
        print("", file=sys.stderr)
        seen = set()
        for rule, _rel, _imp in regressions:
            if rule["name"] in seen:
                continue
            seen.add(rule["name"])
            print(f"  {rule['name']}: {rule['why']}", file=sys.stderr)
        print("", file=sys.stderr)
        print(f"import-lint: FAIL ({len(regressions)} NEW violation(s) — "
              "not in scripts/ci/lint-imports.baseline).",
              file=sys.stderr)
        print("Either (a) fix the import, (b) justify adding the file to "
              "the allowlist in lint-imports.sh, or (c) add to "
              "lint-imports.baseline only with a PR note citing why.",
              file=sys.stderr)
        sys.exit(1)

    if stale:
        for rule_name, rel in sorted(stale):
            print(f"import-lint [{rule_name}] ⚠ baseline stale: {rel} "
                  "no longer violates — remove from "
                  "scripts/ci/lint-imports.baseline.",
                  file=sys.stderr)
        print("", file=sys.stderr)
        print(f"import-lint: FAIL ({len(stale)} stale baseline entry/ies). "
              "Shrink the baseline monotonically as the rework lands.",
              file=sys.stderr)
        sys.exit(1)

    total_baseline = len(observed)
    if total_baseline:
        print(f"✅ import-lint passed. {total_baseline} known-legacy "
              "violation(s) grandfathered via lint-imports.baseline.")
    else:
        print("✅ import-lint passed.")


main()
PY
