# A18 — Ops surface (configs / scripts / deploy / CI)

**Auditor pass:** READ-ONLY, 2026-06-14
**Scope:** `configs/**` (example.toml, ansible roles/inventory/playbooks,
prometheus rules.r1, alertmanager, caddy, loki, healthchecks),
`scripts/**` (dev/ops/ci), `deploy/**` (systemd, monitoring rules, comms,
docker-compose, clickhouse), `.github/workflows/**`, `Makefile`.
**Audit dimensions:** D3 (secrets), D9 (alert↔metric↔runbook parity,
release/CHANGELOG correctness, brand drift), D1 (gate completeness:
verify.sh / r1-smoke.sh / public-export.sh / cut-release.sh),
workflow correctness (deploy/release/ci + Cloudflare deploys), brand drift.

**Headline:** No committed secrets. Alerting layer (212 alert instances,
2 dirs) is clean — full runbook coverage, dual-dir parity, no dead alerts,
no `ratesengine` residue in monitoring. Workflows are well-hardened (env-var
input passing, SHA-pinned actions, checksum-verified tool installs, MITM-safe
SSH). **One genuine issue: 136 MB of stale pre-rebrand macOS binaries
(`ratesengine-api`, `ratesengine-indexer`) are committed at repo root and
would ship into the public open-source export.** A handful of doc/comment-drift
and defense-in-depth items below.

---

## Findings

