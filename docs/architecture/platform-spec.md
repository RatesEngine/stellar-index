---
title: Platform spec — customer + staff dashboards, billing, full lifecycle
last_verified: 2026-05-05
status: design proposal
---

# Platform spec — customer + staff dashboards, billing, full lifecycle

## Goals

Take the system from "API + showcase shell" to a **fully-featured
developer platform**: customers self-serve from signup → key
management → usage analytics → billing → integration; staff get
the operations cockpit needed to run the platform at scale.

Reference points: Stripe, Twilio, Mailgun, Tinybird, Plaid. Pick
the ergonomics from those that fit a small price-data API.

## Non-goals (v1)

- Multi-region session replication — sessions can pin to one
  region; if R1 dies the user re-logs-in.
- Mobile apps — defer indefinitely; the dashboard is responsive
  web-only.
- SSO / SCIM — defer to "Enterprise" tier; only a tiny share of
  customers need it before they're paying $$$.
- API SDKs in N languages — `pkg/client` (Go) ships v1.0; others
  follow demand.

## Primitives

```
Account ──< User ──< Session
    │
    ├─< APIKey ──< UsageEvent
    │
    ├─< Subscription ── Stripe
    │
    └─< Webhook ──< WebhookDelivery
```

The hierarchy is **account-first**: keys, subscriptions, and team
members all hang off `accounts`. A user can belong to one account
in v1 (multi-org for v2).

---

## 1. Authentication

### 1.1 Customer auth — magic-link primary, code fallback

Two paths from the same email:

**Magic link.** Sent on `POST /v1/auth/login` with `{email}`.
Body of the email contains:
- A one-tap link → `https://ratesengine.net/auth/callback?token=<...>`
- A 6-digit numeric code (paste-friendly on mobile, terminal SSH, etc.)

Token is a 32-byte random; SHA-256 hash stored in
`magic_link_tokens` with `expires_at = now() + 15min` and
`consumed_at`. Single-use; second consumption returns 410 Gone.

The code is the same token, base32-encoded → 6 digits derived
from the first 24 bits. Both paths consume the same row.

