# Public-flip pre-flight sweep — 2026-06-12

Pre-flight sweep for the public open-source release.
Scope: the **working tree only** (2,406 tracked files via `git ls-files`) —
the flip strategy is a fresh-history export at v1.0, so private git history
never ships and tree-state is the only thing that matters.

Method: pattern greps over tracked files for secrets / tokens / private
infrastructure details, license + dependency review, and a docs-vs-Makefile
reproducibility cross-check. Read-only; nothing was changed.

---

## 1. Secret scan — **CLEAN**

Patterns swept: `api[_-]?key` / `secret` / `password` / `token` assignments
with long literal values, `BEGIN … PRIVATE KEY`, AWS `AKIA[A-Z0-9]{16}`,
vendor token shapes (`sk_live`, `ghp_`, `xox[bp]-`, `re_…`, `AIza…`,
`glpat-`), 40+-char base64-looking assignments, and Stellar secret seeds
(`S[A-Z2-7]{55}`).

- **No real credentials found.** Every hit resolves to one of:
  - Test fixtures: `internal/api/v1/stripe_webhook_test.go:116`
    (`whsec_test_signing_secret_value`),
    `internal/currency/marketcap/refresher_test.go:28`
    (`cg-pro-secret-DO-NOT-LEAK`),
    `internal/sources/external/coingecko/poller_backoff_test.go:211`
    (`demo-test-key-123`). All obviously synthetic.
  - Public on-chain identifiers (Stellar `C…` contract IDs, Chainlink
    `0x…` feed addresses) — public by definition.
  - Doc-comment prose (`internal/auth/store.go:187` describes the
    `sk_live_…` / `AKIA…` *pattern* for key-prefix design).
  - Base64 image data URIs inside doc files (false-positive
    source for naive scans; verified not key material).
- **No Stellar secret seeds** anywhere in tracked files.
- **Ansible templates are fully variable-substituted** — e.g.
  `configs/ansible/roles/archival-node/templates/stellarindex.env.j2:23-37`
  uses `{{ vault_* }}` jinja vars; `minio.env.j2:8` likewise.
  `deploy/docker-compose/.env.example` carries only obvious dev defaults
  (`stellarindex-dev`).
- **Known-sensitive files verified NOT tracked:**
  - `rates-engine-data-validation-0603331b2417.json` (GCP service-account
    key) exists in the working dir but is ignored via `.gitignore:107`
    (`*-data-validation-*.json`) and is not in the index
    (`git ls-files --error-unmatch` confirms).
  - Zero tracked `*.pem` / `*.key` / `*.p12` files.
  - `.gitignore` defence-in-depth covers `.env*`, `credentials*.json`,
    `service-account*.json`, `*.key`, `*.pem`, `*.secrets.y(a)ml`,
    ansible vault paths.

Verdict: **CLEAN** — a fresh export of the tracked tree leaks no credentials.

---

## 2. License — **CLEAN**

- `LICENSE` is the verbatim Apache-2.0 text.
- **Third-party attribution:** the only `Copyright` headers in tracked
  source are first-party (`// Copyright (c) 2026 Stellar Index
  contributors.` on ~17 newer files). No copied third-party code headers
  found in `*.go` / `*.sh` / `*.js` / `*.ts` / `*.tsx`. (Header presence is
  inconsistent — most files have none — but Apache-2.0 does not require
  per-file headers; cosmetic only.)
- **go.mod:** direct deps are all permissive (BurntSushi/toml, miniredis,
  golang-migrate, lib/pq, prometheus/client_golang, go-redis,
  testcontainers-go, x/sync, cloud.google.com/go/bigquery,
  clickhouse-go/v2, aws-sdk-go-v2, coder/websocket, go-systemd,
  google/uuid, x/crypto, google.golang.org/api, yaml.v3,
  stellar/go-stellar-sdk). Spot-check for copyleft (`juju`, `gpl`,
  sqlite3, etc.): nothing; closest is `hashicorp/golang-lru` (MPL-2.0,
  **indirect**, file-level copyleft — fine to depend on, standard in the
  Go ecosystem). No GPL/LGPL/AGPL dependency, direct or indirect.

Verdict: **CLEAN**.

---

## 3. Reproducibility (README + getting-started vs Makefile) — **NEEDS-ACTION (1)**

- `make help` targets referenced by docs all exist: `dev`, `dev-teardown`,
  `test`, `test-integration`, `lint`, `build`, `docs-all`, `docs-api`,
  `verify`. `deploy/docker-compose/dev.yaml` exists at the path both docs
  reference.
- `README.md` self-hosting blurb is accurate: "`make dev` boots the full
  local stack (TimescaleDB + Redis + MinIO)".