| # | Sev | Dim | File:line | Finding |
|---|-----|-----|-----------|---------|
| A18-1 | **High** | D3/D9 | `/ratesengine-api`, `/ratesengine-indexer` (repo root, tracked) | **136 MB of stale pre-rebrand build artifacts committed to git.** Both are Mach-O 64-bit arm64 executables (macOS, not the linux/amd64 we ship), 62 MB + 79 MB, added in the Stellar Atlas rebrand sweep (commit `142a4d6a`). They are **tracked** and `git archive HEAD` includes them — so `public-export.sh` ships them into the public OSS repo (verified). `.gitignore` only covers `/ratesengine-ops` + `/ratesengine-aggregator` (themselves stale names — should be `/stellarindex-*`, which IS present). Not a secret leak (stripped binaries), but: (a) bloats every clone/export, (b) leaks the old brand in the public release, (c) build-artifact-in-VCS is a repo-hygiene smell. Fix: `git rm --cached ratesengine-api ratesengine-indexer`; `.gitignore` line 114-115 already-stale `/ratesengine-ops` `/ratesengine-aggregator` are dead (those names no longer build) — the live ignore is `/stellarindex-*`. |
| A18-2 | Medium | D3 | `configs/ansible/roles/patroni/templates/etcd.conf.j2:7-11`, `patroni.yml.j2:16-20` | etcd (the Patroni DCS) runs with no client/peer TLS and no auth (`http://`, no RBAC). The DCS holds the PG superuser password in cleartext and can trigger failover. Mitigated to Medium by firewall: 2379/2380 are `@internal_v4`+loopback only (`patroni/tasks/10-firewall.yml`), so exploitable only from inside the internal network — a missing second layer, not an internet hole. Patroni HA is multi-host future scope (R1 is single-host today). Add etcd TLS+auth before any multi-host Patroni standup. |
| A18-3 | Low | D9 | `scripts/dev/cut-release.sh:21` | Stale comment: "release.yml fires on the tag push and produces the GitHub Release **+ container images**." Contradicts `release.yml:14` ("**Container images are NOT built/pushed by this workflow**", GHCR job dropped 2026-05-11). Comment-only; the script behaves correctly. |
| A18-4 | Low | D1 | `configs/ansible/roles/archival-node/tasks/01-preflight.yml` | Unlike the redis/patroni roles (which `assert` their vault passwords are set), archival-node's preflight does NOT assert `postgres_pass_*` / `minio_root_password` are present. They're vault-only (no `defaults` entry), so an *undefined* var fails closed via templating error — but a *typo'd/empty* vault key would create DB roles with empty passwords instead of failing fast. Add a symmetric vault-presence assert. |
| A18-5 | Low | D3 | `configs/ansible/roles/archival-node/defaults/main.yml:475-476` | `allowed_ssh_cidrs: ["0.0.0.0/0"]` — SSH open to the internet on archival-node (dedicated-host roles default to RFC1918). Tempered by key-only root + fail2ban + 30/min rate-limit; comment already TODOs "tighten to jump host/VPN once that exists." Loosest item in an otherwise drop-default firewall posture. |
| A18-6 | Low | D3 | `configs/ansible/roles/loki/tasks/server-01-preflight.yml:77` (`validate_certs: no`); `loki-config.yaml.j2:36` (`insecure: true`) | TLS verification skipped for the internal MinIO bucket probe + Loki S3 chunk backend. Both target internal-only `http://` MinIO — no real exposure today. The `insecure` flag auto-flips to `false` for `https://`; the preflight `validate_certs: no` is unconditional, so note it if a remote `https://` S3 endpoint is ever wired. |
| A18-7 | Info | D9 | `scripts/dev/verify.sh` | `verify-launch-ready` (and `verify-launch-ready-single-region`) are NOT in the canonical pre-push `verify.sh` gate — they're standalone Makefile targets. Likely intentional (launch-readiness is a release-time gate, not a per-push one), but the CLAUDE.md framing of `make verify` as "the canonical pre-push gate" might lead an agent to assume launch-readiness is covered. Documentation/expectation note only. |
| A18-8 | Info | D3 | `configs/example.toml:54`; `configs/ansible/.../stellarindex.{env,toml}.j2`, `09-minio.yml`, `14-stellarindex-services.yml` | Postgres DSN uses `sslmode=disable` uniformly. Acceptable because every DSN targets `127.0.0.1`/unix-socket (loopback, no network MITM path) and pg_hba is loopback-only with scram-sha-256. Worth a one-line note in the config header that this is loopback-scoped, not a blanket "TLS off." |
| A18-9 | Info | D1 | `configs/caddy/Caddyfile.api:42-66` | Cloudflare trusted-proxy CIDR list is hand-maintained (comment says "refresh from cloudflare.com/ips-v4 quarterly"). Stale CIDRs would either drop real-client-IP resolution or (worse) trust a reclaimed range. Operational maintenance item, not a current defect — flagging the manual-refresh dependency. |
| A18-10 | Info | D3 | `configs/ansible/roles/redis-sentinel/defaults/main.yml:74-75` | Comment claims "the role refuses to bind 0.0.0.0" but no `assert` enforces it. Harmless doc-vs-code drift: `redis_bind_address` defaults to `{{ ansible_host }}` (not 0.0.0.0) and is firewall-gated regardless. |
| A18-11 | Info | D9 | `.github/workflows/api-docs.yml:24-27`; `release.yml` (no `id-token`) | `api-docs.yml` retains `id-token: write` + `pages: write` while `deploy.yml`/`release.yml` correctly dropped `id-token: write` (F-1296). `api-docs` legitimately needs `id-token` for `actions/deploy-pages` (OIDC-based Pages deploy), so this is correct — noting the asymmetry only so a future reader doesn't "fix" it by removing the needed permission. Also: `actions/configure-pages@v6` / `upload-pages-artifact@v5` are tag-pinned (not SHA), but they're first-party `actions/*` (single GitHub trust boundary) so the pinning lint allows it. |

---

## CORRECT — things verified clean / well-built

