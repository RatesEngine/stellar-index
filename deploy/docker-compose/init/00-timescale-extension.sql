-- Runs once on first container startup (`docker-entrypoint-initdb.d`).
-- Pre-creates the TimescaleDB extension in the default database so
-- our 0001 migration's CREATE EXTENSION IF NOT EXISTS is a no-op
-- when migrations run. Matches production ordering where the
-- extension is typically provisioned by the DBA ahead of time.

CREATE EXTENSION IF NOT EXISTS timescaledb;
