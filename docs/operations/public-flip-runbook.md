# Public open-source flip — operator runbook

Makes the project completely open source — publicly accessible and
reproducible. Strategy: a **fresh single-commit public repo** — never
push the private history (it once contained a GCP key that GitHub
push-protection caught; and the audit working dirs carry internal r1
security evidence). See `public-flip-preflight-2026-06-12.md` for the
CLEAN pre-flight (no secrets / Apache-2.0 / VERSIONS current).

## Prerequisites (operator-only — cannot be scripted)

1. **GitHub org `StellarIndex`** — created via the GitHub web UI (orgs
   can't be created by API/CLI). The module path is
   `github.com/StellarIndex/stellar-index`, so the public repo MUST live
   there for `go get …/pkg/client` (the SDK) to resolve.
2. **Empty public repo** `StellarIndex/stellar-index` (no README/license
   init — the export supplies them).

## Steps

1. **Generate the scrubbed export** (idempotent; build-verifies):

   ```sh
   bash scripts/dev/public-export.sh /tmp/stellar-index-public
   ```

   Drops `docs/audit-*` + the predecessor analysis, genericises the
   prod host IP, secret-sweeps, and runs `go build ./...`.

2. **Init + push** the fresh history:

   ```sh
   cd /tmp/stellar-index-public
   git init -q && git add -A
   git commit -q -m "Stellar Index v1.0.0 — initial public release"
   git branch -M main
   git remote add origin git@github.com:StellarIndex/stellar-index.git
   git push -u origin main
   git tag v1.0.0 && git push origin v1.0.0
   ```

3. **Wire CI on the public repo** — the workflows are in the export
   (`.github/workflows/`). Add the repo secrets they need (CLOUDFLARE_*
   for the Pages deploys, deploy SSH keys are NOT needed publicly —
   the deploy workflow targets the private operator overlay). Confirm
   the first `ci.yml` run is green (watch the Actions billing-cap
   pattern — jobs failing in 0–2s = cap hit, not code).

4. **Reproducibility proof** (record for the evidence pack): on a clean
   checkout of the public repo, `make dev` (boots TimescaleDB + Redis +
   MinIO) then `make verify` → ALL CHECKS PASSED. Capture the output.

5. **README badge + topics**: add the Apache-2.0 badge, repo topics
   (`stellar`, `soroban`, `defi`, `price-api`, `blockchain-explorer`),
   and the hosted-API link once DNS is live.

## What stays private

- The development repo (full history) stays private as the operator's
  working repo. The public repo is a release artifact, re-exported per
  release.
- The audit working dirs, the ansible operator inventories, and any
  `*-data-validation-*.json`-class credentials never leave the private
  side.

## Release cadence going forward

Each tagged release re-runs `public-export.sh` and force-updates the
public repo's release branch (or, to preserve public history, commits
the diff). For v1.0.0 the single-commit snapshot is the clean baseline.
