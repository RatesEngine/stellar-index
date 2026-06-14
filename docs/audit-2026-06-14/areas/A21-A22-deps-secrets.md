# A21 (deps / build integrity) + A22 (licensing + secrets sweep)

**Audit date:** 2026-06-14
**Mode:** READ-ONLY. No source/git/config edits made — only this file written.
**Auditor scope vs dimensions:** D2 (import boundaries / ADR), D3 (secrets),
D9 (VERSIONS.md currency, license headers).

---

## Headline

- **No real committed secret found.** Every key/token/credential pattern in the
  *git-tracked* tree resolves to a documented placeholder, an OpenAPI example, a
  public Stellar/Soroban address (G-/C-strkey, which are not credentials), or a
  Jinja `{{ vault_* }}` template variable. `git mod verify` → `all modules
  verified`.
- **One real GCP service-account private key exists ON DISK
  (`rates-engine-data-validation-0603331b2417.json`) but is correctly
  `.gitignore`d (line 113, `*-data-validation-*.json`) and is NOT tracked by
  git.** This is the *intended* posture — it validates `.gitignore` adequacy
  rather than being a leak. See A22-01 (Info, with a caveat).
- **One real build-integrity regression: the import-boundary lint
  (`scripts/ci/lint-imports.sh`) currently FAILS on `main`** — the new
  `internal/xdrjson` package imports `go-stellar-sdk/xdr` without an allowlist
  entry for rule B. This means `make verify` is red on a clean checkout. **High.**

---

## Findings table