**SEP-10.** Already implemented. Stays as an enterprise
differentiator ("log in with your Stellar wallet — no email
needed"). Routed through the same session-issuing primitive.

**Why not OAuth?** GitHub / Google adds two providers, two
secret rotations, and two OAuth review applications. Magic link
covers 95% of dev workflows. Defer until churn shows it's needed.

### 1.2 Sessions

Server-issued, opaque session ID stored in:
- HttpOnly + Secure + SameSite=Lax cookie on `ratesengine.net`
- 30-day rolling expiry; touched on every authenticated request

`sessions` table:

```sql
CREATE TABLE sessions (
    id            uuid PRIMARY KEY,
    user_id       uuid NOT NULL REFERENCES users(id),
    expires_at    timestamptz NOT NULL,
    revoked_at    timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now(),
    last_seen_at  timestamptz NOT NULL DEFAULT now(),
    ip_first_seen inet NOT NULL,
    ip_last_seen  inet NOT NULL,
    user_agent    text NOT NULL,
    -- Trigger email when a session starts from a country/ASN
    -- different from the previous one.
    geo_first_seen text,
    geo_last_seen  text
);
```

Sessions are distinct from API keys: dashboard cookie ≠ bearer
token. Route handlers that accept either declare both auth modes.

### 1.3 Account / user data model

```sql
CREATE TABLE accounts (
    id                uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    name              text NOT NULL,                -- "Acme Corp"
    slug              text NOT NULL UNIQUE,         -- "acme"
    billing_email     text NOT NULL,
    stripe_customer_id text,                        -- populated on first paid signup
    tier              text NOT NULL DEFAULT 'free', -- free / starter / pro / business / enterprise
    status            text NOT NULL DEFAULT 'active', -- active / suspended / closed
    created_at        timestamptz NOT NULL DEFAULT now(),
    suspended_at      timestamptz,
    suspended_reason  text,
    -- Enterprise / pre-launch features
    rate_limit_per_min_override int,    -- when set, replaces the tier default
    monthly_request_quota_override int  -- same
);

CREATE TABLE users (
    id                 uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id         uuid NOT NULL REFERENCES accounts(id),
    email              citext NOT NULL UNIQUE,
    display_name       text,
    role               text NOT NULL DEFAULT 'member',  -- owner / admin / billing / member / viewer
    email_verified_at  timestamptz,
    last_login_at      timestamptz,
    mfa_enabled        bool NOT NULL DEFAULT false,
    mfa_secret_enc     bytea,            -- libsodium-sealed TOTP seed
    mfa_recovery_codes_hashed text[],    -- sha256 hashes; 10 codes minted at MFA setup
    created_at         timestamptz NOT NULL DEFAULT now(),
    is_staff           bool NOT NULL DEFAULT false   -- gates the staff dashboard; never settable from the customer surface
);

CREATE TABLE magic_link_tokens (
    token_hash    bytea PRIMARY KEY,
    email         citext NOT NULL,
    purpose       text NOT NULL,            -- "login" / "email-verify" / "invite-accept"
    expires_at    timestamptz NOT NULL,
    consumed_at   timestamptz,
    requested_ip  inet NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);
```

### 1.4 Roles

Per-account roles, enforced at every dashboard endpoint:

| Role     | View | Mint key | Revoke key | Edit billing | Invite users | View staff data |
|----------|:----:|:--------:|:----------:|:------------:|:------------:|:---------------:|
| owner    |  ✓   |    ✓     |     ✓      |      ✓       |      ✓       |        —        |
| admin    |  ✓   |    ✓     |     ✓      |      —       |      ✓       |        —        |
| billing  |  ✓   |    —     |     —      |      ✓       |      —       |        —        |
| member   |  ✓   |    ✓     |     ✓ (own only) | —     |      —       |        —        |
| viewer   |  ✓   |    —     |     —      |      —       |      —       |        —        |

Staff is a separate flag, not a role — orthogonal to the
account hierarchy.

### 1.5 MFA

TOTP only (no SMS — doesn't add security, adds cost). Setup flow:
1. User scans QR code → confirms with one valid OTP → `mfa_enabled = true`
2. 10 single-use recovery codes shown once, hashed and stored
3. Login flow becomes: email → magic link → OTP

**Mandatory for owners + billing role on paid plans**, optional
for everyone else. Mandatory for staff regardless.

---

## 2. API keys (extended)

Today's `auth.APIKeyRecord` carries `KeyID`, `Identifier`, `Tier`,
`RateLimitPerMin`, `Label`, `CreatedAt`. We extend significantly:

```sql
CREATE TABLE api_keys (
    id                  text PRIMARY KEY,           -- "kid_abc123..." stable
    account_id          uuid NOT NULL REFERENCES accounts(id),
    created_by_user_id  uuid REFERENCES users(id),
    name                text NOT NULL,              -- "Production scraper"
    description         text,
    key_hash            bytea NOT NULL UNIQUE,      -- sha256(plaintext)
    key_prefix          text NOT NULL,              -- "rek_4f9c1d8b" - first 12 chars - shown in UI for identification
    tier                text NOT NULL,              -- inherits account.tier at create time; pinnable
    rate_limit_per_min  int NOT NULL,
    monthly_quota       int,                        -- null = inherits plan default
    permissions         jsonb NOT NULL DEFAULT '{"all": true}',
    ip_allowlist        cidr[] NOT NULL DEFAULT '{}',  -- empty = no restriction
    referer_allowlist   text[] NOT NULL DEFAULT '{}',  -- empty = no restriction
    expires_at          timestamptz,                -- null = no expiry
    revoked_at          timestamptz,
    revoked_by_user_id  uuid REFERENCES users(id),
    revoked_reason      text,
    last_used_at        timestamptz,                -- updated by middleware (debounced)
    last_used_ip        inet,
    last_used_user_agent text,
    usage_alert_threshold_pct int,                  -- email when monthly usage hits N% of quota
    created_at          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX api_keys_account_active_idx
  ON api_keys (account_id) WHERE revoked_at IS NULL;
```

### 2.1 Lifecycle

| Operation | Endpoint | Notes |
|-----------|----------|-------|
| Mint | `POST /v1/account/keys` | Returns plaintext **once**; key_prefix in response for client to display in UI alongside the plaintext |
| List | `GET /v1/account/keys` | Returns prefix + metadata; never plaintext |
| Get | `GET /v1/account/keys/{id}` | Same |
| Edit | `PATCH /v1/account/keys/{id}` | Rename / change rate limit / IP allowlist / expiry |
| Rotate | `POST /v1/account/keys/{id}/rotate` | Atomic: mint new + revoke old, returns new plaintext |
| Revoke | `DELETE /v1/account/keys/{id}` | Soft-delete via `revoked_at`; key fails 401 immediately |
| Test | `GET /v1/auth/whoami` | Returns Subject if the bearer key is live |

### 2.2 Permissions (scoped keys)

`permissions` JSON shape:

```json
{
  "all": false,
  "allow": [
    {"endpoint": "GET /v1/price"},
    {"endpoint": "GET /v1/oracle/*"},
    {"endpoint_prefix": "/v1/account/"}
  ],
  "deny": [
    {"endpoint": "POST /v1/account/keys"}
  ]
}
```

Default: `{"all": true}` — full surface. Scoped keys are
opt-in; the dashboard shows a checkbox grid keyed off the
OpenAPI spec.

### 2.3 IP / referer allowlists

Server-side check: when set, requests outside the CIDR list →
403 with `type: ip-not-allowlisted`, `detail: <client-ip> not in
allowlist`. Helps customers ship "this key only works from our
office" or "this key only works from our scraper VM."

Referer allowlist for browser-side keys: same idea. Origin
header must match one of the allowlisted entries (exact or
glob-prefix).

### 2.4 Expiry

Optional `expires_at`. Background sweeper job runs every 5 min:
keys past their expiry get `revoked_at = expires_at`,
`revoked_reason = 'expired'`. Customer gets email at T-7d, T-1d,
T-0.

### 2.5 Plaintext display rules

The plaintext **is shown exactly once** — at creation time, in
the JSON response. Dashboard then displays a single "Copy"
button next to it. After the user navigates away, the plaintext
is gone forever (we only store `key_hash`).

`key_prefix` (first 12 chars, e.g. `rek_4f9c1d8b`) is shown
permanently so users can identify keys by prefix in their own
secret managers.

---

## 3. Usage tracking & analytics

### 3.1 Event ingestion

Every authenticated request emits one usage event. Cost-conscious:
do **not** write to Postgres in the request hot path — buffer
via Redis stream, drain async to Timescale.

`api_usage_events` (TimescaleDB hypertable on `ts`):

```sql
CREATE TABLE api_usage_events (
    ts          timestamptz NOT NULL,
    account_id  uuid NOT NULL,
    key_id      text NOT NULL,
    route       text NOT NULL,        -- "/v1/price" - the registered pattern, NOT the URL
    method      text NOT NULL,
    status      smallint NOT NULL,
    duration_ms int NOT NULL,
    bytes_out   int NOT NULL,
    client_ip   inet,                  -- redacted to /24 for IPv4, /48 for IPv6 if customer opts in
    geo_country text,                  -- ISO 3166-1 alpha-2 from CF-IPCountry
    request_id  text                   -- for cross-correlation with traces
);

SELECT create_hypertable('api_usage_events', 'ts');
SELECT add_retention_policy('api_usage_events', INTERVAL '12 months');
```

### 3.2 Continuous aggregates

Pre-compute the dashboard queries:

```sql
CREATE MATERIALIZED VIEW api_usage_5m WITH (timescaledb.continuous) AS
SELECT time_bucket('5m', ts) AS bucket,
       account_id, key_id, route, method, status,
       count(*) AS requests,
       sum(duration_ms) AS total_duration_ms,
       sum(bytes_out) AS total_bytes,
       percentile_disc(0.95) WITHIN GROUP (ORDER BY duration_ms) AS p95_ms,
       percentile_disc(0.99) WITHIN GROUP (ORDER BY duration_ms) AS p99_ms
FROM api_usage_events
GROUP BY bucket, account_id, key_id, route, method, status;

-- Plus _1h and _1d variants for the longer chart windows.
```

### 3.3 Customer dashboard widgets

**Account overview:**
- "Today: 14,213 requests, 0.04% error rate, p95 12 ms"
- 30-day request chart (stacked by tier endpoint group)
- Top 10 endpoints by volume
- Top 10 endpoints by error count

**Per-key drill-down:**
- Same widgets, scoped to a single key
- Top 10 origin IPs (operator-anonymised)
- Top user-agents

**Quota progress bar:**
- "1.2M / 5M monthly quota used (24%)"
- Projected exhaustion: "at current rate, you'll hit your cap in 18 days"
- Upgrade CTA when projected over

### 3.4 API endpoints powering the dashboard

```
GET /v1/account/usage
    → returns 30d daily series for the authed account

GET /v1/account/keys/{id}/usage?from=&to=&granularity=
    → returns scoped time-series for one key

GET /v1/account/usage/top-endpoints?window=30d
    → ranked list

GET /v1/account/usage/errors?window=24h
    → ranked list of (endpoint, status_code, count)
```

These are fast (CAGG-served) and rate-limited like everything else.

---

## 4. Billing

Stripe-driven. We already have webhook infrastructure (#669);
extend significantly.

### 4.1 Plans

| Plan | Cost | Rate limit | Monthly quota | Notes |
|------|------|------------|---------------|-------|
| Free | $0 | 60 / IP-min | 100K | No key required; IP-bucket |
| Starter | $0 | 1,000 / key-min | 1M | Self-service; one key |
| Pro | $99/mo | 10,000 / key-min | 25M | Multiple keys; usage analytics |
| Business | $499/mo | 50,000 / key-min | 200M | Slack channel; 24h SLA |
| Enterprise | Custom | Custom | Custom | SEP-10 / multi-tenant / dedicated capacity |

**Overage policy:** soft cap with email alerts at 80% / 100%.
Hard cap optional per-account (default off — we'd rather charge
metered overage at $0.10 per 10K than break a customer's
production system mid-incident).

### 4.2 Stripe model

- **One Stripe Customer per Account** (`accounts.stripe_customer_id`)
- **One Subscription per Account**, with two products:
  - Subscription product: monthly recurring at plan price
  - Metered product: overage requests, usage-reported via Stripe Metered Billing API every hour from a worker
- **Invoices**: monthly. Subscription billed in advance, overage billed in arrears.
- **Payment methods**: managed via embedded **Stripe Customer Portal** — not built in-house.

### 4.3 Webhook events to handle

We already handle `checkout.session.completed`. Add:

- `customer.subscription.created` — wire `accounts.tier`
- `customer.subscription.updated` — handle plan change / cancel-at-period-end
- `customer.subscription.deleted` — downgrade to Free (don't suspend; let them spin back up)
- `invoice.payment_failed` — first failure → email + dashboard banner; second failure (3d later) → suspend
- `invoice.payment_succeeded` — clear any "payment failed" state
- `customer.updated` — sync billing_email

All idempotent (Stripe retries; we dedupe via `stripe_event_id`).

### 4.4 Dashboard widgets

**Subscription panel:**
- Current plan + price
- Next invoice date + projected amount (subscription + overage so far)
- "Manage payment / change plan / view invoices" → Stripe Customer Portal
- Cancel at period end toggle

**Invoices section:**
- Latest 12 invoices (PDF download links to Stripe-hosted)
- Status: paid / pending / failed

**Plan comparison:**
- Visible from the account settings; one-click upgrade routes
  through Stripe Checkout → webhook → tier change.

### 4.5 Internal tracking tables

```sql
CREATE TABLE subscriptions (
    id                       uuid PRIMARY KEY,
    account_id               uuid NOT NULL REFERENCES accounts(id),
    stripe_subscription_id   text NOT NULL UNIQUE,
    plan                     text NOT NULL,
    current_period_start     timestamptz NOT NULL,
    current_period_end       timestamptz NOT NULL,
    cancel_at_period_end     bool NOT NULL DEFAULT false,
    canceled_at              timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE stripe_event_log (
    stripe_event_id text PRIMARY KEY,    -- idempotency
    type            text NOT NULL,
    received_at     timestamptz NOT NULL DEFAULT now(),
    processed_at    timestamptz,
    payload         jsonb NOT NULL
);
```

---

## 5. Notifications

### 5.1 Email (transactional)

Use **Resend** (operator-locked decision 2026-05-05). Modern
React-Email-friendly templating, generous free tier (100 / day,
3k / month), and a clean Go SDK. Abstracted behind an
`internal/notify` package so the provider can swap if Resend
ever falls over (Postmark + Mailgun are interface-compatible
fallbacks).

Templates:
- `welcome` — sent on first signup
- `magic-link` — every login
- `key-minted` — sent when a new key is created (with the prefix, NOT the plaintext)
- `key-revoked` — when revoked
- `usage-alert-80pct` — when monthly usage hits 80% of quota
- `usage-alert-100pct` — at 100%
- `payment-failed` — invoice failure
- `subscription-changed` — plan up/down
- `incident-update` — when an active SEV-1/2 incident matches the customer's affected components

### 5.2 In-app

Dashboard notification bell + count badge:
- Persistent until dismissed
- Same trigger conditions as email; user can opt-in/out per channel

### 5.3 Customer webhooks

For high-volume customers wanting machine-to-machine notifications:

```sql
CREATE TABLE customer_webhooks (
    id            uuid PRIMARY KEY,
    account_id    uuid NOT NULL REFERENCES accounts(id),
    name          text NOT NULL,
    url           text NOT NULL,
    secret_hash   bytea NOT NULL,    -- HMAC-SHA-256 signing key
    events        text[] NOT NULL,   -- ['key.minted', 'invoice.paid', ...]
    enabled       bool NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE webhook_deliveries (
    id            uuid PRIMARY KEY,
    webhook_id    uuid NOT NULL REFERENCES customer_webhooks(id),
    event_type    text NOT NULL,
    payload       jsonb NOT NULL,
    attempt_count int NOT NULL DEFAULT 0,
    next_attempt_at timestamptz,
    delivered_at  timestamptz,
    last_error    text,
    created_at    timestamptz NOT NULL DEFAULT now()
);
```

Stripe-style: signed deliveries, exponential retry over 72h
(15 attempts), customer-visible delivery log in dashboard.

---

## 6. Team management

### 6.1 Invites

Owner / admin invites a team member by email. Server creates a
`magic_link_tokens` row with `purpose='invite-accept'`,
`expires_at = now() + 7d`, attached to a pending invite row:

```sql
CREATE TABLE invites (
    token_hash       bytea PRIMARY KEY,
    account_id       uuid NOT NULL REFERENCES accounts(id),
    email            citext NOT NULL,
    role             text NOT NULL,
    invited_by_user_id uuid NOT NULL REFERENCES users(id),
    expires_at       timestamptz NOT NULL,
    accepted_at      timestamptz,
    revoked_at       timestamptz,
    created_at       timestamptz NOT NULL DEFAULT now()
);
```

The invited user clicks the link → magic-link verifies email →
`users` row created with `account_id` + `role` from the invite.

### 6.2 Role changes

Owner/admin can promote/demote. Cannot demote the last owner (UI
guard; server-side enforced).

### 6.3 Audit log

```sql
CREATE TABLE audit_log (
    id          uuid PRIMARY KEY DEFAULT gen_random_uuid(),
    account_id  uuid NOT NULL REFERENCES accounts(id),
    actor_user_id uuid REFERENCES users(id),     -- null for system actions
    actor_kind  text NOT NULL,                    -- 'user' / 'staff' / 'system' / 'webhook'
    action      text NOT NULL,                    -- 'key.mint' / 'plan.upgrade' / etc.
    target_kind text,                              -- 'api_key' / 'invoice' / 'user'
    target_id   text,
    metadata    jsonb,
    ip          inet,
    user_agent  text,
    ts          timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_account_ts_idx ON audit_log (account_id, ts DESC);
```

Dashboard surfaces a 90-day rolling view; staff can query the
full retention (12mo).

---

## 7. Staff dashboard

Strict separation from customer dashboard. Hosted at
`admin.ratesengine.net` (separate Cloudflare Pages project),
auth via:

- Staff users (`users.is_staff = true`) only — separate login flow
- TOTP **mandatory**
- IP allowlist (operator office IPs / VPN ranges)
- 30-minute idle timeout (vs 30-day for customers)
- Every action audit-logged

### 7.1 Views

**Accounts list:**
- Sortable / filterable by tier, status, signup date, MRR, recent activity
- Quick-search by email / account name / key prefix

**Account detail:**
- All users with their roles
- All keys with full metadata (incl. revoked)
- 90d usage chart
- Stripe customer link (deep-link to Stripe dashboard)
- Subscription history
- Audit log
- Internal notes (staff-only annotations: "VIP customer", "Reached out 2026-04-12 about pricing")

**Key search:**
- Paste a `kid_…` or prefix → resolves to the owning account

**Usage anomaly explorer:**
- Top 50 accounts by 5xx errors over last 24h
- Top 50 accounts by latency p95
- Top 50 accounts by request volume (spike detection)
- Filter by endpoint pattern

**Revenue dashboard:**
- MRR / ARR line charts (current + 12mo trailing)
- Net new / churn / upgrade / downgrade per month
- Cohort retention table (signups in week N, % still paying at month M)
- LTV/CAC if we ever wire in CAC data

**Operations cockpit:**
- View all firing alerts (mirror of `/v1/status`)
- Suspend account (immediate; reason required → audit log)
- Reset rate limit (one-time grace)
- Issue refund (proxies to Stripe; requires manager-approval workflow for >$X)
- Manually rotate a key (compromise response — invalidates immediately, mints replacement, emails customer)
- Force-reload aggregator config
- Trigger maintenance mode banner on customer dashboard

**Per-staff audit log:**
- Every action a staff member takes is logged
- "Who suspended this account?" / "Who issued this refund?"
- Read-only view, exportable to CSV for compliance

### 7.2 Endpoints

Mounted under `/admin/` in the API; require `users.is_staff = true`:

```
GET  /admin/accounts
GET  /admin/accounts/{id}
POST /admin/accounts/{id}/suspend
POST /admin/accounts/{id}/unsuspend
POST /admin/accounts/{id}/notes
GET  /admin/keys/search?prefix=
POST /admin/keys/{id}/rotate              # compromise response
GET  /admin/usage/top-by-errors
GET  /admin/usage/top-by-latency
GET  /admin/revenue/mrr
GET  /admin/audit-log
```

All write endpoints require an `X-Reason` header captured into
the audit log.

---

## 8. Security & compliance

### 8.1 Encryption

- TLS everywhere (Caddy / Cloudflare in front)
- Secrets at rest: libsodium sealed boxes for sensitive blobs
  (TOTP seeds, customer webhook secrets); managed via a single
  master key in operator-supplied env var
- Postgres at-rest encryption: trust the disk subsystem (LUKS on
  R1) for v1; future: per-table TDE if Enterprise customers
  demand

### 8.2 Audit retention

- `audit_log`: 12 months online, 7 years archived to S3
- `api_usage_events`: 12 months hot, then dropped (customers can
  export their own data anytime)

### 8.3 GDPR / data subject rights

Endpoints (rate-limited heavily):

- `GET /v1/account/data-export` — async job; emailed download
  link when ready (~1h for power users)
- `DELETE /v1/account` — schedules deletion in 30d; reversible
  during the window. After 30d: account_id stays for foreign
  keys but PII fields are nulled / hashed.

### 8.4 Compliance docs

- Terms of Service, Privacy Policy, DPA (Data Processing
  Agreement) hosted on the marketing site
- SOC 2 Type II prep: audit logging ✓, access controls ✓,
  encryption-at-rest ✓ (above), backup ✓ (operator runbook),
  vendor list, BCP/DR plan

---

## 9. Documentation upgrades

For "platform" credibility:

- **Interactive playground** — Redocly Try-It is good; Stoplight
  Elements is best (live curl + multiple language samples)
- **Code samples in 6 languages** auto-generated from OpenAPI:
  curl, Go, Python, JavaScript, Ruby, PHP, Rust
- **Quickstarts** —
  - "Your first request in 60 seconds"
  - "Build a price ticker in 5 minutes"
  - "Stream prices via SSE"
  - "Verify oracle freshness"
- **Tutorials** matched to common use cases — wallets, DeFi
  dashboards, market-making, accounting
- **Changelog** — already exists; surface RSS feed
- **API status feed** — beyond `/v1/status`, a customer-facing
  RSS / webhook feed for incidents

---

## 10. Implementation phases

Priority-ordered. Each phase ships independently; no big-bang.

### Phase 1 — MVP customer dashboard (4–6 weeks)

- Magic-link login + sessions
- accounts/users tables + migration
- Backfill: existing API keys → synthesise an `account` per
  unique `identifier`; existing user surface becomes a one-user
  account with `role=owner`
- Customer dashboard at `app.ratesengine.net`:
  - List/mint/edit/revoke keys with names + descriptions
  - Basic usage chart (today + 30d, requests count + error rate)
  - Account profile + email change
- `api_usage_events` ingestion (Redis stream → Timescale worker)
- 5m + 1h CAGGs

**Ship gate:** existing API customers can log in, see their
keys, and watch usage in near-real-time.

### Phase 2 — Billing (3–4 weeks)

- Stripe Subscriptions wired
- Plan-comparison + checkout flow
- Customer Portal embed
- Subscription / invoice tables + dashboard widgets
- Webhook handlers for the 6 events listed in §4.3
- Hourly metered-usage reporter to Stripe

**Ship gate:** Pro / Business plans purchasable end-to-end;
overage metering verified.

### Phase 3 — Key features (2–3 weeks)

- IP allowlist enforcement at middleware
- Referer allowlist
- Per-key permissions (scoped keys)
- Auto-expiry sweeper
- Email notifications (welcome, magic link, key-minted,
  usage-alert, payment-failed)

**Ship gate:** customers can mint scoped keys with expiry.

### Phase 4 — Team & audit (2 weeks)

- Invites + role enforcement
- Audit log + dashboard view
- 2FA / TOTP setup (mandatory for paid owners)

**Ship gate:** an Acme-Corp account can have 5 developers each
with their own login.

### Phase 5 — Staff dashboard (3 weeks)

- Separate `admin.ratesengine.net` Pages project
- Account / user / key search
- Usage anomaly explorer
- Revenue dashboard (MRR / ARR / cohorts)
- Suspend / refund / force-rotate operations
- Per-staff audit log

**Ship gate:** on-call can resolve "this customer says their
key is leaked" in under 5 minutes.

### Phase 6 — Polish (continuous)

- Customer webhooks (event delivery)
- Documentation upgrades (playground, samples, tutorials)
- GDPR endpoints
- SOC 2 prep tasks

---

## 11. Open questions

These need design-decision pass before implementation, not
before this spec ships:

1. **Single-org vs multi-org users.** v1 = one account per user.
   v2 = a user can be member of N accounts. Affects every
   permission check. Decision: v1 single, design v2 migration
   path now (account_id + role moves to a `memberships` table).

2. **Key-format compatibility.** Existing `rek_…` keys stay
   valid forever. New format extensions (e.g. environment
   prefix `rek_live_…` / `rek_test_…`) apply only to newly-minted
   keys.

3. **Free-tier abuse vector.** Current anonymous tier is
   IP-bucket 60 r/min. If we open self-service signup at 1k r/min
   per key, attackers can register 1000 emails. Mitigations:
   email verification gate before key issuance, no key issuance
   without payment method on file for >Starter.

4. **Multi-region session pinning.** v1 sticky to R1; v2 (R2/R3)
   needs Redis-replicated sessions or JWT sessions. Probably JWT
   when we cross that bridge — opaque sessions don't multi-region
   without a global Redis.

5. **Usage event sampling.** At sustained 100k req/s, even Redis
   stream → worker → Timescale has cost. v1 = full fidelity;
   when volume warrants, head-sample 1:N for the high-volume
   anonymous tier.

---

## 12. Touchpoints with existing code

What we already have that we extend, vs what's brand new:

| Component | Current state | Phase | Action |
|-----------|---------------|-------|--------|
| `auth.APIKeyRecord` | `KeyID/Identifier/Tier/RateLimit/Label` | 1 | Extend with all the new fields; migrate existing keys |
| `RedisAPIKeyStore` | Redis-only key store | 1 | Move to Postgres (`api_keys` table) — Redis becomes write-through cache |
| `/v1/account/keys` | list / mint / 503 elsewhere | 1 | Add edit / rotate / revoke; rename param `label` → `name` |
| `/v1/signup` | email → key, idempotent | 1 | Becomes "create account + first user + first key" |
| `RateLimitKey` | per-subject Redis counter | 1 | Stays; usage event log is parallel |
| `StripeWebhookConfig` | `checkout.session.completed` only | 2 | Extend to 6 events; idempotency via `stripe_event_log` |
| Showcase `/account` | paste-key dashboard | 1 | Replaced by full dashboard at `app.ratesengine.net` |
| Showcase `/signup` | email → key form | 1 | Becomes magic-link request form |
| `/v1/account/usage` | always returns `[]` | 1 | Wires up the CAGG-fed reader |
| `/v1/status` | already shipped | — | Stays as-is |
| `obs.HTTPMetrics` | route/method/status histogram | 1 | Extend to also enqueue a Redis-stream usage event |
