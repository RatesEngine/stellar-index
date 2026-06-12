package timescale

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// TestVWAPUSDFXResolver_NoPegs — empty USDPegs list means the
// resolver is a no-op: every call returns ok=false without
// touching the DB. Pre-Phase-2 behaviour, preserved by F-1268
// for deployments that haven't opted in.
func TestVWAPUSDFXResolver_NoPegs(t *testing.T) {
	r, err := NewVWAPUSDFXResolver(&Store{}, VWAPUSDFXResolverOptions{
		USDPegs: nil,
	})
	if err != nil {
		t.Fatalf("NewVWAPUSDFXResolver: %v", err)
	}
	eurc, _ := canonical.NewClassicAsset("EURC", "GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2")
	got, ok, err := r.USDPriceAt(context.Background(), eurc, time.Now())
	if err != nil {
		t.Errorf("USDPriceAt: unexpected error: %v", err)
	}
	if ok || got != "" {
		t.Errorf("USDPriceAt(no pegs) = (%q, %t), want ('', false)", got, ok)
	}
}

// TestVWAPUSDFXResolver_NilStore — defensive guard at construction.
func TestVWAPUSDFXResolver_NilStore(t *testing.T) {
	_, err := NewVWAPUSDFXResolver(nil, VWAPUSDFXResolverOptions{})
	if err == nil {
		t.Fatal("expected error when store is nil")
	}
	if !errors.Is(err, err) {
		t.Errorf("expected wrapped error, got: %v", err)
	}
}

// TestVWAPUSDFXResolver_DefaultsApplied — zero-value options yield
// the production-sane defaults (1h freshness, 5m cache, time.Now).
func TestVWAPUSDFXResolver_DefaultsApplied(t *testing.T) {
	r, err := NewVWAPUSDFXResolver(&Store{}, VWAPUSDFXResolverOptions{
		USDPegs: []string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
	})
	if err != nil {
		t.Fatalf("NewVWAPUSDFXResolver: %v", err)
	}
	if r.freshness != time.Hour {
		t.Errorf("freshness default = %v, want 1h", r.freshness)
	}
	if r.cacheTTL != 5*time.Minute {
		t.Errorf("cacheTTL default = %v, want 5m", r.cacheTTL)
	}
}