| ID | Sev | Dim | Area | File / locus | Issue |
| --- | --- | --- | --- | --- | --- |
| A21-01 | **High** | D2/D9 | A21 | `scripts/ci/lint-imports.sh` + `internal/xdrjson/{operation,helpers}.go` | Import-boundary lint FAILS on main (exit 1, 2 NEW violations of rule B). `xdrjson` imports `go-stellar-sdk/xdr` but is not on rule B's allowlist. `make verify` is red on a clean checkout. |
| A21-02 | Low | D9 | A21 | `VERSIONS.md` | On-chain contract / discovery-repo SHAs are "Captured 2026-04-22"; several WASM-hash cells are placeholder ellipses (`18051456…770f73e`), and the file pre-dates ADR-0035 factory-gating + ADR-0038 explorer work. Tool + SDK pins are current; the snapshot table is partially stale. |
| A21-03 | Low | D2 | A21 | `scripts/ci/lint-imports.sh` | Allowlist predicates are bare substring matches (`pred in rel_path`). Short preds (`/decode.go`, `_test.go`, `internal/scval/`) are intentional but over-broad — e.g. any future file ending `…decode.go` or containing the substring anywhere is silently exempted from rule A. Robustness note, not a live bug. |
| A21-04 | Info | D9 | A21 | `.golangci.yml` gosec excludes | G101 (hardcoded creds) and G104/G115/G404 are globally excluded. G101 exclusion is justified in-file (env-var NAME constants) and corroborated by the secret sweep (no real Go-literal secrets), but it does remove gosec as a secondary secret tripwire — gitleaks is the sole automated secret gate. |
| A22-01 | Info | D3 | A22 | `rates-engine-data-validation-0603331b2417.json` (untracked) | Real GCP service-account key (`"type":"service_account"`, has `private_key`) present in the working dir. Correctly ignored (`.gitignore:113`) and NOT in git. No action needed for the flip; flagged only so it is consciously confirmed never `git add`-ed. |
| A22-02 | Info | D9 | A22 | `internal/**`, `cmd/**`, `pkg/**`, `web/explorer/src/**` | Per-file license headers are sparse: 17 / 942 Go files carry an `SPDX-License-Identifier: Apache-2.0` line; 0 carry full Apache header text; 0 / 150 web TS/TSX files have headers. Apache-2.0 is satisfied by the root `LICENSE` + README declaration, so this is NOT a compliance gap — but coverage is inconsistent (the 17 SPDX'd files are a recent partial convention, not a policy). No documented per-file-header policy in CONTRIBUTING / engineering-standards. |

No Critical findings. No Medium findings.

---

## CORRECT list (things explicitly verified as right)

### A21 — deps / build integrity

1. **`go mod verify` → `all modules verified`.** go.sum (530 lines) integrity is
   intact; no missing/extra sums. Modules mode (no `vendor/`).
2. **`go-stellar-sdk` pin is consistent across sources.** `go.mod` requires
   `v0.5.0` with comment "Pinned SHA in VERSIONS.md"; VERSIONS.md records
   `v0.5.0` ← SHA `9d52d04a911d…` (2026-04-22). Transitive `stellar/go-xdr`
   pseudo-version present as indirect. No drift.
3. **Tool-version pins match exactly between Makefile and VERSIONS.md:**
   gofumpt `v0.8.0`, goimports `v0.42.0`, golangci-lint `v2.11.4`,
   govulncheck `v1.1.4`. gitleaks `v8.21.2` matches between `VERSIONS.md` and
   `.github/workflows/ci.yml` (`GITLEAKS_VERSION: 8.21.2`).
4. **Every direct dep in go.mod carries an ADR reference or one-line role
   comment** (per the file's own stated policy "No unaudited deps"). Spot-checked:
   BurntSushi/toml (config + SEP-1), lib/pq (ADR-0006), redis/go-redis (ADR-0007),
   prometheus/client_golang (obs), go-stellar-sdk (ADR-0013).
5. **`go 1.25.10` directive** in go.mod (VERSIONS.md skeleton's `go 1.25` is the
   minor-only shorthand — consistent, not a conflict).
6. **All 3 production `//go:embed` directives resolve to existing, valid files:**
   - `internal/currency/verified.go` → `data/seed.yaml` (19 030 B; **valid YAML**,
     verified by `yaml.Unmarshal`; embed wiring via `LoadEmbedded()`
     `yaml.Unmarshal`; covered by `verified_test.go`).
   - `internal/sources/forex/circulation.go` → `circulation_data.csv` (9 313 B,
     present).
   - `internal/incidents/incidents.go` → `data/*.md` (3 incident MDs + `_template.md`,
     all present).
7. **`.golangci.yml`** is internally coherent: v2 schema, formatters
   (gofmt+goimports with `local-prefixes` = module path), correctness +
   complexity linters enabled, test-file relaxations scoped to `_test\.go`, every
   gosec exclude justified in a comment.
8. **import-boundary rules MATCH the ADR module boundaries** they claim to
   enforce: rule A (no `internal/stellarrpc` in production ingest — CLAUDE.md
   §6 / ingest-pipeline.md), rule B (xdr scoped to `internal/scval` — ADR-0013),
   rule C (no Horizon anywhere — ADR-0001). Rule C allowlist is empty (correct —
   Horizon is unconditionally banned). The baseline file
   (`scripts/ci/lint-imports.baseline`) is empty → lint runs strict, shrinks
   monotonically. (The rule SET is correct; the *current allowlist content* is
   stale — see A21-01.)
9. **`make verify` gate composition is comprehensive:** fmt, vet, lint, lint-docs,
   lint-imports, lint-openapi-urls, lint-pk-discriminators, monitoring-check /
   metric-refs, vuln, test, integration-build, web type/lint/build (×3 SPAs).
   verify.sh ends on the `✅ ALL CHECKS PASSED` sentinel and uses `set -euo
   pipefail`.

### A22 — licensing + secrets

10. **Whole-tree secret sweep is clean (git-tracked files):**
    - `rek_…` API-key pattern → only `rek_4f9c1d8b` / `rek_xxxx…` / `rek_topsecret`
      etc. — all OpenAPI examples, godoc, test fixtures, and CHANGELOG prose.
    - `re_live_…` (Resend) → single fabricated 48-hex example in OpenAPI spec +
      its generated mirrors + TS types. Documented + gitleaks-allowlisted as a
      response-shape illustration.
    - AWS `AKIA/ASIA…`: **zero** hits anywhere.
    - Stellar secret seed `S[A-Z2-7]{55}`: **zero** hits.
    - Slack/GitHub/Google/OpenAI tokens (`xox*`, `ghp_`, `AIza…`, `sk-…`): **zero**.
    - 32+ hex high-entropy literals in code/config (excluding hash/wasm/ledger
      fixtures): **zero**.
    - `BEGIN … PRIVATE KEY` blocks: only 3 files, all of which *reference the
      pattern as a scanner regex* (`scripts/dev/public-export.sh`,
      `docs/operations/public-flip-preflight-*.md`, audit W19 workstream) — no
      actual key material.
11. **Chainlink/Alchemy key claim VERIFIED.** The Alchemy API key (embedded in
    the RPC URL path) is **not in the repo.** All ~30 "alchemy" hits are
    placeholders (`https://eth-mainnet.g.alchemy.com/v2/<ALCHEMY_KEY>`), code
    that *treats the URL as a secret* (config doc tags say "treat the whole value
    as a secret. Prefer env var."; `CHAINLINK_RPC_URL` env), or the ansible
    template `CHAINLINK_RPC_URL={{ vault_chainlink_rpc_url | default('') }}`.
    Matches the documented posture (key lives in r1 TOML / vault, not the repo).
12. **`.gitignore` is robust for the public flip.** Explicitly ignores: `.env` /
    `.env.*` (keeps `!.env.example`), `*.env`, `credentials*.json`,
    `service-account*.json`, `*.key`/`*.pem`/`*.p12`/`*.pfx`/`*.jks`/`*.keystore`,
    `*.secrets.yml`/`yaml`, ansible vault files + `vault-password.txt`, real
    `r*.yml` inventories (keeps `r*.example.yml`), GCP keys
    (`*-data-validation-*.json`, `*.iam.gserviceaccount.com.json`, `gcp-key*.json`,
    `stellar-index-data-validation-*.json`), `.discovery-repos/`, and `.claude/`.
13. **No tracked secret-class files.** `git ls-files` for `.env`/`credentials`/
    `.key`/`.pem`/`data-validation`/`secret` returns only: `*.env.example`,
    ansible `*.env.j2` Jinja templates, and the W19 audit workstream docs. The
    `.discovery-repos/*.env*` files seen on disk are **0 tracked** (whole dir
    ignored). `web/explorer/.next` build output: **0 tracked** (per-package
    `.gitignore`).
14. **Ansible env/secret templates carry NO literal secrets** — all values are
    `{{ vault_* }}` / `{{ … }}` Jinja placeholders (`minio.env.j2`,
    `stellarindex.env.j2`). The `*_access_key` defaults
    (`galexie-archive-writer`, `stellarindex-reader`) are MinIO **usernames**;
    their paired secret keys are all `vault_*`. keepalived/patroni passwords
    default to `""`.
15. **Static UI bundle has no baked secrets.** `web/explorer/.next/static` scan
    for key/token/private-key patterns: clean. Explorer source uses only
    non-secret `NEXT_PUBLIC_*` vars (`BUILD_SHA`, `BUILD_TIME`, API/base URLs);
    **zero** `process.env.*KEY/SECRET/TOKEN/PASSWORD` usages.
16. **`example.toml` models the secrets-via-env discipline** ("Secrets … NEVER
    belong in this file"; `*_env` field names; commented `# prefer env:` for every
    connector API key). No real values.
17. **gitleaks runs on every PR in CI** (`ci.yml` → `gitleaks detect --redact`,
    pinned version with a SHA256-verified tarball download). `.gitleaks.toml` uses
    `useDefault = true` + path-scoped allowlists, each with a written false-positive
    rationale (no fingerprint-ignores, deliberately — squash merges rotate hashes).
18. **LICENSE = Apache-2.0** (full text); README declares Apache-2.0 in two
    places; web `package.json`s are `"private": true` (not published, so no
    `license` field required). Root-license + declaration satisfies Apache-2.0 —
    see A22-02 for the per-file-header consistency note.

---

## Detail on the actionable findings

### A21-01 (High) — import-boundary lint fails on main

Running `bash scripts/ci/lint-imports.sh` on a clean `main` checkout exits **1**:

```
import-lint [B/xdr-scoped-to-scval] ❌ internal/xdrjson/operation.go imports github.com/stellar/go-stellar-sdk/xdr
import-lint [B/xdr-scoped-to-scval] ❌ internal/xdrjson/helpers.go imports github.com/stellar/go-stellar-sdk/xdr
import-lint: FAIL (2 NEW violation(s) — not in scripts/ci/lint-imports.baseline).
```

`internal/xdrjson` was added in committed HEAD `2638610d`
("feat(explorer): Phase A unit 2a — XDR->JSON decode + /v1/operations (ADR-0038)").
Its `doc.go` describes it as a **structural** decoder of raw classic XDR from the
ClickHouse lake into JSON — i.e. the *same legitimate category* as
`internal/storage/clickhouse/` and `internal/sources/sdex/`, both already on
rule B's allowlist. It does NOT decode SCVal (which is what ADR-0013 scopes to
`internal/scval`). So the lint is correctly firing on a missing allowlist entry,
not on a genuine boundary violation.

Impact: `lint-imports.sh` is part of `make verify` and CI; the gate is red on a
clean tree. Either the lint job is currently failing in CI on every PR, or it is
being bypassed/overridden — both warrant a look. Correct fix (out of scope for
this read-only pass): add `"internal/xdrjson/"` to rule B's `allow` list with a
rationale comment matching the clickhouse/sdex precedent.

Confidence: **high** (reproduced; real exit 1 captured directly, not through a
`| tail` pipe that would mask it).

### A21-02 (Low) — VERSIONS.md partial staleness

`VERSIONS.md` is explicitly stamped "Captured: 2026-04-22." The Go-dep + tool +
SDK pins are current (verified above), but: (a) the on-chain-contract table has
placeholder WASM-hash ellipses (`18051456…770f73e`) and several "(factory hash)"
stubs, and (b) the file pre-dates ADR-0035 factory-anchored gating and ADR-0038
explorer work that have since landed. The file's own "How to keep this file
honest" section says every audit-doc claim must be re-verifiable at the pinned
SHA — the discovery-repo SHAs are fine for that, but the WASM-hash strong-pins
are incomplete. Low because nothing in the *build* depends on these cells; it is a
documentation-completeness gap on the public flip.

### A21-03 (Low) — allowlist substring matching

`file_allowed()` does `if pred in rel_path` (Python substring). Predicates like
`/decode.go`, `_test.go`, `internal/scval/` are intended as path-segment matches
but will match the substring *anywhere*. Today's tree has no accidental exemption,
but a future file named e.g. `xyz_decode.go` would be silently exempted from
rule A. Robustness hardening, not a live bug.

---

## Files / surfaces scanned

- **Build-integrity:** `go.mod`, `go.sum` (530 lines), `VERSIONS.md`,
  `.golangci.yml`, `scripts/ci/lint-imports.sh` + `.baseline`, `Makefile`
  (verify/vuln/deps/lint targets), `scripts/dev/verify.sh`,
  `.github/workflows/ci.yml` (gitleaks + govulncheck jobs). Ran `go mod verify`
  and `scripts/ci/lint-imports.sh` live.
- **Embeds:** all 3 production `//go:embed` sites + their target files
  (seed.yaml validated as YAML via Go; circulation_data.csv + incidents/*.md
  existence).
- **Secret sweep (whole tree):** patterns `rek_`, `re_(live|test)_`,
  `AKIA/ASIA…`, Stellar `S`-seed, `xox*`/`ghp_`/`gho_`/`github_pat_`/`AIza`/`sk-`,
  32+ hex literals, `BEGIN … PRIVATE KEY`, `alchemy`, generic
  `(password|secret|api_key|token|access_key) = "literal"` — across `.go .toml
  .yaml .yml .json .ts .tsx .md .sh .env*` in `internal cmd pkg configs deploy
  scripts test examples web/explorer` (node_modules and `.git` excluded).
- **Licensing:** LICENSE, README, CONTRIBUTING, engineering-standards;
  SPDX/Apache header coverage across 942 Go + 150 web TS/TSX files; web
  `package.json` license/private fields.
- **`.gitignore` + tracked-file inventory:** `git ls-files` cross-checks for
  secret-class names; `git check-ignore` on the on-disk GCP key,
  `.discovery-repos/`, and `web/explorer/.next/`.
- **Counts:** 2 452 git-tracked files total; 942 Go files (internal+cmd+pkg);
  198 config/ansible files; 153 web/explorer src files. Effective scanned surface
  for this area: the whole tree via the grep sweeps above + targeted reads of the
  ~15 build/lint/license control files.

**Verdict for A21+A22:** 1 High (verify-gate red — `xdrjson` missing from the
import-lint allowlist), 0 Critical secrets. The secrets/licensing posture for the
public flip is sound: no committed credential, robust `.gitignore`, the one
real key on disk is correctly ignored, the static bundle is clean, and Apache-2.0
is properly declared at the repo level.
