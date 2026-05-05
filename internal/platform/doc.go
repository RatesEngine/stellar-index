// Package platform models the customer + staff dashboard primitives
// from docs/architecture/platform-spec.md.
//
// Scope today (Phase 1 Week 1, this PR): types only. Repository
// interfaces for each aggregate are declared so the auth flow
// (Week 2), key management (Week 4), and usage ingestion (Week 5)
// can wire concrete implementations behind them without
// touching this package's public surface.
//
// The runtime API auth path (internal/auth.RedisAPIKeyValidator)
// is unchanged in this PR. The cutover from Redis-only to
// Postgres-canonical key storage lands in Week 4 with a
// write-through migration that mirrors existing Redis records
// into api_keys and adds Postgres as the read source of truth
// behind the validator.
//
// Package layout:
//
//	account.go      — Account aggregate
//	user.go         — User + Session aggregates
//	token.go        — MagicLinkToken + Invite aggregates
//	apikey.go       — Extended APIKey aggregate (replaces auth.APIKeyRecord
//	                  once the migration cuts over)
//	usage.go        — UsageEvent + UsageRollup wire types
//	billing.go      — Subscription + StripeEventLog aggregates
//	audit.go        — AuditLog entry
//	webhook.go      — CustomerWebhook + WebhookDelivery aggregates
//	errors.go       — Sentinel errors
package platform
