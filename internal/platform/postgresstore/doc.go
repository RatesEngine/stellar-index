// Package postgresstore implements every store interface from
// internal/platform against the Postgres schema in
// migrations/0027_platform_v1_schema.
//
// Conservative posture for this PR: the runtime auth path
// (internal/auth.RedisAPIKeyValidator) does NOT consult these
// stores. Phase 1 Week 2 — when the magic-link flow lands —
// will use AccountStore + UserStore + TokenStore. Phase 1 Week 4
// switches the API auth path to APIKeyStore via a Redis-cached
// read-through wrapper around the Postgres impl.
//
// Tests use testcontainers-go to spin a transient Postgres +
// TimescaleDB container per package, matching the
// internal/storage/timescale pattern (see the per-store *_test.go
// files, e.g. apikey_store_test.go).
package postgresstore
