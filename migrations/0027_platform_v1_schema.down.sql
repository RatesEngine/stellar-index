-- 0027 down — reverse the platform v1 schema.
--
-- Drops in reverse-FK order. Reversible: this migration is
-- intended to be run in dev / staging during platform-spec
-- iteration. Once Phase 1 ships and customer data lands in
-- these tables, rolling back is a data-loss event — operators
-- should pg_dump first.

DROP TABLE IF EXISTS webhook_deliveries CASCADE;
DROP TABLE IF EXISTS customer_webhooks CASCADE;
DROP TABLE IF EXISTS audit_log CASCADE;
DROP TABLE IF EXISTS invites CASCADE;
DROP TABLE IF EXISTS stripe_event_log CASCADE;
DROP TABLE IF EXISTS subscriptions CASCADE;

-- api_usage_events is a hypertable; drop_chunks first then the
-- table. The hypertable extension drops cleanly with DROP TABLE
-- but we explicitly drop the retention policy first so the
-- background worker doesn't hold a lock.
SELECT remove_retention_policy('api_usage_events', if_exists => true);
DROP TABLE IF EXISTS api_usage_events CASCADE;

DROP TABLE IF EXISTS api_keys CASCADE;
DROP TABLE IF EXISTS magic_link_tokens CASCADE;
DROP TABLE IF EXISTS sessions CASCADE;
DROP TABLE IF EXISTS users CASCADE;
DROP TABLE IF EXISTS accounts CASCADE;

-- Extensions are shared with other migrations — we don't drop them.
