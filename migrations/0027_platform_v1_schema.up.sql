-- 0027 up — Platform v1 schema.
--
-- Foundational tables for the customer + staff dashboards
-- specified in docs/architecture/platform-spec.md. This migration
-- establishes the schema shape; subsequent migrations add data
-- (backfill from Redis-stored API keys → synthesised accounts)
-- and the runtime hooks (auth flow, key extension, usage events).
--
-- Conservative mid-launch posture: every new table is additive.
-- The existing `auth.RedisAPIKeyStore` keeps validating bearer
-- tokens against `apikey:<sha256>` Redis keys until the platform
-- migration linking those records to `accounts(id)` lands. No
-- breaking changes to the runtime auth path in this migration.
--
-- Tables added:
--   accounts                 — top-level org primitive
--   users                    — humans who log in
--   sessions                 — dashboard cookie sessions
--   magic_link_tokens        — single-use email-link auth tokens
--   api_keys                 — extended replacement for the Redis-only key model
--   api_usage_events         — TimescaleDB hypertable; one row per request
--   subscriptions            — Stripe subscription mirror
--   stripe_event_log         — webhook idempotency + audit
--   invites                  — pending team-member invitations
--   audit_log                — every user/staff action
--   customer_webhooks        — outbound webhooks the customer registers
--   webhook_deliveries       — outbound delivery attempts log
--
-- Why all in one migration: foreign keys cross every table.
-- Splitting would mean shipping half-empty tables that don't
-- constrain anything until the second migration applies; the
-- intermediate state is broken-by-design. Atomic landing keeps
-- the schema coherent at every snapshot.

-- ─── Extensions ────────────────────────────────────────────────────

CREATE EXTENSION IF NOT EXISTS "uuid-ossp";
CREATE EXTENSION IF NOT EXISTS citext;       -- case-insensitive email
-- timescaledb already loaded for trades / prices_1m hypertables

-- ─── 1. accounts ───────────────────────────────────────────────────
--
-- The org primitive. Every key, subscription, webhook, audit-log
-- entry hangs off accounts(id). v1 = one user per account; v2
-- migrates to many-to-many via a future `memberships` table.
--
-- `slug` is the URL-safe handle (e.g. `acme`); auto-generated
-- from `name` at creation but operator-overridable for vanity.
--
-- `tier` mirrors the per-key tier shipping today (free / starter
-- / pro / business / enterprise). Account-level tier sets the
-- default for newly-minted keys but per-key overrides win.

CREATE TABLE accounts (
    id                            uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    name                          text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    slug                          text NOT NULL CHECK (slug ~ '^[a-z0-9][a-z0-9-]{0,62}$'),
    billing_email                 citext NOT NULL,
    stripe_customer_id            text,
    tier                          text NOT NULL DEFAULT 'free'
                                    CHECK (tier IN ('free', 'starter', 'pro', 'business', 'enterprise')),
    status                        text NOT NULL DEFAULT 'active'
                                    CHECK (status IN ('active', 'suspended', 'closed')),
    created_at                    timestamptz NOT NULL DEFAULT now(),
    suspended_at                  timestamptz,
    suspended_reason              text,
    -- Per-account overrides for the per-tier defaults; null = inherit tier.
    rate_limit_per_min_override   int CHECK (rate_limit_per_min_override > 0),
    monthly_request_quota_override bigint CHECK (monthly_request_quota_override > 0)
);