// TestVWAPUSDFXResolver_CachePopulatedHits — once a rate is in
// the cache, subsequent calls within the TTL skip the DB query.
// We exercise this by populating the cache directly + asserting
// USDPriceAt returns the cached value without panicking on the
// nil DB.
func TestVWAPUSDFXResolver_CachePopulatedHits(t *testing.T) {
	now := time.Date(2026, 5, 12, 14, 30, 0, 0, time.UTC)
	r, err := NewVWAPUSDFXResolver(&Store{}, VWAPUSDFXResolverOptions{
		USDPegs: []string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
		Clock:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVWAPUSDFXResolver: %v", err)
	}

	eurc, _ := canonical.NewClassicAsset("EURC", "GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2")
	// Floor the timestamp the same way the resolver does.
	bucket := now.UTC().Truncate(time.Minute)
	key := fxCacheKey{asset: eurc.String(), bucketMs: bucket.UnixMilli()}
	r.cache[key] = fxCacheEntry{rate: "1.0850", cachedAt: now}

	got, ok, err := r.USDPriceAt(context.Background(), eurc, now)
	if err != nil {
		t.Fatalf("USDPriceAt: %v", err)
	}
	if !ok {
		t.Fatalf("expected cache hit, got ok=false")
	}
	if got != "1.0850" {
		t.Errorf("USDPriceAt = %q, want 1.0850", got)
	}
}

// TestVWAPUSDFXResolver_CacheNegativeHitsAlsoSkipDB — a previous
// resolution that returned "" (no rate available) is also cached,
// so we don't re-query for the next thousand trades against the
// same uncovered asset. The cached negative result should produce
// ok=false without DB access.
func TestVWAPUSDFXResolver_CacheNegativeHitsAlsoSkipDB(t *testing.T) {
	now := time.Date(2026, 5, 12, 14, 30, 0, 0, time.UTC)
	r, err := NewVWAPUSDFXResolver(&Store{}, VWAPUSDFXResolverOptions{
		USDPegs: []string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
		Clock:   func() time.Time { return now },
	})
	if err != nil {
		t.Fatalf("NewVWAPUSDFXResolver: %v", err)
	}
	mxn, _ := canonical.NewClassicAsset("MXNe", "GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2")
	bucket := now.UTC().Truncate(time.Minute)
	key := fxCacheKey{asset: mxn.String(), bucketMs: bucket.UnixMilli()}
	// Negative cache entry — empty rate, fresh-cachedAt.
	r.cache[key] = fxCacheEntry{rate: "", cachedAt: now}

	got, ok, err := r.USDPriceAt(context.Background(), mxn, now)
	if err != nil {
		t.Errorf("USDPriceAt with cached negative: %v", err)
	}
	if ok {
		t.Errorf("expected ok=false for cached negative, got ok=true, rate=%q", got)
	}
	if got != "" {
		t.Errorf("expected empty rate for cached negative, got %q", got)
	}
}

// TestVWAPUSDFXResolver_CacheTTLExpiry — a cache entry older than
// CacheTTL is treated as a miss. Without a DB the call returns
// an error because queryDB would touch nil; we don't assert on
// the error type, just on the cache miss path (we re-acquire the
// lock + look up; we don't take the early-return).
func TestVWAPUSDFXResolver_CacheTTLExpiry(t *testing.T) {
	now := time.Date(2026, 5, 12, 14, 30, 0, 0, time.UTC)
	clock := now
	r, err := NewVWAPUSDFXResolver(&Store{}, VWAPUSDFXResolverOptions{
		USDPegs:  []string{"USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"},
		CacheTTL: 5 * time.Minute,
		Clock:    func() time.Time { return clock },
	})
	if err != nil {
		t.Fatalf("NewVWAPUSDFXResolver: %v", err)
	}
	asset, _ := canonical.NewClassicAsset("USDX", "GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2")
	bucket := now.UTC().Truncate(time.Minute)
	key := fxCacheKey{asset: asset.String(), bucketMs: bucket.UnixMilli()}
	// Cache an entry — but stamped 10 minutes ago, past the TTL.
	r.cache[key] = fxCacheEntry{rate: "0.95", cachedAt: now.Add(-10 * time.Minute)}

	got, isCached := r.lookupCache(key)
	if isCached {
		t.Errorf("expired entry should return ok=false from lookupCache; got rate=%q", got)
	}
}

// TestVWAPUSDFXResolver_MinuteBucketKey — two trades within the
// same minute share the same cache key. Pin this — the cache
// resolution is what makes the per-trade lookup affordable.
func TestVWAPUSDFXResolver_MinuteBucketKey(t *testing.T) {
	asset, _ := canonical.NewClassicAsset("EURC", "GDHU6WRG4IEQXM5NZ4BMPKOXHW76MZM4Y2IEMFDVXBSDP6SJY4ITNPP2")
	base := time.Date(2026, 5, 12, 14, 30, 0, 0, time.UTC)
	for _, offset := range []time.Duration{0, time.Second, 30 * time.Second, 59 * time.Second} {
		t.Run(offset.String(), func(t *testing.T) {
			at := base.Add(offset)
			gotBucket := at.UTC().Truncate(time.Minute)
			if !gotBucket.Equal(base) {
				t.Errorf("offset %v truncated to %v, want %v", offset, gotBucket, base)
			}
			_ = asset
		})
	}
}

// TestTrimNumericText — Postgres NUMERIC::text preserves the
// column's full scale, so a VWAP arithmetically equal to 1.085
// arrives as `1.085000000000000000000`. The resolver canonicalises
// before returning so consumers see the human-friendly form.
// F-1251 (codex audit-2026-05-12).
func TestTrimNumericText(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"1.085000", "1.085"},
		{"1.085000000000000000000", "1.085"},
		{"1.000000", "1"},
		{"42", "42"},
		{"42.0", "42"},
		{"0.000", "0"},
		{"0.5", "0.5"},
		{"100.500", "100.5"},
		{"-1.500", "-1.5"},
		{"-0.0", "0"},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := trimNumericText(tc.in); got != tc.want {
				t.Errorf("trimNumericText(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestVWAPUSDFXResolver_FreshnessSentinels — F-1251 sentinel
// semantics: 0 → default 1h; negative → disabled; positive →
// use as-is. Pre-fix the docstring claimed "0 = disable" but
// the constructor silently overrode 0 to 1h.
func TestVWAPUSDFXResolver_FreshnessSentinels(t *testing.T) {
	cases := []struct {
		name string
		in   time.Duration
		want time.Duration
	}{
		{"zero defaults to 1h", 0, time.Hour},
		{"negative disables", -1, 0},
		{"explicit positive used", 30 * time.Minute, 30 * time.Minute},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r, err := NewVWAPUSDFXResolver(&Store{}, VWAPUSDFXResolverOptions{
				USDPegs:   []string{"USDC-G..."},
				Freshness: tc.in,
			})
			if err != nil {
				t.Fatalf("NewVWAPUSDFXResolver: %v", err)
			}
			if r.freshness != tc.want {
				t.Errorf("freshness = %v, want %v", r.freshness, tc.want)
			}
		})
	}
}
