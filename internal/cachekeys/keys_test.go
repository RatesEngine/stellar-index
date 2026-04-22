package cachekeys_test

import (
	"strings"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/cachekeys"
	"github.com/RatesEngine/rates-engine/internal/canonical"
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

	// Open-candle TTL is short; closed is zero (immutable).
	if cachekeys.OHLCOpenTTL != 5*time.Second {
		t.Errorf("OHLCOpenTTL = %v", cachekeys.OHLCOpenTTL)
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
	// Consistency check: internal/ratelimit builds "rl:<key>:<bucket>"
	// directly; this package mirrors that shape. If someone changes
	// either side, this test highlights the drift.
	now := time.Unix(1_750_000_000, 0).UTC()
	k := cachekeys.RateLimitKey("x", now, time.Minute)
	if !strings.HasPrefix(k, "rl:") {
		t.Errorf("RateLimitKey must use rl: prefix, got %q", k)
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
	if cachekeys.TOMLTTL != 15*time.Minute {
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
	if k := cachekeys.Divergence(xlm); k != "div:native" {
		t.Errorf("Divergence(XLM) = %q", k)
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

func TestAllKeysHaveDistinctPrefixes(t *testing.T) {
	// Regression guard: every key class must have a unique first
	// segment so cluster-slot distribution is natural + grep-able.
	xlm := canonical.NativeAsset()
	usdc, _ := canonical.NewClassicAsset("USDC", usdcIssuer)
	now := time.Now()

	prefixes := map[string]string{
		"price":  cachekeys.Price(xlm),
		"vwap":   cachekeys.VWAP(xlm, usdc, time.Minute),
		"ohlc":   cachekeys.OHLC(xlm, usdc, "1m", now),
		"rl":     cachekeys.RateLimitKey("x", now, time.Minute),
		"toml":   cachekeys.TOML("example.com"),
		"meta":   cachekeys.Metadata(xlm),
		"sub":    cachekeys.Subscriber("c", "s"),
		"div":    cachekeys.Divergence(xlm),
		"health": cachekeys.Health("src"),
	}
	for want, got := range prefixes {
		first := strings.SplitN(got, ":", 2)[0]
		if first != want {
			t.Errorf("key %q should start with %q:", got, want)
		}
	}
}
