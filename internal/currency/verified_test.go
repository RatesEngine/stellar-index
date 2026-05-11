package currency

import (
	"strings"
	"testing"
)

func TestLoadEmbedded(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	if cat == nil {
		t.Fatal("LoadEmbedded returned nil catalogue")
	}
	if got := len(cat.All()); got < 10 {
		t.Errorf("seed catalogue has %d entries; want at least 10", got)
	}

	// Sanity-check a few well-known entries.
	cases := []struct {
		slug   string
		ticker string
		// hasStellar: must have a Stellar network entry with a code (not native-only).
		hasStellarClassic bool
	}{
		{"usdc", "USDC", true},
		{"xlm", "XLM", false},
		{"btc", "BTC", false},
		{"aqua", "AQUA", true},
	}
	for _, tc := range cases {
		t.Run(tc.slug, func(t *testing.T) {
			v, ok := cat.LookupBySlug(tc.slug)
			if !ok {
				t.Fatalf("LookupBySlug(%q): not found", tc.slug)
			}
			if v.Ticker != tc.ticker {
				t.Errorf("ticker = %q, want %q", v.Ticker, tc.ticker)
			}
			if tc.hasStellarClassic {
				se := v.StellarEntry()
				if se == nil {
					t.Fatalf("StellarEntry: nil for %s", tc.slug)
				}
				if se.Code == "" || se.Issuer == "" || se.AssetID == "" {
					t.Errorf("Stellar entry incomplete: code=%q issuer=%q asset_id=%q",
						se.Code, se.Issuer, se.AssetID)
				}
				// asset_id should be CODE-ISSUER format.
				want := se.Code + "-" + se.Issuer
				if se.AssetID != want {
					t.Errorf("asset_id %q != %q", se.AssetID, want)
				}
			}
		})
	}
}

func TestLookupByTicker_caseInsensitive(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	v1, ok1 := cat.LookupByTicker("USDC")
	v2, ok2 := cat.LookupByTicker("usdc")
	v3, ok3 := cat.LookupByTicker("UsDc")
	if !(ok1 && ok2 && ok3) {
		t.Fatalf("LookupByTicker: case sensitivity leaked (ok1=%v ok2=%v ok3=%v)", ok1, ok2, ok3)
	}
	if v1 != v2 || v2 != v3 {
		t.Error("LookupByTicker returned different pointers for case-variants — index is inconsistent")
	}
}

func TestLookupByStellarAssetID(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	const usdcID = "USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	v, ok := cat.LookupByStellarAssetID(usdcID)
	if !ok {
		t.Fatalf("LookupByStellarAssetID(%s): not found", usdcID)
	}
	if v.Slug != "usdc" {
		t.Errorf("slug = %q, want usdc", v.Slug)
	}

	// Native XLM is indexed as the literal "native" asset_id.
	v, ok = cat.LookupByStellarAssetID("native")
	if !ok {
		t.Fatal("LookupByStellarAssetID(native): not found")
	}
	if v.Ticker != "XLM" {
		t.Errorf("native ticker = %q, want XLM", v.Ticker)
	}

	// Unverified asset returns false.
	_, ok = cat.LookupByStellarAssetID("USDC-GBADISSUERSOMETHINGTHATWILLNEVERMATCHANYREALACCOUNTAB")
	if ok {
		t.Error("LookupByStellarAssetID returned a hit for an unverified asset")
	}
}

func TestStellarCollision(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}

	const realUSDCIssuer = "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN"
	const fakeIssuer = "GBADISSUERSOMETHINGTHATWILLNEVERMATCHANYREALACCOUNTAB"

	t.Run("real issuer = no collision", func(t *testing.T) {
		v, collision := cat.StellarCollision("USDC", realUSDCIssuer)
		if v == nil {
			t.Fatal("StellarCollision returned nil verified currency for real USDC")
		}
		if collision {
			t.Error("StellarCollision flagged the real USDC as a collision")
		}
	})

	t.Run("fake issuer with verified code = collision", func(t *testing.T) {
		v, collision := cat.StellarCollision("USDC", fakeIssuer)
		if v == nil {
			t.Fatal("StellarCollision returned nil for a code that IS verified")
		}
		if !collision {
			t.Error("StellarCollision missed the fake-issuer collision")
		}
		if v.Slug != "usdc" {
			t.Errorf("returned verified currency slug = %q, want usdc", v.Slug)
		}
	})

	t.Run("case-insensitive code match", func(t *testing.T) {
		_, collisionLower := cat.StellarCollision("usdc", fakeIssuer)
		_, collisionUpper := cat.StellarCollision("USDC", fakeIssuer)
		if collisionLower != collisionUpper {
			t.Errorf("case sensitivity in StellarCollision: lower=%v upper=%v", collisionLower, collisionUpper)
		}
	})

	t.Run("unknown code returns (nil, false)", func(t *testing.T) {
		v, collision := cat.StellarCollision("NOTAVERIFIEDCODE", fakeIssuer)
		if v != nil || collision {
			t.Errorf("StellarCollision for unknown code: v=%v collision=%v", v, collision)
		}
	})

	t.Run("empty inputs return (nil, false)", func(t *testing.T) {
		v, collision := cat.StellarCollision("", fakeIssuer)
		if v != nil || collision {
			t.Errorf("empty code: v=%v collision=%v", v, collision)
		}
		v, collision = cat.StellarCollision("USDC", "")
		if v != nil || collision {
			t.Errorf("empty issuer: v=%v collision=%v", v, collision)
		}
	})
}

