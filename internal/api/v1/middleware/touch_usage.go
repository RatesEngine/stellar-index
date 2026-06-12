// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package middleware

import (
	"context"
	"log/slog"
	"net"
	"net/http"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
)

// KeyToucher is the storage-side primitive the [TouchUsage]
// middleware drives. Production implementation:
// `*postgresstore.APIKeyStore.TouchUsage`. Declared as a narrow
// interface so tests substitute fakes without standing up
// Postgres.
type KeyToucher interface {
	TouchUsage(ctx context.Context, id string, ip net.IP, userAgent string) error
}

// TouchDebouncer gates calls to [KeyToucher.TouchUsage] so we
// don't hammer the api_keys hot row with one UPDATE per
// request. Production implementation: a Redis-SETNX adapter
// keyed on `touch:apikey:<keyID>` with a configurable TTL
// (5 minutes by default — matches the audit's "debounce to
// once-per-minute" guidance with safety margin).
type TouchDebouncer interface {
	// ShouldTouch returns true exactly once per (keyID, TTL)
	// window. Subsequent calls inside the window return false
	// without contacting the underlying touch store.
	//
	// A storage-side error returns (false, err); the caller
	// treats both branches as "skip this tick" — TouchUsage is
	// best-effort and must not block customer requests.
	ShouldTouch(ctx context.Context, keyID string) (bool, error)
}

// TouchUsage returns a [Middleware] that updates the api_keys
// row's `last_used_at` / `last_used_ip` / `last_used_user_agent`
// columns for authenticated requests. F-1226 (codex audit-
// 2026-05-12): closes the third half of the finding (cache-hit
// policy parity in wave 34, monthly-quota enforcement in wave
// 38, this is the TouchUsage half).
//
// Behaviour:
//
//   - Wraps `next.ServeHTTP` so the touch fires post-handler
//     (touch is bookkeeping, not load-bearing). The work is
//     INLINE on the request goroutine — no detached goroutine
//     because the response has already been flushed and
//     spawning per-request goroutines for bookkeeping creates
//     unbounded fan-out under load.
//   - Skips anonymous Subjects, Subjects without a KeyID, and
//     deployments where toucher OR debouncer is nil — Redis-less
//     deployments fall in here and get the legacy "no last_used
//     updates" posture.
//   - The SETNX debounce keeps the per-request cost to one
//     Redis round-trip in the common case (debounce window held
//     by a recent request); only the first call per (key, TTL)
//     window pays the additional Postgres UPDATE.
//   - Best-effort: every error path logs at debug and drops.
//     The dashboard's "last seen" column is operator UX, not
//     auth — a debounce-store blip must never surface to the
//     customer.
//
// Wire AFTER [Auth] (so SubjectFrom returns) — placement in the
// chain after that is flexible because the middleware fires
// post-handler.
func TouchUsage(toucher KeyToucher, debouncer TouchDebouncer, logger *slog.Logger) Middleware {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r)
			if toucher == nil || debouncer == nil {
				return
			}
			subject, ok := auth.SubjectFrom(r.Context())
			if !ok || subject.Tier == auth.TierAnonymous || subject.KeyID == "" {
				return
			}
			ok, err := debouncer.ShouldTouch(r.Context(), subject.KeyID)
			if err != nil {
				logger.Debug("touch-usage: debounce check failed; skipping",
					"err", err, "key_id", subject.KeyID)
				return
			}
			if !ok {
				return
			}
			ip := net.ParseIP(RemoteIPFrom(r))
			ua := truncateUserAgentForTouch(r.UserAgent())
			if err := toucher.TouchUsage(r.Context(), subject.KeyID, ip, ua); err != nil {
				logger.Debug("touch-usage: TouchUsage failed; bookkeeping lost for this tick",
					"err", err, "key_id", subject.KeyID)
			}
		})
	}
}

// truncateUserAgentForTouch caps the User-Agent at 512 bytes so
// a misbehaving client can't blow up the api_keys.last_used_user_agent
// column (TEXT in Postgres but the column is rendered in the
// dashboard, where multi-KB UAs distort the table layout).
// Returns the empty string for blank input so the postgres NULLIF
// in TouchUsage's UPDATE leaves the column NULL.
func truncateUserAgentForTouch(ua string) string {
	const limit = 512
	if len(ua) > limit {
		return ua[:limit]
	}
	return ua
}
