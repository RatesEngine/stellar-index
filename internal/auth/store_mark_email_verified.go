// Copyright (c) 2026 Rates Engine contributors.
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
)

// MarkEmailVerified flips an existing API key's
// `EmailVerifiedAt` timestamp to now (or to the optional
// `at` override, used by tests for determinism). F-1218 wave 45
// (codex audit-2026-05-12): the `/v1/signup/verify` handler
// calls this after Consume so the optional `RequireEmailVerified`
// middleware can gate /v1/* access on the flag.
//
// Implementation mirrors `UpdateRateLimit`: SCAN the
// `apikey:*` keyspace until the matching KeyID is found,
// read-modify-write the JSON record back. O(N) in key count;
// fine for v1's thousands-of-keys scale.
//
// Idempotent: re-marking an already-verified key updates the
// timestamp to the new value but doesn't error. The verify
// handler relies on this so a customer clicking the link twice
// in 24h doesn't get a 500.
//
// Returns the updated record on success or [ErrKeyNotFound]
// if no matching key exists.
func (s *RedisAPIKeyStore) MarkEmailVerified(ctx context.Context, keyID string, at time.Time) (APIKeyRecord, error) {
	if keyID == "" {
		return APIKeyRecord{}, errors.New("auth: MarkEmailVerified: keyID is required")
	}
	if at.IsZero() {
		at = s.now().UTC()
	}

	iter := s.rdb.Scan(ctx, 0, cachekeys.APIKey("*"), 1000).Iterator()
	for iter.Next(ctx) {
		k := iter.Val()
		raw, err := s.rdb.Get(ctx, k).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			return APIKeyRecord{}, fmt.Errorf("auth: MarkEmailVerified: redis get %s: %w", k, err)
		}
		var rec APIKeyRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.KeyID != keyID {
			continue
		}
		rec.EmailVerifiedAt = at
		body, err := json.Marshal(rec)
		if err != nil {
			return APIKeyRecord{}, fmt.Errorf("auth: MarkEmailVerified: marshal: %w", err)
		}
		if err := s.rdb.Set(ctx, k, body, 0).Err(); err != nil {
			return APIKeyRecord{}, fmt.Errorf("auth: MarkEmailVerified: redis set %s: %w", k, err)
		}
		return rec, nil
	}
	if err := iter.Err(); err != nil {
		return APIKeyRecord{}, fmt.Errorf("auth: MarkEmailVerified: redis scan: %w", err)
	}
	return APIKeyRecord{}, ErrKeyNotFound
}