func TestNilCatalogue(t *testing.T) {
	// Lookups on a nil catalogue should be safe — handlers may
	// invoke them when no catalogue is wired.
	var cat *Catalogue
	if v, ok := cat.LookupBySlug("usdc"); v != nil || ok {
		t.Errorf("nil catalogue LookupBySlug: v=%v ok=%v", v, ok)
	}
	if v, ok := cat.LookupByTicker("USDC"); v != nil || ok {
		t.Errorf("nil catalogue LookupByTicker: v=%v ok=%v", v, ok)
	}
	if v, ok := cat.LookupByStellarAssetID("native"); v != nil || ok {
		t.Errorf("nil catalogue LookupByStellarAssetID: v=%v ok=%v", v, ok)
	}
	if v, ok := cat.StellarCollision("USDC", "G123"); v != nil || ok {
		t.Errorf("nil catalogue StellarCollision: v=%v ok=%v", v, ok)
	}
}

func TestLoadFromBytes_validation(t *testing.T) {
	cases := []struct {
		name string
		yaml string
		want string // expected substring in the error
	}{
		{
			"missing ticker",
			`verified_currencies:
  - slug: foo
    name: Foo
    networks: [{network: stellar}]
`,
			"ticker is required",
		},
		{
			"missing slug",
			`verified_currencies:
  - ticker: FOO
    name: Foo
    networks: [{network: stellar}]
`,
			"slug is required",
		},
		{
			"missing networks",
			`verified_currencies:
  - ticker: FOO
    slug: foo
    name: Foo
`,
			"at least one network entry",
		},
		{
			"duplicate slug",
			`verified_currencies:
  - ticker: FOO
    slug: foo
    name: Foo
    networks: [{network: stellar}]
  - ticker: BAR
    slug: foo
    name: Bar
    networks: [{network: stellar}]
`,
			"duplicate slug",
		},
		{
			"duplicate ticker",
			`verified_currencies:
  - ticker: FOO
    slug: foo
    name: Foo
    networks: [{network: stellar}]
  - ticker: FOO
    slug: bar
    name: Bar
    networks: [{network: stellar}]
`,
			"duplicate ticker",
		},
		{
			"two currencies claim same code on stellar",
			`verified_currencies:
  - ticker: USDC
    slug: usdc
    name: USD Coin
    networks:
      - network: stellar
        code: USDC
        issuer: GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN
  - ticker: USDC2
    slug: usdc2
    name: Other USDC
    networks:
      - network: stellar
        code: USDC
        issuer: GBADISSUERSOMETHINGTHATWILLNEVERMATCHANYREALACCOUNTAB
`,
			"claimed by both",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadFromBytes([]byte(tc.yaml))
			if err == nil {
				t.Fatalf("LoadFromBytes: nil err, want error containing %q", tc.want)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("err = %q, want substring %q", err, tc.want)
			}
		})
	}
}

func TestCoinGeckoIDs(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	ids := cat.CoinGeckoIDs()
	if len(ids) == 0 {
		t.Fatal("CoinGeckoIDs: empty — seed entries are missing coingecko_id")
	}
	// Spot-check well-known entries that must have a CG slug.
	cases := map[string]string{
		"XLM":  "stellar",
		"USDC": "usd-coin",
		"BTC":  "bitcoin",
		"ETH":  "ethereum",
	}
	for ticker, want := range cases {
		got, ok := ids[ticker]
		if !ok {
			t.Errorf("CoinGeckoIDs: %s not present", ticker)
			continue
		}
		if got != want {
			t.Errorf("CoinGeckoIDs[%s] = %q, want %q", ticker, got, want)
		}
	}
	// Stellar-native tokens we don't expect CG to track shouldn't
	// appear (no `coingecko_id` in the seed).
	for _, ticker := range []string{"BLND", "PHO", "yUSDC"} {
		if _, ok := ids[ticker]; ok {
			t.Errorf("CoinGeckoIDs unexpectedly includes %s; the seed entry shouldn't have coingecko_id set", ticker)
		}
	}
}

