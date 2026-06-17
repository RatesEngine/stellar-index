// Package ratelimit is a Redis-backed fixed-window rate limiter.
//
// # Why fixed-window, not token bucket or sliding window?
//
// Our API SLA is "1000 req/min per client".
// That's a minute-granular ceiling, not a smooth-rate budget. A
// fixed 1-minute window keyed on `rl:<key>:<min>` matches the
// contract exactly + costs one Redis round-trip per request.
//
// Sliding windows need two counters and weighted maths; token
// buckets need INCRBYFLOAT + drift correction + more state per
// key. Neither is worth the complexity here.
//
// # Atomicity
//
// The check is a Lua script (EVAL) that does INCR + EXPIRE-on-first
// + TTL-return atomically. No race between "read counter" and "set
// TTL" — if two requests arrive simultaneously on a cold key, only
// one sets the expiry and both see the correct incremented count.
//
// # Redis key shape
//
// Matches ADR-0007:
//
//	rl:<key>:<minute-epoch>
//
// where `<key>` is an API-key hash or IP address and
// `<minute-epoch>` is `unix_seconds / 60` — deterministic so every
// API pod derives the same key. Keys TTL at 120 s (2× window), so
// they drain out on their own.
//
// # Usage
//
//	b := ratelimit.New(rdb, 1000, time.Minute)
//	res, err := b.Take(ctx, "rek_abc123")
//	if err != nil {
//	    // Redis unreachable — fail open + log a warning.
//	    log.Warn("ratelimit: redis unreachable", "err", err)
//	    next(w, r)
//	    return
//	}
//	if !res.Allowed {
//	    w.Header().Set("Retry-After", strconv.Itoa(int(res.RetryAfter.Seconds())))
//	    http.Error(w, "rate limit exceeded", http.StatusTooManyRequests)
//	    return
//	}
//	w.Header().Set("X-RateLimit-Limit", strconv.Itoa(b.Max()))
//	w.Header().Set("X-RateLimit-Remaining", strconv.Itoa(res.Remaining))
//
// # Failure mode
//
// When Redis is unreachable, Take() returns an error. The caller
// chooses fail-open (accept the request + log) or fail-closed
// (reject + 503). API handler code fails open — it's better to
// accept a burst of unthrottled requests than to refuse every
// request during a Redis blip.
//
// The HA plan §9 documents this as a stale_flag=true scenario.
package ratelimit