CREATE UNIQUE INDEX accounts_slug_idx ON accounts (slug);
CREATE UNIQUE INDEX accounts_stripe_customer_idx ON accounts (stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;
CREATE INDEX accounts_status_idx ON accounts (status) WHERE status != 'active';

-- ─── 2. users ──────────────────────────────────────────────────────
--
-- Humans with dashboard logins. Single-account-per-user in v1
-- (`account_id` is NOT NULL); multi-org migrates by moving
-- (account_id, role) to a `memberships` table and dropping these
-- columns from users.
--
-- `is_staff` gates the staff dashboard; orthogonal to the
-- per-account `role`. Customer surfaces never see is_staff = true
-- users in their team list.

CREATE TABLE users (
    id                       uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id               uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    email                    citext NOT NULL,
    display_name             text CHECK (length(display_name) <= 200),
    role                     text NOT NULL DEFAULT 'member'
                                CHECK (role IN ('owner', 'admin', 'billing', 'member', 'viewer')),
    email_verified_at        timestamptz,
    last_login_at            timestamptz,
    mfa_enabled              boolean NOT NULL DEFAULT false,
    mfa_secret_enc           bytea,                       -- libsodium-sealed TOTP seed
    mfa_recovery_codes_hashed bytea[],                    -- sha256 hashes; mint 10 at MFA setup
    is_staff                 boolean NOT NULL DEFAULT false,
    created_at               timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX users_email_idx ON users (email);
CREATE INDEX users_account_idx ON users (account_id);
CREATE INDEX users_staff_idx ON users (is_staff) WHERE is_staff = true;

-- ─── 3. sessions ───────────────────────────────────────────────────
--
-- Dashboard cookie sessions. Distinct from API keys: cookie ≠
-- bearer token. The auth middleware that handles the dashboard
-- consults this table; the API auth middleware (handling
-- Authorization: Bearer <key>) consults `api_keys`.
--
-- Geo + IP fields drive the "new login from a new country" email
-- alert. We update last_seen_at on every authenticated request
-- (debounced to once-per-minute to avoid hot-row contention).

CREATE TABLE sessions (
    id              uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    user_id         uuid NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    expires_at      timestamptz NOT NULL,
    revoked_at      timestamptz,
    created_at      timestamptz NOT NULL DEFAULT now(),
    last_seen_at    timestamptz NOT NULL DEFAULT now(),
    ip_first_seen   inet NOT NULL,
    ip_last_seen    inet NOT NULL,
    user_agent      text NOT NULL,
    geo_first_seen  text,
    geo_last_seen   text
);

CREATE INDEX sessions_user_active_idx ON sessions (user_id, expires_at)
    WHERE revoked_at IS NULL;

-- ─── 4. magic_link_tokens ──────────────────────────────────────────
--
-- Single-use tokens for the magic-link login flow + email
-- verification + invite acceptance. We store sha256(token), not
-- the plaintext, so a Redis dump leaks nothing.
--
-- `purpose` distinguishes the three flows so a leaked login
-- token can't be used to accept an invite or vice versa.

CREATE TABLE magic_link_tokens (
    token_hash    bytea PRIMARY KEY,
    email         citext NOT NULL,
    purpose       text NOT NULL CHECK (purpose IN ('login', 'email-verify', 'invite-accept')),
    expires_at    timestamptz NOT NULL,
    consumed_at   timestamptz,
    requested_ip  inet NOT NULL,
    created_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX magic_link_tokens_email_idx ON magic_link_tokens (email);
CREATE INDEX magic_link_tokens_expires_idx ON magic_link_tokens (expires_at);

-- ─── 5. api_keys ───────────────────────────────────────────────────
--
-- Extended replacement for the Redis-only key model. Note: the
-- runtime auth middleware keeps reading from Redis until the
-- migration that mirrors records here lands; this table is
-- written by the dashboard when the customer mints a key, and
-- the Redis-side store gains a write-through to keep both in
-- sync.
--
-- `key_id` is `kid_<hex>` (matches today's format).
-- `key_hash` is sha256(plaintext); same shape as the Redis
-- record's primary key, so the platform-side migration that
-- mirrors existing Redis records reuses the hash directly.
-- `key_prefix` is the first 12 chars of plaintext (matches
-- #707).
--
-- `permissions` JSONB defaults to {"all": true}; per-endpoint
-- enforcement lands when scoped-keys ship in Phase 3.
--
-- Soft-delete via revoked_at. revoked records stay forever for
-- audit + dashboard "rotated last week" UX.

CREATE TABLE api_keys (
    id                          text PRIMARY KEY CHECK (id ~ '^kid_[a-f0-9]{12,}$'),
    account_id                  uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    created_by_user_id          uuid REFERENCES users(id) ON DELETE SET NULL,
    name                        text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    description                 text CHECK (length(description) <= 2000),
    key_hash                    bytea NOT NULL,
    key_prefix                  text NOT NULL CHECK (key_prefix ~ '^rek_[a-f0-9]{8}$'),
    tier                        text NOT NULL
                                  CHECK (tier IN ('apikey', 'partner', 'operator')),
    rate_limit_per_min          int NOT NULL CHECK (rate_limit_per_min > 0),
    monthly_quota               bigint CHECK (monthly_quota IS NULL OR monthly_quota > 0),
    permissions                 jsonb NOT NULL DEFAULT '{"all": true}'::jsonb,
    ip_allowlist                cidr[] NOT NULL DEFAULT '{}',
    referer_allowlist           text[] NOT NULL DEFAULT '{}',
    expires_at                  timestamptz,
    revoked_at                  timestamptz,
    revoked_by_user_id          uuid REFERENCES users(id) ON DELETE SET NULL,
    revoked_reason              text,
    last_used_at                timestamptz,
    last_used_ip                inet,
    last_used_user_agent        text,
    usage_alert_threshold_pct   int CHECK (usage_alert_threshold_pct IS NULL OR
                                            (usage_alert_threshold_pct BETWEEN 1 AND 100)),
    created_at                  timestamptz NOT NULL DEFAULT now()
);

CREATE UNIQUE INDEX api_keys_hash_idx ON api_keys (key_hash);
CREATE INDEX api_keys_account_active_idx ON api_keys (account_id) WHERE revoked_at IS NULL;
CREATE INDEX api_keys_account_all_idx ON api_keys (account_id, created_at DESC);
CREATE INDEX api_keys_prefix_idx ON api_keys (key_prefix);
CREATE INDEX api_keys_expires_idx ON api_keys (expires_at)
    WHERE revoked_at IS NULL AND expires_at IS NOT NULL;

-- ─── 6. api_usage_events ───────────────────────────────────────────
--
-- One row per authenticated request. Hypertable on ts. Ingested
-- async via a Redis stream → worker pattern (the request hot
-- path doesn't write Postgres directly).
--
-- 12-month retention; the dashboard reads from continuous
-- aggregates (api_usage_5m, api_usage_1h, api_usage_1d) which
-- the next migration creates once the table has data flowing.
--
-- `client_ip` is captured at /24 (IPv4) or /48 (IPv6) prefix to
-- stay well clear of "personally identifying" interpretations
-- under GDPR unless the customer opts into full retention.

CREATE TABLE api_usage_events (
    ts          timestamptz NOT NULL,
    account_id  uuid NOT NULL,                 -- FK semantics enforced via app, not constraint, for hypertable speed
    key_id      text NOT NULL,
    route       text NOT NULL,
    method      text NOT NULL,
    status      smallint NOT NULL CHECK (status BETWEEN 100 AND 599),
    duration_ms int NOT NULL CHECK (duration_ms >= 0),
    bytes_out   int NOT NULL DEFAULT 0 CHECK (bytes_out >= 0),
    client_ip   inet,
    geo_country text CHECK (geo_country IS NULL OR length(geo_country) = 2),
    request_id  text
);

SELECT create_hypertable('api_usage_events', 'ts',
    chunk_time_interval => INTERVAL '1 day');

-- 12-month retention. Drops chunks older than this on the
-- background retention worker.
SELECT add_retention_policy('api_usage_events', INTERVAL '12 months');

CREATE INDEX api_usage_events_account_ts_idx
    ON api_usage_events (account_id, ts DESC);
CREATE INDEX api_usage_events_key_ts_idx
    ON api_usage_events (key_id, ts DESC);

-- ─── 7. subscriptions ──────────────────────────────────────────────
--
-- Mirror of Stripe subscription state. Single source of truth
-- for "what tier is this account on, which plan period are they
-- in, are they cancelling at period end."
--
-- One row per account at any time (current_period_end > now()
-- means active; otherwise the next webhook will replace it).

CREATE TABLE subscriptions (
    id                       uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id               uuid NOT NULL REFERENCES accounts(id) ON DELETE RESTRICT,
    stripe_subscription_id   text NOT NULL UNIQUE,
    plan                     text NOT NULL CHECK (plan IN ('starter', 'pro', 'business', 'enterprise')),
    current_period_start     timestamptz NOT NULL,
    current_period_end       timestamptz NOT NULL,
    cancel_at_period_end     boolean NOT NULL DEFAULT false,
    canceled_at              timestamptz,
    created_at               timestamptz NOT NULL DEFAULT now(),
    updated_at               timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX subscriptions_account_idx ON subscriptions (account_id);
CREATE INDEX subscriptions_period_idx ON subscriptions (current_period_end DESC);

-- ─── 8. stripe_event_log ───────────────────────────────────────────
--
-- Webhook idempotency + audit. Stripe retries; we dedupe by
-- stripe_event_id. Processed_at tracks whether we successfully
-- handled the event (null = received but not yet processed).

CREATE TABLE stripe_event_log (
    stripe_event_id  text PRIMARY KEY,
    type             text NOT NULL,
    received_at      timestamptz NOT NULL DEFAULT now(),
    processed_at     timestamptz,
    error            text,
    payload          jsonb NOT NULL
);

CREATE INDEX stripe_event_log_unprocessed_idx ON stripe_event_log (received_at)
    WHERE processed_at IS NULL;

-- ─── 9. invites ────────────────────────────────────────────────────
--
-- Pending team-member invitations. Token-hash key matches the
-- magic_link_tokens shape so a single email-link consumption
-- handles both magic-login and invite-accept flows.

CREATE TABLE invites (
    token_hash         bytea PRIMARY KEY,
    account_id         uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    email              citext NOT NULL,
    role               text NOT NULL CHECK (role IN ('admin', 'billing', 'member', 'viewer')),
    invited_by_user_id uuid NOT NULL REFERENCES users(id) ON DELETE RESTRICT,
    expires_at         timestamptz NOT NULL,
    accepted_at        timestamptz,
    revoked_at         timestamptz,
    created_at         timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX invites_account_idx ON invites (account_id);
CREATE INDEX invites_email_idx ON invites (email);
CREATE INDEX invites_pending_idx ON invites (expires_at)
    WHERE accepted_at IS NULL AND revoked_at IS NULL;

-- ─── 10. audit_log ─────────────────────────────────────────────────
--
-- Every user / staff / system action. Append-only; no UPDATE or
-- DELETE except the retention policy.
--
-- 12-month online retention; older rows archived to S3 by an
-- offline job (separate concern, not in this migration).

CREATE TABLE audit_log (
    id              uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id      uuid REFERENCES accounts(id) ON DELETE SET NULL,
    actor_user_id   uuid REFERENCES users(id) ON DELETE SET NULL,
    actor_kind      text NOT NULL CHECK (actor_kind IN ('user', 'staff', 'system', 'webhook')),
    action          text NOT NULL CHECK (length(action) BETWEEN 1 AND 100),
    target_kind     text,
    target_id       text,
    metadata        jsonb,
    ip              inet,
    user_agent      text,
    ts              timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX audit_log_account_ts_idx ON audit_log (account_id, ts DESC);
CREATE INDEX audit_log_actor_ts_idx ON audit_log (actor_user_id, ts DESC)
    WHERE actor_user_id IS NOT NULL;
CREATE INDEX audit_log_action_idx ON audit_log (action, ts DESC);

-- ─── 11. customer_webhooks ─────────────────────────────────────────
--
-- Outbound webhooks the customer registers to receive events.
-- HMAC-signed deliveries; retry log is in webhook_deliveries.

CREATE TABLE customer_webhooks (
    id            uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    account_id    uuid NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    name          text NOT NULL CHECK (length(name) BETWEEN 1 AND 200),
    url           text NOT NULL CHECK (url ~ '^https://'),
    secret_hash   bytea NOT NULL,                 -- HMAC-SHA-256 signing key
    events        text[] NOT NULL CHECK (array_length(events, 1) > 0),
    enabled       boolean NOT NULL DEFAULT true,
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX customer_webhooks_account_idx ON customer_webhooks (account_id);

-- ─── 12. webhook_deliveries ────────────────────────────────────────
--
-- Per-attempt delivery log. Stripe-style: signed deliveries,
-- exponential retry over 72h (15 attempts), customer-visible
-- delivery list in dashboard.

CREATE TABLE webhook_deliveries (
    id               uuid PRIMARY KEY DEFAULT uuid_generate_v4(),
    webhook_id       uuid NOT NULL REFERENCES customer_webhooks(id) ON DELETE CASCADE,
    event_type       text NOT NULL,
    payload          jsonb NOT NULL,
    attempt_count    int NOT NULL DEFAULT 0 CHECK (attempt_count >= 0),
    next_attempt_at  timestamptz,
    delivered_at     timestamptz,
    last_error       text,
    last_response_status int,
    created_at       timestamptz NOT NULL DEFAULT now()
);

CREATE INDEX webhook_deliveries_webhook_idx ON webhook_deliveries (webhook_id, created_at DESC);
CREATE INDEX webhook_deliveries_pending_idx ON webhook_deliveries (next_attempt_at)
    WHERE delivered_at IS NULL AND next_attempt_at IS NOT NULL;