func TestCoinMarketCapIDs(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	ids := cat.CoinMarketCapIDs()
	if len(ids) == 0 {
		t.Fatal("CoinMarketCapIDs: empty")
	}
	if got := ids["XLM"]; got != "512" {
		t.Errorf("CoinMarketCapIDs[XLM] = %q, want 512", got)
	}
	if got := ids["BTC"]; got != "1" {
		t.Errorf("CoinMarketCapIDs[BTC] = %q, want 1", got)
	}
}

func TestTickers(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	tickers := cat.Tickers()
	if len(tickers) != len(cat.All()) {
		t.Errorf("Tickers count %d != catalogue size %d", len(tickers), len(cat.All()))
	}
	for _, ticker := range tickers {
		if ticker == "" {
			t.Error("empty ticker in Tickers slice")
		}
		if ticker != strings.ToUpper(ticker) {
			t.Errorf("ticker %q not upper-cased", ticker)
		}
	}
}

func TestSeedFiatEntries_HaveExpectedShape(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	fiat := cat.ByClass(ClassFiat)
	if len(fiat) < 10 {
		t.Fatalf("seed has %d fiat entries; want at least 10", len(fiat))
	}
	wantPresent := map[string]bool{
		"us-dollar":     false,
		"euro":          false,
		"chinese-yuan":  false,
		"japanese-yen":  false,
		"british-pound": false,
		"indian-rupee":  false,
	}
	for _, vc := range fiat {
		if vc.Class != ClassFiat {
			t.Errorf("ByClass returned non-fiat entry: %+v", vc)
		}
		if vc.CirculatingSupply == "" {
			t.Errorf("fiat entry %q missing circulating_supply", vc.Slug)
		}
		if len(vc.Networks) != 0 {
			t.Errorf("fiat entry %q has networks (%d); fiat is network-agnostic",
				vc.Slug, len(vc.Networks))
		}
		if _, ok := wantPresent[vc.Slug]; ok {
			wantPresent[vc.Slug] = true
		}
	}
	for slug, present := range wantPresent {
		if !present {
			t.Errorf("required fiat slug %q missing from seed", slug)
		}
	}
}

func TestSeedClassDefaults(t *testing.T) {
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	usdc, _ := cat.LookupBySlug("usdc")
	if usdc.Class != ClassStablecoin {
		t.Errorf("usdc.Class = %q, want stablecoin", usdc.Class)
	}
	xlm, _ := cat.LookupBySlug("xlm")
	if xlm.Class != ClassCrypto {
		t.Errorf("xlm.Class = %q, want crypto (default)", xlm.Class)
	}
	cny, ok := cat.LookupBySlug("chinese-yuan")
	if !ok || cny.Class != ClassFiat {
		t.Errorf("chinese-yuan.Class = %q, want fiat", cny.Class)
	}
}

func TestLoadFromBytes_RejectsUnknownClass(t *testing.T) {
	y := `verified_currencies:
  - ticker: FOO
    slug: foo
    name: Foo
    class: not-a-real-class
    networks: [{network: stellar}]
`
	_, err := LoadFromBytes([]byte(y))
	if err == nil || !strings.Contains(err.Error(), "unknown class") {
		t.Errorf("expected 'unknown class' error, got %v", err)
	}
}

func TestSeedDataIntegrity(t *testing.T) {
	// Sanity-check: every entry with a Stellar classic network entry
	// must have non-empty code, issuer, and asset_id, and the
	// asset_id must be code-issuer concatenation.
	cat, err := LoadEmbedded()
	if err != nil {
		t.Fatalf("LoadEmbedded: %v", err)
	}
	for _, vc := range cat.All() {
		for _, n := range vc.Networks {
			if n.Network != "stellar" {
				continue
			}
			// Native XLM: asset_id == "native", code/issuer empty.
			if n.AssetID == "native" {
				if n.Code != "" || n.Issuer != "" {
					t.Errorf("%s native: expected empty code/issuer, got %q/%q",
						vc.Ticker, n.Code, n.Issuer)
				}
				continue
			}
			// Classic asset.
			if n.Code == "" || n.Issuer == "" || n.AssetID == "" {
				t.Errorf("%s stellar entry incomplete: code=%q issuer=%q asset_id=%q",
					vc.Ticker, n.Code, n.Issuer, n.AssetID)
				continue
			}
			want := n.Code + "-" + n.Issuer
			if n.AssetID != want {
				t.Errorf("%s asset_id = %q, want %q", vc.Ticker, n.AssetID, want)
			}
			// G-strkey must be 56 chars.
			if len(n.Issuer) != 56 || !strings.HasPrefix(n.Issuer, "G") {
				t.Errorf("%s issuer %q: not a valid G-strkey", vc.Ticker, n.Issuer)
			}
		}
	}
}