### D3 — Secrets (no committed secrets found)
- `configs/ansible/inventory/r1.secrets.yml` is **ansible-vault AES256-encrypted AND untracked** (gitignored via `.gitignore` `*.secrets.yml` + `/configs/ansible/inventory/*.secrets.yml`; `git ls-files` shows only `*.example.yml`). Confirmed via `git ls-files --error-unmatch` (errors out).
- `configs/ansible/inventory/r1.yml` is a **gitignored local operator file** (NOT tracked) — its `ratesengine.net`/IP residue never reaches git. `git grep` on tracked files for `ratesengine` returns zero hits in configs/scripts/deploy/.github.
- All role `defaults/main.yml` ship empty-string or omitted credentials with fail-closed preflight `assert`s (redis/patroni). No `changeme`/`admin`-class shipped passwords anywhere.
- All `.j2` templates render secrets from `{{ vault_* }}` / role-var lookups: postgres, redis (`redis.conf`, `sentinel.conf`, `users.acl`), minio (`minio.env`), patroni (superuser/replicator/REST), keepalived vrrp, `stellarindex.env`. Zero hardcoded credential values.
- `pg_hba.conf.j2` — only `peer` (socket) + `127.0.0.1/32`/`::1` with `scram-sha-256`. No `trust`, no `0.0.0.0/0`. Patroni-generated pg_hba scoped to RFC1918 CIDRs.
- `sshd_config.j2` — `PermitRootLogin prohibit-password` (key-only root, per project policy), `PasswordAuthentication no`, `MaxAuthTries 3`, modern ciphers.
- Firewall (`nftables.conf.j2` + all `*-firewall.yml`) — `policy drop`; 5432/6379/26379/9000/2379/2380/8008/9090/9093/9100/3100 all gated to `@internal_v4`+loopback. Only SSH 22 (rate-limited) + HAProxy 80/443 public.
- Every `get_url` binary install has `checksum: sha256:{{...}}` (fail-fast asserted, F-1280/1291/1293); no `curl|bash`.
- `configs/alertmanager/alertmanager.r1.yml` + `apply.sh` — Slack/Healthchecks URLs are `${ENV}` placeholders substituted from off-disk `/etc/default/alertmanager-secrets`; empty → no-op stub.
- `configs/caddy/Caddyfile.api` — no basicauth/bcrypt/inline tokens; TLS via Let's Encrypt.
- `configs/loki/loki.r1.yml` `auth_enabled: false` + `promtail.r1.yml` — bound to localhost (3100/9080), firewall-gated; acceptable single-host.
- `configs/healthchecks/*.sh` — no hardcoded ping URLs/UUIDs; read from 0600 env file.
- `deploy/docker-compose/.env.example` + `dev.yaml` — `stellarindex-dev` passwords are dev-only (referenced only by Makefile `dev`/`down`, no production path).
- `.github/workflows/**` — every secret via `${{ secrets.X }}`; zero hardcoded tokens. No `.pem`/`.key`/`.crt`/Stellar S-seed committed.

### D9 — Alert / metric / runbook parity + release correctness
- **212 alert instances** (106 per dir × 2) ALL carry a `runbook_url:` → an existing `docs/operations/runbooks/<slug>.md`. Zero missing runbooks, zero alerts lacking a runbook annotation.
- **Dual-dir parity:** `deploy/monitoring/rules/` and `configs/prometheus/rules.r1/` carry an **identical 106-alert set** (set-difference empty both ways). Only job-label names differ (expected R1 rewrite, out of scope).
- **No dead alerts:** every `stellarindex_*` expr token resolves to an emitter or a documented `KNOWN_INERT` entry. `scripts/ci/lint-metric-refs.sh` enforces this and passes (exit 0).
- **No `ratesengine` residue** in `configs/prometheus/`, `deploy/monitoring/`, or `docs/operations/runbooks/`. Rebrand fully propagated to monitoring.
- **Release/CHANGELOG correctness:** `release.yml` cross-compiles the correct 6 binaries (`stellarindex-{indexer,aggregator,api,ops,migrate,sla-probe}`) for linux/amd64; CHANGELOG-section extraction uses literal-prefix match (avoids the awk bracket-class trap); fallback to commit summary on empty section; prerelease detection per semver §9. `cut-release.sh` guards: SemVer shape, on-main, clean tree, origin-sync, tag-doesn't-exist, non-empty CHANGELOG section, runs verify.sh. All systemd units + docker images + cmd dirs are `stellarindex-*`.

