// Package redisclient builds a [redis.UniversalClient] from
// [config.StorageConfig], picking Sentinel-aware FailoverClient
// when the operator configured Sentinel addresses (production
// topology per ADR-0024) and falling back to a plain Client
// against [config.StorageConfig.RedisAddr] for dev / single-node.
//
// Both binaries (cmd/ratesengine-api, cmd/ratesengine-aggregator)
// use this builder so the Sentinel migration happens in one
// place, not two. Tests hit a real redis via miniredis or the
// integration harness — neither path uses Sentinel, so the
// FailoverClient branch is exercised only in deployment.
package redisclient

import (
	"github.com/redis/go-redis/v9"

	"github.com/RatesEngine/rates-engine/internal/config"
)

// Build returns a [redis.UniversalClient] suitable for both API
// and aggregator binaries.
//
// Selection rule:
//   - cfg.RedisSentinelAddrs non-empty → FailoverClient (asks
//     Sentinel for the current primary; survives failover without
//     a config change). cfg.RedisAddr is ignored in this mode.
//   - cfg.RedisSentinelAddrs empty AND cfg.RedisAddr non-empty →
//     plain Client. Dev / single-node deployments.
//   - both empty → returns nil. Caller decides whether that's
//     fatal (aggregator) or tolerable (API: SEP-1 cache + account
//     store fall through gracefully).
//
// The returned client is safe for concurrent use; close it via
// [redis.UniversalClient.Close] on shutdown.
func Build(cfg config.StorageConfig) redis.UniversalClient {
	// Username is empty by default (legacy default-user path) and
	// set to "ratesengine" (or per-component) when the operator
	// flipped redis_acl_lockdown=true in the redis-sentinel ansible
	// role. F-1213 (audit-2026-05-12).
	if len(cfg.RedisSentinelAddrs) > 0 {
		return redis.NewFailoverClient(&redis.FailoverOptions{
			MasterName:    cfg.RedisMasterName,
			SentinelAddrs: cfg.RedisSentinelAddrs,
			// Same secret authenticates both the data plane and
			// Sentinel — see ADR-0024 §"Auth": requirepass +
			// masterauth + sentinel auth-pass all share the vault
			// entry.
			Username:         cfg.RedisUsername,
			Password:         cfg.RedisPassword,
			SentinelPassword: cfg.RedisPassword,
		})
	}
	if cfg.RedisAddr == "" {
		return nil
	}
	return redis.NewClient(&redis.Options{
		Addr:     cfg.RedisAddr,
		Username: cfg.RedisUsername,
		Password: cfg.RedisPassword,
	})
}

// Mode reports which branch [Build] would take. Used by the
// startup logger so operators see "redis mode=sentinel" or
// "redis mode=single" in the boot line — matters for SEV-1
// triage when the config doesn't match the deployed topology.
func Mode(cfg config.StorageConfig) string {
	switch {
	case len(cfg.RedisSentinelAddrs) > 0:
		return "sentinel"
	case cfg.RedisAddr != "":
		return "single"
	default:
		return "disabled"
	}
}
