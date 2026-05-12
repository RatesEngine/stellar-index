# W19 — Security, secrets, auth, billing

## Scope

Every authentication, authorization, secret, audit-log, billing,
notification, and money path.

In scope:
- `internal/auth/*` (apikey postgres + redis stores, list_keys,
  signup_tracker, store, subject, validators)
- `internal/auth/sep10/*`
- `internal/platform/*` (account, apikey, audit, billing, errors,
  token, usage, user, webhook)
- `internal/platform/postgresstore/*`
- `internal/usage/counter.go`
- `internal/notify/*` (sender, resend, templates, webhook, noop)
- `internal/ratelimit/*` (Redis token bucket)
- `internal/api/v1/dashboardauth/`, `dashboardkeys/`
- `cmd/ratesengine-ops/mint_key.go`, `upgrade_key.go`
- `.gitleaks.toml`, secret scan rules + allowlist
- `cmd/ratesengine-api/main.go` CORS + trusted-proxy
- TLS cert lifecycle (Caddy Let's Encrypt — W18 owns provisioning;
  W19 owns expiry observability)

## Inputs

- `evidence/cross-file-interactions.md` XFI-1213 (trusted-proxy),
  XFI-1214 (apikey postgres↔redis), XFI-1222 (billing/usage),
  XFI-1223 (notify)
- prior 05-02 audit Blend / divergence / F2 closures (re-test)

## Per-file checklist

| File | Role | Tests | Status |
| --- | --- | --- | --- |
| `internal/auth/apikey.go` | API-key type | | |
| `internal/auth/apikey_postgres.go` + `_test.go` | Postgres store | | |
| `internal/auth/apikey_redis.go` + `_test.go` | Redis store + cache | | |
| `internal/auth/list_keys.go` | list path | | |
| `internal/auth/sep10/*` | challenge / verify / JWT | | |
| `internal/auth/sep10.go` | facade | | |
| `internal/auth/signup_tracker.go` | anti-abuse | | |
| `internal/auth/store.go` + `store_redis_test.go` + `store_update.go` + `store_update_test.go` | store interface + updates | | |
| `internal/auth/subject.go` + `_test.go` | identity precedence | | |
| `internal/auth/validators.go` + `_test.go` | input validation | | |
| `internal/auth/errors.go` | typed errors | | |
| `internal/auth/doc.go` | package doc | | |
| `internal/platform/account.go` | account model | | |
| `internal/platform/apikey.go` + `_test.go` | platform-side apikey | | |
| `internal/platform/audit.go` | audit log writer | | |
| `internal/platform/billing.go` | billing surface | | |
| `internal/platform/errors.go` + `_test.go` | platform errors | | |
| `internal/platform/postgresstore/*` | Postgres persistence | | |
| `internal/platform/token.go` | token model | | |
| `internal/platform/usage.go` | usage model | | |
| `internal/platform/user.go` | user model | | |
| `internal/platform/webhook.go` | webhook model | | |
| `internal/platform/doc.go` | package doc | | |
| `internal/usage/counter.go` | per-request increment | | |
| `internal/notify/sender.go` | sender interface | | |
| `internal/notify/resend.go` | Resend adapter | | |
| `internal/notify/templates.go` | email templates (PII redaction) | | |
| `internal/notify/noop.go` | test backend | | |
| `internal/notify/notify_test.go` | tests | | |
| `internal/notify/doc.go` | package doc | | |
| `internal/ratelimit/*` | token bucket (per-key precedence over IP) | | |
| `cmd/ratesengine-ops/mint_key.go` | key minting CLI | | |
| `cmd/ratesengine-ops/upgrade_key.go` | tier change CLI | | |
| `cmd/ratesengine-api/main.go` (auth wiring + CORS + trusted-proxy) | | | |

## Secret hygiene grep

```sh
gitleaks detect --redact --no-banner
# manual greps for common patterns:
grep -RnEi 'sk_(live|test)_[A-Za-z0-9]{20,}' --include='*' . | grep -v '.discovery-repos'
grep -RnEi 'BEGIN [A-Z ]*PRIVATE KEY' --include='*' . | grep -v '.discovery-repos'
grep -RnEi 'aws_(secret_access_key|access_key_id)' --include='*' .
grep -RnEi 'hc-ping.com/[0-9a-f-]+' --include='*' .
```

Expected: zero hits in tracked files. Any hit = critical.

### Vault file glob coverage (F-1207)

`configs/ansible/inventory/r1.secrets.yml` exists locally as an
Ansible vault file but only the `*.example.yml` siblings are
git-tracked. `.gitignore` covers `.env`, `credentials*.json`,
`*.key`, `*.pem`, `*.p12`, `*.pfx`, `*.jks`, `*.keystore` —
but NOT `*.secrets.yml`. Verify:

- `git ls-files` returns no `*.secrets.yml`
- propose explicit `*.secrets.yml` glob in `.gitignore` to
  protect future operators (F-1207)
- if any *.secrets.yml is tracked anywhere, IMMEDIATE critical

## Audit log integrity

- `internal/platform/audit.go` writes to migration 0027 platform schema
- writes are append-only?
- privileged operations (mint-key, upgrade-key, key-revoke,
  backfill against arbitrary range) all logged?
- audit log accessible only to admin?

## Rate-limit identity precedence

- per-API-key > per-IP > anonymous bucket
- internal calls (127.0.0.1) exempt?
- trusted-proxy list bounded (Cloudflare IPs only — ADR-0025)
- X-Forwarded-For spoofing impossible when direct client
  (verify in test)

## SEP-10 robustness

- challenge nonce / one-time-use
- audience check (we vs other-host)
- network passphrase check (mainnet vs testnet)
- JWT expiry enforcement
- replay defence

## Billing surface

- `internal/platform/billing.go` interface
- `internal/usage/counter.go` increments on every request
- meter consistency under crash
- free-tier quota enforcement
- paid-tier overage handling
- Stripe (or other PSP) integration? — EX-1214

## CORS policy

- allowed origins
- credentials flag
- preflight cache duration

## Adversarial vectors

- B1.1..B1.8 authentication / authorization
- B2.1..B2.4 rate-limit bypass
- E1.1..E1.4 privileged commands
- F3.1..F3.3 privacy

## Cross-workstream dependencies

- W03 owns CI gitleaks job (verify it runs)
- W04 owns supply-chain trust
- W11 owns API surface that auth gates protect
- W14 owns alert + runbook for auth failures (api-5xx etc.)
- W18 owns Caddy trusted-proxy provisioning

## Closure criteria

- Every per-file row terminal
- Secret hygiene grep returns zero
- Audit log integrity proven
- Rate-limit identity precedence proven by test
- SEP-10 attacks tested
- Billing surface evaluated end-to-end
- CORS policy bounded