- **NEEDS-ACTION — `docs/getting-started.md:201`:**
  `make dev    # docker-compose: TimescaleDB + Redis + MinIO + API`
  claims the API is part of the compose stack. It is not — there is no
  API service in `deploy/docker-compose/dev.yaml` (CLAUDE.md and the
  Makefile `dev` help text agree: app binaries run on the host). A
  first-time external user following the quickstart will look for an API
  container that never starts. Fix the comment (and ideally add the
  two-line "then run `make build && bin/stellarindex-api`" step).
- **Flip dependency, not a doc bug:** `docs/getting-started.md:199` and
  README clone/issues/releases links all point at
  `github.com/StellarIndex/stellar-index`, which matches the `go.mod`
  module path (`module github.com/StellarIndex/stellar-index`). The
  public repo MUST be created at exactly that org/name or `go install`
  / `git clone` instructions break on day one (the private development
  repo lives under a different org today).

Verdict: **NEEDS-ACTION** — one stale quickstart comment; repo-name
constraint noted for the flip itself.

---

## 4. Private-info sweep — **NEEDS-ACTION (4 + 1 cosmetic)**

### 4a. Production server IP `136.243.90.96` — decision required

**79 occurrences across 48 tracked files.** Three clusters:

| Cluster | Files (examples) | Character |
| --- | --- | --- |
| Ops runbooks + configs READMEs | `configs/caddy/Caddyfile.api:8`, `configs/healthchecks/README.md:88-89`, `configs/prometheus/rules.r1/README.md:54-55`, `docs/operations/runbooks/{projector-lag,minio-metrics-403,external-poller-stale,fx-history-missing}.md`, `docs/operations/{deploy-workflow,cf-pages-setup,pre-launch-hardening,lcm-cache-tiering}.md`, `scripts/dev/r1-smoke.sh`, `scripts/ops/cf-pages-bootstrap.sh` | `ssh root@136.243.90.96` copy-paste commands |
| Internal working archives | the internal audit/working-note directories (~25 files), kept out of the published tree | Live-probe evidence, **including security findings about the host** (F-1201 partial firewall hardening, externally-reachable ports 9000/11726 at probe time) |
| Workflow comments | `.github/workflows/deploy.yml:9` ("e.g. 136.243.90.96") | Example value for a secret |

The IP itself is semi-public (it is the A record for
`api.stellarindex.io`), but publishing **root-SSH command lines plus an
audit trail of the host's historical security weaknesses** is a different
exposure class. Decide per cluster: (1) runbooks — replace the literal IP
with `$R1_HOST` / `r1` (mechanical sed, keeps runbooks useful); (2) internal
working archives — keep them **out of the public export** (internal working
artifacts, heavy on live-host evidence); (3) workflow comment — harmless
once (1)/(2) are decided, but trivial to genericise.

### 4b. Personal email — minor

`ash@ashfrancis.com` appears only as a placeholder SSH-key comment marked
"replace" in `configs/ansible/inventory/r{1,2,3}.example.yml`
(r1:119, r2:91, r3:107). Replace with `ops@stellarindex.io` or
`you@example.com`. `ash.francis@x.com` / `ash@acme.com` in
`internal/api/v1/dashboardauth/handlers_test.go` are synthetic test
fixtures — fine. No other personal emails; everything else is
`@example`/`@acme.example` test data or project-domain addresses.

### 4c. Private-codebase references — decision required

`~/code/rates` (the predecessor private codebase) is
referenced in: `CLAUDE.md:445-446` (CEX-connector recipe), shipped source
comments `internal/sources/external/binance/events.go:11` and
`internal/sources/external/coinbase/events.go:4`, and `CHANGELOG.md:14013`.
These point external readers at a local path they can't access and name a
predecessor system. Minimum: scrub the two shipped `.go` comments + the
CLAUDE.md recipe step; decide whether the predecessor-system teardown
notes ship at all.

### 4d. Internal working documents — excluded from export

A set of internal working documents (requirements and scoping notes)
that embed base64 images and infra details are kept out of the public
export. The first-party coverage summary
(`docs/architecture/coverage-matrix.md`) carries the requirements
mapping that external readers need.

### 4e. Cosmetic: legacy-domain branding

33 files still mention the old domain — concentrated in the dated audit
dirs (19 files), `docs/adr`, `docs/blog`. If `docs/audit-*`
is excluded per 4a, the residue is small; historical artifacts may
legitimately keep their original domain. No action strictly required.

### Confirmed absent

- **Healthchecks.io ping UUIDs:** none — all references are
  `https://hc-ping.com/<uuid-…>` placeholders
  (`configs/healthchecks/README.md:101-105`); a full UUID-shape grep over
  the tree found zero real UUIDs.
