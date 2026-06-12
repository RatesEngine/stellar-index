package cachekeys_test

import (
	"strings"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// usdcIssuer is the Circle USDC issuer — reused as a realistic G-address
// fixture across tests.
const usdcIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"

func TestPriceKey(t *testing.T) {
	xlm := canonical.NativeAsset()
	k := cachekeys.Price(xlm)
	if k != "price:native" {
		t.Errorf("Price(XLM) = %q, want 'price:native'", k)
	}

	usdc, _ := canonical.NewClassicAsset("USDC", usdcIssuer)
	k2 := cachekeys.Price(usdc)
	if !strings.HasPrefix(k2, "price:USDC-") {
		t.Errorf("Price(USDC) = %q, want prefix 'price:USDC-'", k2)
	}

	// TTL pinned to ADR-0007 (60 s). Mirrors the assertion style used
	// by the other key classes so a drift in either direction is
	// caught by the test suite.
	if cachekeys.PriceTTL != 60*time.Second {
		t.Errorf("PriceTTL = %v, want 60s (ADR-0007)", cachekeys.PriceTTL)
	}
}

func TestVWAP(t *testing.T) {
	xlm := canonical.NativeAsset()
	usdc, _ := canonical.NewClassicAsset("USDC", usdcIssuer)

	k := cachekeys.VWAP(xlm, usdc, 5*time.Minute)
	// Format: vwap:<base>:<quote>:<window-seconds>
	expected := "vwap:native:USDC-" + usdcIssuer + ":300"
	if k != expected {
		t.Errorf("VWAP = %q, want %q", k, expected)
	}

	if ttl := cachekeys.VWAPTTL(5 * time.Minute); ttl != 5*time.Minute {
		t.Errorf("VWAPTTL = %v", ttl)
	}
	if ttl := cachekeys.VWAPTTL(0); ttl != 0 {
		t.Errorf("VWAPTTL(0) = %v, want 0", ttl)
	}
}

func TestOHLC(t *testing.T) {
	xlm := canonical.NativeAsset()
	usdc, _ := canonical.NewClassicAsset("USDC", usdcIssuer)
	bucket := time.Unix(1_745_000_000, 0).UTC()

	k := cachekeys.OHLC(xlm, usdc, "15m", bucket)
	// Expected: ohlc:native:USDC-...:15m:1745000000
	if !strings.HasPrefix(k, "ohlc:native:USDC-") {
		t.Errorf("OHLC key malformed: %q", k)
	}
	if !strings.HasSuffix(k, ":15m:1745000000") {
		t.Errorf("OHLC key does not end with granularity:bucket: %q", k)
	}

	// Open-candle TTL is a safety-net upper bound matching ADR-0007;
	// closed is zero (immutable — CDN-pinned).
	if cachekeys.OHLCOpenTTL != time.Hour {
		t.Errorf("OHLCOpenTTL = %v, want 1h (ADR-0007)", cachekeys.OHLCOpenTTL)
	}
	if cachekeys.OHLCClosedTTL != 0 {
		t.Errorf("OHLCClosedTTL should be 0 (immutable), got %v", cachekeys.OHLCClosedTTL)
	}
}

func TestRateLimitKey(t *testing.T) {
	now := time.Unix(1_750_000_000, 0).UTC()
	k := cachekeys.RateLimitKey("rek_abc", now, time.Minute)
	// minute bucket = 1_750_000_000 / 60 = 29166666
	if k != "rl:rek_abc:29166666" {
		t.Errorf("RateLimitKey = %q, want 'rl:rek_abc:29166666'", k)
	}

	// TTL is 2× window per ADR-0007.
	if ttl := cachekeys.RateLimitTTL(time.Minute); ttl != 2*time.Minute {
		t.Errorf("RateLimitTTL = %v, want 2m", ttl)
	}
}

func TestRateLimitKey_MatchesRatelimitPackagePrefix(t *testing.T) {
	// Consistency check: internal/ratelimit builds "rl:<escape(key)>:<bucket>"
	// directly; this package mirrors that shape. If someone changes
	// either side, this test highlights the drift.
	now := time.Unix(1_750_000_000, 0).UTC()
	k := cachekeys.RateLimitKey("x", now, time.Minute)
	if !strings.HasPrefix(k, "rl:") {
		t.Errorf("RateLimitKey must use rl: prefix, got %q", k)
	}
}

func TestRateLimitKey_EscapesSubjectForParityWithBucket(t *testing.T) {
	// Subjects containing `:` (e.g. IPv6 addresses) are url.QueryEscape'd
	// by internal/ratelimit/bucket.go's Take() to prevent
	// cross-subject collisions on the Redis slot. This mirror
	// function MUST escape identically or the two sides produce
	// different keys for the same subject.
	now := time.Unix(1_750_000_000, 0).UTC()
	k := cachekeys.RateLimitKey("2001:db8::1", now, time.Minute)
	if !strings.HasPrefix(k, "rl:2001%3Adb8%3A%3A1:") {
		t.Errorf("RateLimitKey did not escape `:` in IPv6 subject: got %q", k)
	}
}

func TestTOML(t *testing.T) {
	// Lowercasing is intentional — domain names are case-insensitive.
	if k := cachekeys.TOML("Circle.com"); k != "toml:circle.com" {
		t.Errorf("TOML(Circle.com) = %q", k)
	}
	if k := cachekeys.TOML("lobstr.co"); k != "toml:lobstr.co" {
		t.Errorf("TOML(lobstr.co) = %q", k)
	}
	// 24h — stellar.toml is slow-changing issuer reference data; a
	// short TTL just makes cold /v1/assets/{id} pay a fresh ~500ms
	// upstream fetch (#63).
	if cachekeys.TOMLTTL != 24*time.Hour {
		t.Errorf("TOMLTTL = %v", cachekeys.TOMLTTL)
	}
}

func TestMetadata(t *testing.T) {
	xlm := canonical.NativeAsset()
	if k := cachekeys.Metadata(xlm); k != "meta:native" {
		t.Errorf("Metadata(XLM) = %q", k)
	}
	if cachekeys.MetadataTTL != 5*time.Minute {
		t.Errorf("MetadataTTL = %v", cachekeys.MetadataTTL)
	}
}

func TestSubscriber(t *testing.T) {
	k := cachekeys.Subscriber("price:XLM", "conn-42")
	if k != "sub:price:XLM:conn-42" {
		t.Errorf("Subscriber = %q", k)
	}
	if cachekeys.SubscriberTTL != 60*time.Second {
		t.Errorf("SubscriberTTL = %v", cachekeys.SubscriberTTL)
	}
}

func TestDivergence(t *testing.T) {
	xlm := canonical.NativeAsset()
	usd, err := canonical.ParseAsset("fiat:USD")
	if err != nil {
		t.Fatalf("ParseAsset: %v", err)
	}
	pair, err := canonical.NewPair(xlm, usd)
	if err != nil {
		t.Fatalf("NewPair: %v", err)
	}
	if k := cachekeys.Divergence(pair); k != "div:native/fiat:USD" {
		t.Errorf("Divergence(XLM/USD) = %q", k)
	}
	if k := cachekeys.DivergenceBaseIndex(xlm); k != "div:idx:native" {
		t.Errorf("DivergenceBaseIndex(XLM) = %q", k)
	}
	if cachekeys.DivergenceTTL != 5*time.Minute {
		t.Errorf("DivergenceTTL = %v", cachekeys.DivergenceTTL)
	}
}

func TestHealth(t *testing.T) {
	if k := cachekeys.Health("soroswap"); k != "health:soroswap" {
		t.Errorf("Health = %q", k)
	}
	if cachekeys.HealthTTL != 60*time.Second {
		t.Errorf("HealthTTL = %v", cachekeys.HealthTTL)
	}
}

// TestVWAPProvenance covers the cache key + the constant marker
// the triangulation worker stamps. The API reads the value via
// byte equality so the two must stay in lock-step — flipping
// either side without the other breaks `flags.triangulated`.
func TestVWAPProvenance(t *testing.T) {
	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", usdcIssuer)
	if err != nil {
		t.Fatalf("NewClassicAsset: %v", err)
	}

	got := cachekeys.VWAPProvenance(xlm, usdc, 5*time.Minute)
	want := "vwap:native:USDC-" + usdcIssuer + ":300:provenance"
	if got != want {
		t.Errorf("VWAPProvenance = %q, want %q", got, want)
	}
	// Sibling-key check: the provenance key MUST be the VWAP key
	// with `:provenance` suffixed. The aggregator writes both
	// atomically; a mismatched suffix would orphan the marker.
	vwap := cachekeys.VWAP(xlm, usdc, 5*time.Minute)
	if got != vwap+":provenance" {
		t.Errorf("VWAPProvenance %q is not VWAP %q + :provenance", got, vwap)
	}
	if cachekeys.VWAPProvenanceTriangulated != "triangulated" {
		t.Errorf("marker = %q, want 'triangulated' (API matches by byte-equality)",
			cachekeys.VWAPProvenanceTriangulated)
	}
}

// TestConfidence pins the wire shape + ConfidenceTTL parity with
// VWAPTTL. The score is meaningless once the underlying VWAP
// expires, so the two TTLs must move together.
func TestConfidence(t *testing.T) {
	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", usdcIssuer)
	if err != nil {
		t.Fatalf("NewClassicAsset: %v", err)
	}

	got := cachekeys.Confidence(xlm, usdc, time.Hour)
	want := "confidence:native:USDC-" + usdcIssuer + ":3600"
	if got != want {
		t.Errorf("Confidence = %q, want %q", got, want)
	}
	if cachekeys.ConfidenceTTL(time.Hour) != cachekeys.VWAPTTL(time.Hour) {
		t.Errorf("ConfidenceTTL must equal VWAPTTL — score is tied to its underlying VWAP")
	}
}

// TestFreeze pins the marker key shape and the documented FreezeTTL
// of 5 minutes (long enough to span the next bucket-close, short
// enough to clear within a few buckets of recovery).
func TestFreeze(t *testing.T) {
	xlm := canonical.NativeAsset()
	usdc, err := canonical.NewClassicAsset("USDC", usdcIssuer)
	if err != nil {
		t.Fatalf("NewClassicAsset: %v", err)
	}

	got := cachekeys.Freeze(xlm, usdc)
	want := "freeze:native:USDC-" + usdcIssuer
	if got != want {
		t.Errorf("Freeze = %q, want %q", got, want)
	}
	if cachekeys.FreezeTTL != 5*time.Minute {
		t.Errorf("FreezeTTL = %v, want 5m (per cachekeys §Freeze design note)",
			cachekeys.FreezeTTL)
	}
}

// TestAPIKey pins the auth package's lookup contract: the key is
// `apikey:<sha256-hex>`, no TTL (revocation is encoded in the JSON
// payload, not at Redis).
func TestAPIKey(t *testing.T) {
	// Realistic-shape SHA-256 hex (64 chars).
	hash := "fc1908d72d0c4cdf3eaa45e8b3f2c2c4f6a3b7d29c4ad4e63b81a7e9be2c1cea"
	got := cachekeys.APIKey(hash)
	want := "apikey:" + hash
	if got != want {
		t.Errorf("APIKey = %q, want %q", got, want)
	}
	if cachekeys.APIKeyTTL != 0 {
		t.Errorf("APIKeyTTL = %v, want 0 (revocation in payload, not Redis TTL)",
			cachekeys.APIKeyTTL)
	}
}

// TestRateLimitTTL pins the 2× window relationship documented in
// ADR-0007. Anything less and the counter could still be live when
// the next window starts; anything more is wasted Redis memory.
func TestRateLimitTTL(t *testing.T) {
	for _, w := range []time.Duration{time.Second, 30 * time.Second, time.Minute, time.Hour} {
		if got := cachekeys.RateLimitTTL(w); got != 2*w {
			t.Errorf("RateLimitTTL(%v) = %v, want 2× = %v", w, got, 2*w)
		}
	}
}

func TestAllKeysHaveDistinctPrefixes(t *testing.T) {
	// Regression guard: every key class must have a unique first
	// segment so cluster-slot distribution is natural + grep-able.
	xlm := canonical.NativeAsset()
	usdc, _ := canonical.NewClassicAsset("USDC", usdcIssuer)
	now := time.Now()

	prefixes := map[string]string{
		"price":      cachekeys.Price(xlm),
		"vwap":       cachekeys.VWAP(xlm, usdc, time.Minute),
		"confidence": cachekeys.Confidence(xlm, usdc, time.Minute),
		"ohlc":       cachekeys.OHLC(xlm, usdc, "1m", now),
		"rl":         cachekeys.RateLimitKey("x", now, time.Minute),
		"toml":       cachekeys.TOML("example.com"),
		"meta":       cachekeys.Metadata(xlm),
		"sub":        cachekeys.Subscriber("c", "s"),
		"div":        cachekeys.Divergence(canonical.Pair{Base: xlm, Quote: usdc}),
		"freeze":     cachekeys.Freeze(xlm, usdc),
		"health":     cachekeys.Health("src"),
	}
	for want, got := range prefixes {
		first := strings.SplitN(got, ":", 2)[0]
		if first != want {
			t.Errorf("key %q should start with %q:", got, want)
		}
	}
}
