package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/RatesEngine/rates-engine/internal/auth"
	"github.com/RatesEngine/rates-engine/internal/config"
	"github.com/RatesEngine/rates-engine/internal/storage/redisclient"
)

// upgradeKey lifts (or lowers) the per-minute rate-limit on an
// existing API key. Used by operators to perform manual paid-tier
// upgrades before the Stripe webhook handler ships, and by the
// webhook handler itself once it does (calls the same internal
// `auth.RedisAPIKeyStore.UpdateRateLimit` path).
//
// Usage:
//
//	ratesengine-ops upgrade-key \
//	  -config /etc/ratesengine.toml \
//	  -key-id kid_515c8d94191f4e93 \
//	  -rate-limit-per-min 10000
//
// Tier suggestion (matches the /signup page tier table):
//
//	 1000  — Starter (free; what /v1/signup hands out)
//	10000  — Pro (paid)
//	50000  — Business (paid)
//	custom — Enterprise (per-deployment)
//
// Exit codes:
//
//	0 — upgraded
//	1 — error (Redis unreachable, key not found, etc.)
//	2 — usage error (missing flag)
func upgradeKey(args []string) error {
	fs := flag.NewFlagSet("upgrade-key", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	keyID := fs.String("key-id", "", "KeyID of the API key to upgrade (kid_… prefix). Get this from /v1/account/me or the signup response (required)")
	rateLimit := fs.Int("rate-limit-per-min", 0, "New per-minute rate-limit budget. 0 means tier default. (required, supply -1 for 'reset to default')")
	hasRateLimit := false
	fs.Visit(func(*flag.Flag) {})
	if err := fs.Parse(args); err != nil {
		return err
	}
	// Detect whether -rate-limit-per-min was actually supplied —
	// without it the int default 0 is ambiguous with "reset".
	fs.Visit(func(f *flag.Flag) {
		if f.Name == "rate-limit-per-min" {
			hasRateLimit = true
		}
	})

	if *cfgPath == "" {
		return errors.New("-config is required")
	}
	if *keyID == "" {
		return errors.New("-key-id is required")
	}
	if !hasRateLimit {
		return errors.New("-rate-limit-per-min is required (use 0 for tier default)")
	}
	if *rateLimit < 0 {
		return fmt.Errorf("-rate-limit-per-min must be >= 0 (got %d; use 0 for tier default)", *rateLimit)
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}

	rdb := redisclient.Build(cfg.Storage)
	if rdb == nil {
		return errors.New("redis is not configured (storage.redis_addr / redis_sentinel_addrs both empty) — upgrade-key requires Redis")
	}
	defer func() { _ = rdb.Close() }()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if err := rdb.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping: %w", err)
	}

	store := auth.NewRedisAPIKeyStore(rdb)

	rec, err := store.UpdateRateLimit(ctx, *keyID, *rateLimit)
	if err != nil {
		if errors.Is(err, auth.ErrKeyNotFound) {
			return fmt.Errorf("key_id %q not found in Redis (typo? or this deployment's apikey hash list is empty)", *keyID)
		}
		return err
	}

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Updated record:")
	fmt.Fprintf(os.Stderr, "  key_id:           %s\n", rec.KeyID)
	fmt.Fprintf(os.Stderr, "  identifier:       %s\n", rec.Identifier)
	fmt.Fprintf(os.Stderr, "  label:            %s\n", rec.Label)
	fmt.Fprintf(os.Stderr, "  tier:             %s\n", rec.Tier)
	fmt.Fprintf(os.Stderr, "  rate_limit_per_min: %d  ← updated\n", rec.RateLimitPerMin)
	fmt.Fprintf(os.Stderr, "  created_at:       %s\n", rec.CreatedAt.UTC().Format(time.RFC3339))
	if !rec.ExpiresAt.IsZero() {
		fmt.Fprintf(os.Stderr, "  expires_at:       %s\n", rec.ExpiresAt.UTC().Format(time.RFC3339))
	}
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "The customer's existing plaintext key keeps working — they don't need to rotate to pick up the new budget. Effective on the next request.")
	return nil
}