- **Cloudflare account IDs:** none — workflows use
  `${{ secrets.CLOUDFLARE_ACCOUNT_ID }}`; scripts require it as an env var
  (`scripts/ops/cf-pages-bootstrap.sh:39`); docs use `<account-id>`.
- **"TODO: remove before public" markers:** none found
  (`remove before public` / `do not publish` / `private repo only` greps
  hit only aggregator "do not publish this bucket" domain language).
- **Internal hostnames:** `stellarindex-archival-r1` / `r1`-style names
  appear in ops docs as machine labels only; no internal-DNS leakage
  beyond the IP question in 4a.

Verdict: **NEEDS-ACTION** — items 4a–4d above.

---

## 5. VERSIONS.md — **CLEAN**

Present at repo root; captured 2026-04-22. Pins SHAs/tags for 21
well-known public repos (`stellar/stellar-galexie`,
`stellar/go-stellar-sdk`, `stellar/rs-stellar-archivist`, `soroswap/core`,
`reflector-network/reflector-contract`, `blend-capital/…`,
`Phoenix-Protocol-Group/…`, `withObsrvr/…`, `bandprotocol/…`,
`redstone-finance/…`, `zenith-protocols/…`, `stellar/stellar-protocol`,
`stellar/stellar-docs`, …). All are public, active orgs; nothing
references a private or vanished repo. The production-dependency shortlist
matches `go.mod` (module `github.com/StellarIndex/stellar-index`,
`go-stellar-sdk v0.5.0`). Sanity-read: internally consistent, accurately
notes the stellar/go monorepo archive + per-repo split.

---

## Verdict summary

| Check | Verdict | NEEDS-ACTION items |
| --- | --- | --- |
| 1. Secret scan | CLEAN | 0 |
| 2. License | CLEAN | 0 |
| 3. Reproducibility | NEEDS-ACTION | 1 (getting-started.md:201) |
| 4. Private-info | NEEDS-ACTION | 4 (+1 cosmetic) |
| 5. VERSIONS.md | CLEAN | 0 |

**Total NEEDS-ACTION: 5** (plus one cosmetic note).

---

## Ordered action list for the flip

1. **Decide the `docs/audit-*` exposure question** (4a). Recommended:
   exclude all six `docs/audit-*` dirs from the public export — they are
   internal working artifacts containing root-SSH evidence trails and
   historical security findings about the live host, and they carry most
   of the stale branding. This one decision resolves ~25 of the 48
   IP-bearing files.
2. **Genericise the production IP in operator docs/configs** (4a):
   `sed 136.243.90.96 → <r1-host>` (or `$R1_HOST`) across
   `docs/operations/**`, `configs/**` READMEs, `scripts/dev/r1-smoke.sh`,
   `scripts/ops/*.sh`, `.github/workflows/deploy.yml`. Keep the runbooks
   copy-pasteable via a single "set R1_HOST" preamble.
3. **Exclude the internal working documents** (4d) from the export —
   the first-party coverage summary
   (`docs/architecture/coverage-matrix.md`) carries the requirements
   mapping external readers need.
4. **Scrub private-codebase references** (4c): the two shipped source
   comments (`internal/sources/external/{binance,coinbase}/events.go`)
   and the `CLAUDE.md:445-446` recipe step.
5. **Fix `docs/getting-started.md:201`** ("+ API" is wrong — compose has
   no API service) and replace the `ash@ashfrancis.com` placeholders in
   the three `configs/ansible/inventory/r*.example.yml` files (3 + 4b).
6. **Flip mechanics precondition:** create the public repo at exactly
   `github.com/StellarIndex/stellar-index` (matches `go.mod` module path
   and every clone/issues/releases link in README + getting-started);
   never push private history (per the standing fresh-export strategy).
7. (Optional, cosmetic) Sweep residual legacy-domain mentions outside
   the audit dirs (`docs/adr`, `docs/blog`) if a clean
   brand surface is wanted; dated artifacts may keep the old domain.

— Generated by the pre-flight sweep, 2026-06-12. Read-only run; no files
other than this report were modified.

## Resolution status (same day)

- Fixed in-tree: getting-started `make dev` claim; predecessor-system
  references in binance/coinbase/CLAUDE.md; ansible example emails.
- **Export-time rules for the flip script** (fresh-history export):
  exclude `docs/audit-*/` (six dirs — root-SSH evidence + historical
  security findings) and predecessor-analysis notes if any remain;
  rewrite `136.243.90.96` → `$R1_HOST` in everything exported
  (operator docs keep a private overlay).
- Operator: keep the internal working documents out of the export;
  ship the first-party coverage summary instead.
