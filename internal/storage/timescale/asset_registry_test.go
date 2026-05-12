package timescale

import (
	"sync"
	"testing"
	"time"
)

// resetAssetRegistryDedupe clears the process-wide cache so
// subsequent calls to `shouldSkipAssetRegistryUpsert` see the
// asset as fresh. F-1243 (codex audit-2026-05-12).
func resetAssetRegistryDedupe() {
	assetRegistryDedupe = sync.Map{}
}

// TestShouldSkipAssetRegistryUpsert_NoCache — first call for an
// asset returns false (don't skip; the upsert needs to run).
func TestShouldSkipAssetRegistryUpsert_NoCache(t *testing.T) {
	resetAssetRegistryDedupe()
	if shouldSkipAssetRegistryUpsert("USDC-GA5Z", time.Now()) {
		t.Error("first-time check returned skip=true; want false (no cache → must upsert)")
	}
}

// TestShouldSkipAssetRegistryUpsert_WithinTTL — call inside the
// 60s window after a recorded upsert returns true (skip the
// DB round-trip — the F-1243 pre-fix preserved this behaviour
// indefinitely; the wave-46 fix only preserves it for the TTL
// window).
func TestShouldSkipAssetRegistryUpsert_WithinTTL(t *testing.T) {
	resetAssetRegistryDedupe()
	now := time.Now()
	assetRegistryDedupe.Store("USDC-GA5Z", now.Add(-5*time.Second))
	if !shouldSkipAssetRegistryUpsert("USDC-GA5Z", now) {
		t.Error("5s-after-upsert returned skip=false; want true (within 60s TTL)")
	}
}

// TestShouldSkipAssetRegistryUpsert_PastTTL — call outside the
// 60s window returns false (upsert must run so `last_seen_*` +
// `observation_count` advance). This is the F-1243 regression
// — pre-wave-46 the cache had no TTL so every subsequent call
// returned true and the row froze at first observation.
func TestShouldSkipAssetRegistryUpsert_PastTTL(t *testing.T) {
	resetAssetRegistryDedupe()
	now := time.Now()
	assetRegistryDedupe.Store("USDC-GA5Z", now.Add(-2*time.Minute))
	if shouldSkipAssetRegistryUpsert("USDC-GA5Z", now) {
		t.Error("2min-after-upsert returned skip=true; want false (past 60s TTL)")
	}
}

// TestShouldSkipAssetRegistryUpsert_DifferentAssetMisses — the
// cache is per-asset; an entry for asset A doesn't suppress
// asset B.
func TestShouldSkipAssetRegistryUpsert_DifferentAssetMisses(t *testing.T) {
	resetAssetRegistryDedupe()
	now := time.Now()
	assetRegistryDedupe.Store("USDC-GA5Z", now)
	if shouldSkipAssetRegistryUpsert("AQUA-GBNZ", now) {
		t.Error("different-asset returned skip=true; want false (per-asset cache)")
	}
}

// TestShouldSkipAssetRegistryUpsert_CacheCorruption — if some
// future code mistakenly stores a non-time.Time value (legacy
// bug shape from the pre-wave-46 sentinel pattern), the gate
// fails open (returns false) so the upsert still runs.
func TestShouldSkipAssetRegistryUpsert_CacheCorruption(t *testing.T) {
	resetAssetRegistryDedupe()
	assetRegistryDedupe.Store("USDC-GA5Z", struct{}{}) // pre-wave-46 shape
	if shouldSkipAssetRegistryUpsert("USDC-GA5Z", time.Now()) {
		t.Error("corrupt-cache returned skip=true; want false (fail-open to allow upsert)")
	}
}

// TestAssetRegistryDedupeTTL_FrozenValue — pin the TTL so a
// future operator can't quietly tighten it past the audit's
// acceptable window (60s gives roughly minute-level "last
// seen" freshness in the dashboard).
func TestAssetRegistryDedupeTTL_FrozenValue(t *testing.T) {
	if assetRegistryDedupeTTL != 60*time.Second {
		t.Errorf("assetRegistryDedupeTTL = %v, want 60s (operator-visible)", assetRegistryDedupeTTL)
	}
}