### D1 — Gate / script completeness
- `verify.sh` covers fmt, vet, lint, lint-docs, lint-imports, openapi-urls, pk-discriminators, monitoring-check (graceful promtool skip + metric-refs fallback), vuln (graceful skip), test, integration-build, and all three web apps (explorer/dashboard/status). Sequential mirror of the parallel CI matrix.
- `r1-smoke.sh` — 40+ GETs across health/catalogue/pricing/vwap/oracle/diagnostics/customer/discovery, with behaviour-pin `expect_status` assertions (400/404 envelope checks), multi-status acceptance for empty-window routes, per-check timeouts, synthetic User-Agent so smoke traffic is excluded from SLO histograms.
- `public-export.sh` — fresh single-commit `git archive` (no history → no historical secret reachable), drops `docs/audit-*` + predecessor-system analysis + r{1,2,3}.yml, genericises prod IP, secret-sweep regex + residual-IP check + `go build` verification before handing off. (Note A18-1: would carry the stale root binaries — separate from this script's logic.)
- `cut-release.sh` — gh-API-based origin checks (works SSH-or-HTTPS), `--dry-run`, explicit-URL push fallback.
- `pre-launch-check.sh` — read-only R1 readiness (listen-addr/CORS/Stripe/HC/AM secrets/timers/services/Caddy/smoke/SECURITY-warns); exit = FAIL count.
- CI lint scripts (`lint-imports.sh`, `lint-actions-pinning.sh`, `lint-metric-refs.sh`) are well-structured with baselines/allowlists/clear failure messages.

### Workflow correctness (hardening)
- `deploy.yml` + `release.yml` pass all `workflow_dispatch` inputs through `env:` (never `${{ inputs.* }}` interpolated into shell — F-1298), validate SemVer + binary names (`[a-zA-Z0-9-]+`, reject path traversal), MITM-safe SSH (fail-fast if pinned `R1_SSH_KNOWN_HOSTS` unset — F-1297, no live ssh-keyscan fallback), verify SHA256SUMS, stage migrations at the exact tag, `id-token: write` removed (F-1296).
- `ci.yml` — `permissions: contents: read` workflow-wide; SHA-pinned third-party actions; checksum-verified promtool/gitleaks installs (F-1294); push-to-main trigger added to close the unprotected-main gap (F-1231); OSS gitleaks CLI (not the paywalled action); pnpm-audit gates on all 3 web apps.
- `release-validate.yml` — discovers binaries from `cmd/`, cross-compiles with release ldflags, smoke-tests `-version`, lints `cut-release.sh`, builds all 6 Dockerfiles; path-filtered to avoid burn.
- **Billing-cap pattern:** `k6-weekly` cron is commented out (Actions spend cap, Task #50), `api-audit` schedule deliberately omitted, `ci.yml` `paths-ignore` skips docs-only PRs — all consistent with the documented budget discipline. No 0-2s billing-cap failure pattern observed in workflow structure.
- Cloudflare deploys (`explorer-deploy`/`docs-deploy`/`status-page`) — all `workflow_dispatch`-only fallbacks (CF git integration is the primary path), `contents: read` + `deployments: write`, SHA-pinned `wrangler-action`, all `stellarindex-*` CF project names.

### Brand
- All 6 systemd `.service`/`.timer` units in `deploy/systemd/` + ansible templates are `stellarindex-*`. ExecStart paths `/usr/local/bin/stellarindex-*`. `example.toml` fully rebranded (`home_domain = stellarindex.io`). All 12 prometheus.r1 scrape jobs `stellarindex-*`/standard-exporter. Only brand residue is the tracked stale binaries (A18-1) and the gitignored local `r1.yml`.

---

## Cross-cutting / root-cause notes
- The single real defect (A18-1) is a **build-artifact-in-VCS** hygiene miss that survived the rebrand because `.gitignore` was updated for the *new* names but the *old* tracked artifacts were never `git rm`'d. Same class as the dead `/ratesengine-ops` `/ratesengine-aggregator` ignore lines.
- Everything else is defense-in-depth (A18-2/4/5/6), doc/comment drift (A18-3/8/10), or maintenance dependencies (A18-9). The security-evidence trail (F-12xx finding IDs threaded through the workflows + ansible) shows this surface has been hardened by prior audit passes; this pass found no regression of those fixes.

---

**Files read (in full or via targeted grep/agent):** ~95 of 308 in-scope files.
Direct full reads: verify.sh, public-export.sh, cut-release.sh, r1-smoke.sh,
pre-launch-check.sh, lint-imports.sh, lint-metric-refs.sh, lint-actions-pinning.sh,
verify-launch-ready/main.go (header), release.yml, deploy.yml, ci.yml,
release-validate.yml, explorer/docs/status/api-docs/api-audit/k6-weekly workflows,
alertmanager.r1.yml, apply.sh, Caddyfile.api, example.toml (header), Makefile (verify).
Agent-driven deep reads: full secret scan of configs/scripts/deploy/.github
(15 tool calls), full alert↔runbook↔metric parity across both rule dirs + all
runbooks (20 tool calls), full ansible role/template/task security review
(~30 of 150 ansible files read + full-tree greps, 34 tool calls).
