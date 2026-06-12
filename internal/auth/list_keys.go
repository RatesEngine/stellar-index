package auth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/redis/go-redis/v9"

	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
)

// ListKeysForIdentifier returns every [APIKeyRecord] whose
// Identifier matches. Used by:
//
//   - The Stripe webhook handler, when a payment lands and we
//     need to lift every key that customer holds into the paid
//     tier (rather than asking them to rotate).
//   - The future /v1/account/keys (GET) endpoint that lists a
//     caller's keys.
//
// Implementation: SCANs `apikey:*`, JSON-decodes each, filters
// by Identifier. O(N) on key count — acceptable at v1's scale;
// if/when the total key count crosses ~10⁵, swap the SCAN for a
// `signup:identifier:<id>` Redis SET written at Create time.
//
// Returns nil + nil for "no matches" (the operator-facing path
// distinguishes "Stripe sent us a webhook for an identifier we
// don't know" from a Redis I/O failure).
func (s *RedisAPIKeyStore) ListKeysForIdentifier(ctx context.Context, identifier string) ([]APIKeyRecord, error) {
	if identifier == "" {
		return nil, errors.New("auth: ListKeysForIdentifier: identifier is required")
	}

	var out []APIKeyRecord
	iter := s.rdb.Scan(ctx, 0, cachekeys.APIKey("*"), 1000).Iterator()
	for iter.Next(ctx) {
		k := iter.Val()
		raw, err := s.rdb.Get(ctx, k).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue // raced with delete
			}
			return nil, fmt.Errorf("auth: ListKeysForIdentifier: redis get %s: %w", k, err)
		}
		var rec APIKeyRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			// Malformed record — skip + continue (don't fail the
			// whole call because some other key is corrupt).
			continue
		}
		if rec.Identifier == identifier {
			out = append(out, rec)
		}
	}
	if err := iter.Err(); err != nil {
		return nil, fmt.Errorf("auth: ListKeysForIdentifier: redis scan: %w", err)
	}
	return out, nil
}

// RevokeKeyByID deletes the API key whose KeyID matches `keyID`,
// constrained to the supplied `identifier` so a caller can only
// revoke keys they own. Returns nil + nil for "not found / not
// yours" — distinguishing those would let an attacker probe key-
// id existence cross-account, and the v1 handler treats both as
// 404 anyway.
//
// SCAN-based like ListKeysForIdentifier; same trade-off applies
// (O(N) on key count, fine at v1 scale). Once a `kid_idx`
// secondary index lands at issuance time, this method becomes
// O(1).
func (s *RedisAPIKeyStore) RevokeKeyByID(ctx context.Context, identifier, keyID string) error {
	if identifier == "" {
		return errors.New("auth: RevokeKeyByID: identifier is required")
	}
	if keyID == "" {
		return errors.New("auth: RevokeKeyByID: keyID is required")
	}
	iter := s.rdb.Scan(ctx, 0, cachekeys.APIKey("*"), 1000).Iterator()
	for iter.Next(ctx) {
		k := iter.Val()
		raw, err := s.rdb.Get(ctx, k).Bytes()
		if err != nil {
			if errors.Is(err, redis.Nil) {
				continue
			}
			return fmt.Errorf("auth: RevokeKeyByID: redis get %s: %w", k, err)
		}
		var rec APIKeyRecord
		if err := json.Unmarshal(raw, &rec); err != nil {
			continue
		}
		if rec.Identifier != identifier || rec.KeyID != keyID {
			continue
		}
		if err := s.rdb.Del(ctx, k).Err(); err != nil {
			return fmt.Errorf("auth: RevokeKeyByID: redis del %s: %w", k, err)
		}
		return nil
	}
	if err := iter.Err(); err != nil {
		return fmt.Errorf("auth: RevokeKeyByID: redis scan: %w", err)
	}
	// Not found / not owned. Silent — the handler renders 404 either
	// way and conflating the two prevents enumeration probes.
	return nil
}
