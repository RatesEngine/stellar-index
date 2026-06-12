package divergence_test

import (
	"context"
	"encoding/json"
	"math/big"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/aggregate"
	"github.com/StellarAtlas/stellar-atlas/internal/cachekeys"
	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/divergence"
)

// TestStablecoinDepeg_DivergenceWorkerFires wires the stablecoin
// late-binding policy (ADR-0026) together with the divergence
// safety net (ADR-0019 §"Divergence"). The two are designed as a
// pair: the aggregator deliberately conceals stablecoin↔fiat
// drift so XLM/USDC trades flow into the same XLM/USD bucket as
// XLM/USDT and direct XLM/fiat:USD; the divergence worker is the
// safety net that fires `flags.divergence_warning` when our
// (concealed-depeg) price drifts from external references.
//
// Without this test, the late-binding rewrite + the divergence
// service could silently regress in opposite directions and
// leave the safety net broken — exactly the gap F-1230
// (audit-2026-05-12) flagged.
//
// Scenario:
//
//   - 1 XLM observed trading for 0.10 USDC on Soroswap.
//   - aggregate.ProxyTrade rewrites the pair to XLM/fiat:USD,
//     leaving amounts untouched (the policy is "stablecoin ≈
//     fiat at aggregator layer", not "renormalise the i128").
//   - The aggregator publishes the resulting price as our
//     XLM/USD = 0.10 (the depeg is invisible at this layer).
//   - But USDC actually depegs to $0.95 — true XLM/USD is ~0.0950.
//   - References (CoinGecko + Chainlink + Reflector) all see the
//     true price.
//   - divergence.RefreshPair(XLM/USD, our=0.10) fires
//     WarningFired=true because the delta exceeds the threshold.
func TestStablecoinDepeg_DivergenceWorkerFires(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdc, _ := canonical.NewCryptoAsset("USDC")
	xlmUSDCPair, err := canonical.NewPair(xlm, usdc)
	if err != nil {
		t.Fatalf("build XLM/USDC pair: %v", err)
	}

	// Step 1 — simulate one Soroswap trade: 1 XLM ↔ 0.10 USDC.
	trade := canonical.Trade{
		Source:      "soroswap",
		Ledger:      52_000_000,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000000",
		OpIndex:     1,
		Pair:        xlmUSDCPair,
		BaseAmount:  canonical.NewAmount(big.NewInt(10_000_000)), // 1 XLM @ 7-decimals
		QuoteAmount: canonical.NewAmount(big.NewInt(1_000_000)),  // 0.10 USDC @ 7-decimals
	}

	// Step 2 — apply the aggregator's stablecoin proxy. ProxyTrade
	// rewrites the pair to XLM/fiat:USD; amounts stay untouched
	// (the policy assumes USDC ≈ USD). This is exactly the
	// concealment path ADR-0026 documents.
	proxied, ok := aggregate.ProxyTrade(trade)
	if !ok {
		t.Fatalf("ProxyTrade should rewrite XLM/USDC → XLM/fiat:USD")
	}
	if proxied.Pair.Quote.Type != canonical.AssetFiat || proxied.Pair.Quote.Code != "USD" {
		t.Fatalf("proxied quote = %+v, want fiat:USD", proxied.Pair.Quote)
	}

	// Step 3 — what our aggregator would publish: VWAP from the
	// proxied trades. With one trade the VWAP is just the trade
	// price; no need to invoke the orchestrator. The amounts are
	// 7-decimal stroops; price is 0.10.
	const ourPrice = 0.10

	// Step 4 — the actual market: USDC depegged to 0.95 USD this
	// hour, so the true XLM/USD is 0.10 × 0.95 = 0.0950. Three
	// independent references see that.
	refs := []divergence.Reference{
		&stubReference{name: "coingecko", price: 0.0950},
		&stubReference{name: "chainlink", price: 0.0950},
		&stubReference{name: "reflector", price: 0.0950},
	}
	svc, rdb, _ := newTestService(t, refs, divergence.ServiceOptions{
		Threshold:            2.5, // depeg-grade threshold (matches r1 prod)
		MinSourcesForWarning: 2,
	})

	// Step 5 — pretend the aggregator just refreshed the pair.
	// The divergence worker compares our (concealed-depeg) price
	// against the references and writes the result to Redis.
	if err := svc.RefreshPair(context.Background(), proxied.Pair, ourPrice, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}

	// Step 6 — assert the safety net fired. Our 0.10 vs reference
	// 0.0950 is ~5.26% delta, above the 2.5% threshold; with 3
	// agreeing references and MinSources=2 the warning must fire.
	body, err := rdb.Get(context.Background(), cachekeys.Divergence(proxied.Pair)).Bytes()
	if err != nil {
		t.Fatalf("redis get %s: %v", cachekeys.Divergence(proxied.Pair), err)
	}
	var cached divergence.CachedResult
	if err := json.Unmarshal(body, &cached); err != nil {
		t.Fatalf("unmarshal CachedResult: %v", err)
	}
	if !cached.WarningFired {
		t.Errorf("WarningFired = false on simulated USDC depeg "+
			"(our=%g vs ref=%g, delta ~5.3%%); the divergence safety "+
			"net is supposed to compensate for stablecoin late-binding "+
			"concealment per ADR-0026 + F-1230",
			ourPrice, 0.0950)
	}
	if cached.SuccessCount != 3 {
		t.Errorf("SuccessCount = %d, want 3 (all stub references should report)", cached.SuccessCount)
	}
}

// TestStablecoinPegHolds_DivergenceWorkerStaysQuiet — symmetric
// negative case: when the stablecoin holds its peg, our concealed-
// fiat price matches the references and the divergence worker
// must NOT fire. Catches a regression that makes the warning
// fire on the steady state.
func TestStablecoinPegHolds_DivergenceWorkerStaysQuiet(t *testing.T) {
	xlm, _ := canonical.NewCryptoAsset("XLM")
	usdc, _ := canonical.NewCryptoAsset("USDC")
	xlmUSDCPair, _ := canonical.NewPair(xlm, usdc)

	trade := canonical.Trade{
		Source:      "soroswap",
		Ledger:      52_000_000,
		TxHash:      "0000000000000000000000000000000000000000000000000000000000000001",
		Pair:        xlmUSDCPair,
		BaseAmount:  canonical.NewAmount(big.NewInt(10_000_000)),
		QuoteAmount: canonical.NewAmount(big.NewInt(1_000_000)),
	}
	proxied, ok := aggregate.ProxyTrade(trade)
	if !ok {
		t.Fatalf("ProxyTrade should rewrite")
	}

	const ourPrice = 0.10
	refs := []divergence.Reference{
		&stubReference{name: "coingecko", price: 0.10},
		&stubReference{name: "chainlink", price: 0.10},
		&stubReference{name: "reflector", price: 0.10},
	}
	svc, rdb, _ := newTestService(t, refs, divergence.ServiceOptions{
		Threshold:            2.5,
		MinSourcesForWarning: 2,
	})

	if err := svc.RefreshPair(context.Background(), proxied.Pair, ourPrice, time.Now()); err != nil {
		t.Fatalf("RefreshPair: %v", err)
	}
	body, _ := rdb.Get(context.Background(), cachekeys.Divergence(proxied.Pair)).Bytes()
	var cached divergence.CachedResult
	_ = json.Unmarshal(body, &cached)
	if cached.WarningFired {
		t.Errorf("WarningFired = true on peg-holds steady state; "+
			"divergence safety net is over-firing (our=%g vs ref=0.10)", ourPrice)
	}
}
