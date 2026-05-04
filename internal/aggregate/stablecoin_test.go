package aggregate

import (
	"testing"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

func TestFiatProxy_USDPegged(t *testing.T) {
	for _, code := range []string{"USDT", "USDC", "DAI", "PYUSD", "USDP"} {
		t.Run(code, func(t *testing.T) {
			a, err := canonical.NewCryptoAsset(code)
			if err != nil {
				t.Fatalf("NewCryptoAsset(%q): %v", code, err)
			}
			got, ok := FiatProxy(a)
			if !ok {
				t.Fatalf("FiatProxy(%s) ok=false, want true", code)
			}
			if got.Type != canonical.AssetFiat || got.Code != "USD" {
				t.Errorf("FiatProxy(%s) = %+v, want fiat:USD", code, got)
			}
		})
	}
}

func TestFiatProxy_EURPegged(t *testing.T) {
	for _, code := range []string{"EURC", "EUROC", "EUROB"} {
		t.Run(code, func(t *testing.T) {
			a, _ := canonical.NewCryptoAsset(code)
			got, ok := FiatProxy(a)
			if !ok {
				t.Fatalf("FiatProxy(%s) ok=false", code)
			}
			if got.Code != "EUR" {
				t.Errorf("FiatProxy(%s).Code = %q, want EUR", code, got.Code)
			}
		})
	}
}

func TestFiatProxy_MXNePeg(t *testing.T) {
	a, _ := canonical.NewCryptoAsset("MXNe")
	got, ok := FiatProxy(a)
	if !ok {
		t.Fatal("FiatProxy(MXNe) ok=false")
	}
	if got.Code != "MXN" {
		t.Errorf("FiatProxy(MXNe).Code = %q, want MXN", got.Code)
	}
}

func TestFiatProxy_UnmappedCryptoReturnsFalse(t *testing.T) {
	// BTC / ETH / SOL are on the ADR-0014 allow-list but are NOT
	// stablecoins — they have no fiat peg and must not be proxied.
	for _, code := range []string{"BTC", "ETH", "SOL", "XLM", "XRP"} {
		t.Run(code, func(t *testing.T) {
			a, _ := canonical.NewCryptoAsset(code)
			_, ok := FiatProxy(a)
			if ok {
				t.Errorf("FiatProxy(%s) ok=true, want false — not a stablecoin", code)
			}
		})
	}
}

func TestFiatProxy_NonCryptoAssetsReturnFalse(t *testing.T) {
	// Fiat assets themselves are not stablecoin-crypto, so no
	// proxy mapping applies.
	fiat, _ := canonical.NewFiatAsset("USD")
	if _, ok := FiatProxy(fiat); ok {
		t.Error("FiatProxy(fiat:USD) ok=true, want false")
	}

	// Native XLM — no crypto-prefix form, no proxy.
	if _, ok := FiatProxy(canonical.NativeAsset()); ok {
		t.Error("FiatProxy(native) ok=true, want false")
	}

	// Classic-issued USDC is NOT proxied. Circle's Stellar-classic
	// USDC-GA5ZSEJY… carries full issuer identity; proxying it to
	// fiat:USD would conflate all classic USDC with the abstract
	// `crypto:USDC` ticker, which intentionally aren't Equal() under
	// canonical semantics.
	classicUSDC, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if _, ok := FiatProxy(classicUSDC); ok {
		t.Error("FiatProxy(classic USDC) ok=true, want false — must not coerce issuer-specific asset")
	}
}

func TestProxyPair_RewritesQuoteOnly(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdt, _ := canonical.NewCryptoAsset("USDT")
	p, _ := canonical.NewPair(xlm, usdt)

	got, ok := ProxyPair(p)
	if !ok {
		t.Fatal("ProxyPair(XLM/USDT) ok=false")
	}
	if !got.Base.Equal(xlm) {
		t.Errorf("ProxyPair base changed: got %s want %s", got.Base, xlm)
	}
	if got.Quote.Type != canonical.AssetFiat || got.Quote.Code != "USD" {
		t.Errorf("ProxyPair quote = %+v, want fiat:USD", got.Quote)
	}
}

func TestProxyPair_BaseSideStablecoinNotRewritten(t *testing.T) {
	// Base=USDC is a stablecoin, but ProxyPair only rewrites
	// quotes. This is the semantic guarantee: we don't recast a
	// USDC-denominated market by fiat-coercing its base.
	usdc, _ := canonical.NewCryptoAsset("USDC")
	xlm, _ := canonical.NewCryptoAsset("XLM")
	p, _ := canonical.NewPair(usdc, xlm)

	_, ok := ProxyPair(p)
	if ok {
		t.Error("ProxyPair(USDC/XLM) ok=true — should not rewrite base side")
	}
}

func TestProxyPair_NonStablecoinQuoteReturnsFalse(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	btc, _ := canonical.NewCryptoAsset("BTC")
	p, _ := canonical.NewPair(xlm, btc)

	_, ok := ProxyPair(p)
	if ok {
		t.Error("ProxyPair(XLM/BTC) ok=true, want false")
	}
}

func TestProxyPair_FiatQuoteReturnsFalse(t *testing.T) {
	// Already-fiat pair has nothing to proxy — caller should pass
	// these through unchanged.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	p, _ := canonical.NewPair(xlm, usd)

	_, ok := ProxyPair(p)
	if ok {
		t.Error("ProxyPair(XLM/fiat:USD) ok=true — already fiat, no proxy")
	}
}

func TestProxyTrade_RewritesPair(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdc, _ := canonical.NewCryptoAsset("USDC")
	p, _ := canonical.NewPair(xlm, usdc)

	src := canonical.Trade{
		Source:  "soroswap",
		Ledger:  42,
		TxHash:  "0000000000000000000000000000000000000000000000000000000000000000",
		OpIndex: 1,
		Pair:    p,
	}
	got, ok := ProxyTrade(src)
	if !ok {
		t.Fatal("ProxyTrade ok=false")
	}
	if got.Pair.Quote.Code != "USD" || got.Pair.Quote.Type != canonical.AssetFiat {
		t.Errorf("ProxyTrade.Pair.Quote = %+v, want fiat:USD", got.Pair.Quote)
	}
	// Non-pair fields must be preserved — the proxy is a pair
	// rewrite only, not a trade re-stamp.
	if got.Source != src.Source || got.Ledger != src.Ledger ||
		got.TxHash != src.TxHash || got.OpIndex != src.OpIndex {
		t.Errorf("ProxyTrade mutated non-pair fields: %+v", got)
	}
}

func TestProxyTrade_UnmappedReturnsOriginalAndFalse(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	btc, _ := canonical.NewCryptoAsset("BTC")
	p, _ := canonical.NewPair(xlm, btc)

	src := canonical.Trade{Source: "binance", Pair: p}
	got, ok := ProxyTrade(src)
	if ok {
		t.Error("ProxyTrade(XLM/BTC) ok=true, want false")
	}
	if !got.Pair.Equal(p) {
		t.Errorf("ProxyTrade returned mutated trade when ok=false: %+v", got)
	}
}

func TestFiatBackers_USD(t *testing.T) {
	got := FiatBackers("USD")
	want := map[string]bool{"USDT": true, "USDC": true, "DAI": true, "PYUSD": true, "USDP": true}
	if len(got) != len(want) {
		t.Fatalf("FiatBackers(USD) = %v (len %d), want %d entries", got, len(got), len(want))
	}
	for _, code := range got {
		if !want[code] {
			t.Errorf("unexpected USD backer: %q", code)
		}
	}
}

func TestFiatBackers_EUR(t *testing.T) {
	got := FiatBackers("EUR")
	want := map[string]bool{"EURC": true, "EUROC": true, "EUROB": true}
	if len(got) != len(want) {
		t.Fatalf("FiatBackers(EUR) = %v, want 3 entries", got)
	}
	for _, code := range got {
		if !want[code] {
			t.Errorf("unexpected EUR backer: %q", code)
		}
	}
}

func TestFiatBackers_UnpeggedFiatReturnsNil(t *testing.T) {
	// No stablecoin in the map targets GBP → nil result.
	if got := FiatBackers("GBP"); got != nil {
		t.Errorf("FiatBackers(GBP) = %v, want nil", got)
	}
}

func TestExpandTargetPair_FiatTargetReturnsDirectPlusBackers(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	target, _ := canonical.NewPair(xlm, usd)

	got, err := ExpandTargetPair(target)
	if err != nil {
		t.Fatalf("ExpandTargetPair: %v", err)
	}
	// 1 direct + 5 USD backers (USDT, USDC, DAI, PYUSD, USDP).
	if len(got) != 6 {
		t.Fatalf("ExpandTargetPair(XLM/USD) len=%d want 6: %v", len(got), got)
	}

	foundDirect := false
	foundBackers := map[string]bool{}
	for _, p := range got {
		switch p.Quote.Type {
		case canonical.AssetFiat:
			if p.Equal(target) {
				foundDirect = true
			}
		case canonical.AssetCrypto:
			foundBackers[p.Quote.Code] = true
		}
	}
	if !foundDirect {
		t.Error("ExpandTargetPair did not include direct XLM/fiat:USD target")
	}
	for _, want := range []string{"USDT", "USDC", "DAI", "PYUSD", "USDP"} {
		if !foundBackers[want] {
			t.Errorf("ExpandTargetPair missing backer XLM/crypto:%s", want)
		}
	}
}

func TestExpandTargetPair_NonFiatTargetReturnsOnlyItself(t *testing.T) {
	// Crypto/crypto target — no stablecoin expansion.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	btc, _ := canonical.NewCryptoAsset("BTC")
	target, _ := canonical.NewPair(xlm, btc)

	got, err := ExpandTargetPair(target)
	if err != nil {
		t.Fatalf("ExpandTargetPair: %v", err)
	}
	if len(got) != 1 || !got[0].Equal(target) {
		t.Errorf("ExpandTargetPair(XLM/BTC) = %v, want [%s]", got, target)
	}
}

func TestExpandTargetPairWithClassicPegs_USDFiatAddsClassicSources(t *testing.T) {
	// On Stellar mainnet most XLM/USD volume is quoted in classic
	// `USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`,
	// not the abstract `crypto:USDC` ticker. Without classic-peg
	// expansion the aggregator's XLM/fiat:USD VWAP would be empty
	// even with EnableStablecoinFiatProxy on — the bug observed in
	// production on r1 (vwap_writes_total stuck at 0 across 3h).
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	target, _ := canonical.NewPair(xlm, usd)

	circleUSDC, err := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")
	if err != nil {
		t.Fatalf("setup classic USDC: %v", err)
	}

	got, err := ExpandTargetPairWithClassicPegs(target, []canonical.Asset{circleUSDC})
	if err != nil {
		t.Fatalf("ExpandTargetPairWithClassicPegs: %v", err)
	}
	// 1 direct + 5 abstract backers (USDT/USDC/DAI/PYUSD/USDP) + 1 classic.
	if len(got) != 7 {
		t.Fatalf("len(got)=%d want 7: %v", len(got), got)
	}
	foundClassic := false
	for _, p := range got {
		if p.Quote.Type == canonical.AssetClassic && p.Quote.Code == "USDC" {
			foundClassic = true
		}
	}
	if !foundClassic {
		t.Errorf("ExpandTargetPairWithClassicPegs did not include the classic USDC source pair")
	}
}

func TestExpandTargetPairWithClassicPegs_NonUSDFiatIgnoresClassicPegs(t *testing.T) {
	// Operator's classic-peg list is implicitly USD; an EUR target
	// should NOT pull XLM/<classic-USDC> as a source.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	eur, _ := canonical.NewFiatAsset("EUR")
	target, _ := canonical.NewPair(xlm, eur)
	circleUSDC, _ := canonical.NewClassicAsset("USDC", "GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN")

	got, err := ExpandTargetPairWithClassicPegs(target, []canonical.Asset{circleUSDC})
	if err != nil {
		t.Fatalf("ExpandTargetPairWithClassicPegs: %v", err)
	}
	for _, p := range got {
		if p.Quote.Type == canonical.AssetClassic {
			t.Errorf("EUR target leaked classic USD peg into source list: %s", p)
		}
	}
}

func TestExpandTargetPairWithClassicPegs_NonClassicEntriesSkipped(t *testing.T) {
	// Defensive: a fiat or crypto entry on the USD pegs list is
	// silently dropped rather than crashing the tick loop.
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usd, _ := canonical.NewFiatAsset("USD")
	target, _ := canonical.NewPair(xlm, usd)
	usdtAbstract, _ := canonical.NewCryptoAsset("USDT") // crypto, not classic
	bareUSD, _ := canonical.NewFiatAsset("USD")         // fiat, not classic

	got, err := ExpandTargetPairWithClassicPegs(target, []canonical.Asset{usdtAbstract, bareUSD})
	if err != nil {
		t.Fatalf("ExpandTargetPairWithClassicPegs: %v", err)
	}
	// Same shape as ExpandTargetPair (no classic entries appended).
	if len(got) != 6 {
		t.Errorf("len(got)=%d want 6 (non-classic entries on the list must be skipped)", len(got))
	}
}

func TestExpandTargetPair_BaseCollisionWithBackerIsSkipped(t *testing.T) {
	// target USDC/fiat:USD — `base=crypto:USDC` collides with the
	// USDC backer pair (NewPair would build crypto:USDC/crypto:USDC
	// which fails validation). Other backers still expand cleanly.
	usdc, _ := canonical.NewCryptoAsset("USDC")
	usd, _ := canonical.NewFiatAsset("USD")
	target, _ := canonical.NewPair(usdc, usd)

	got, err := ExpandTargetPair(target)
	if err != nil {
		t.Fatalf("ExpandTargetPair: %v", err)
	}
	// Direct + 4 surviving backers (USDT, DAI, PYUSD, USDP) — USDC
	// skipped because of base collision.
	if len(got) != 5 {
		t.Errorf("ExpandTargetPair(USDC/USD) len=%d want 5: %v", len(got), got)
	}
	for _, p := range got {
		if p.Base.Code == p.Quote.Code && p.Quote.Type == canonical.AssetCrypto {
			t.Errorf("ExpandTargetPair produced self-collision: %s", p)
		}
	}
}

// TestFiatProxy_CodesAreOnCanonicalAllowList guards against a future
// addition to stablecoinFiatProxy for a ticker that's NOT on the
// canonical crypto allow-list. Such a code could never originate
// from a decoder (`NewCryptoAsset` would reject it), so the entry
// would be dead weight — this test forces us to add the ticker in
// both places.
func TestFiatProxy_CodesAreOnCanonicalAllowList(t *testing.T) {
	for code := range stablecoinFiatProxy {
		if !canonical.IsKnownCrypto(code) {
			t.Errorf("stablecoinFiatProxy key %q is not on canonical.IsKnownCrypto "+
				"— add it to internal/canonical/asset_crypto.go or remove it here",
				code)
		}
	}
}
