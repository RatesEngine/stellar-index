package redstone

import "github.com/StellarAtlas/stellar-atlas/internal/canonical"

// feedEntry is one row of the RedStone feed registry: the canonical
// (base, quote) pair a feed_id prices.
type feedEntry struct {
	Base  canonical.Asset
	Quote canonical.Asset
}

// quoteUSD / quoteEUR are the two quote currencies the registry
// uses. RedStone publishes USD-denominated prices unless the feed_id
// carries an explicit `/<QUOTE>` suffix — only EUROC/EUR does today.
// See ADR-0028 §The RedStone 19-feed registry.
var (
	quoteUSD = mustFiat("USD")
	quoteEUR = mustFiat("EUR")
)

// feedRegistry maps each EXACT on-chain feed_id() string to the
// canonical (base, quote) pair it prices — the 19 RedStone Stellar
// mainnet feeds, captured on-chain 2026-05-22 (#53; see ADR-0028).
//
// The key is the string the relayer passes in
// write_prices(updater, feed_ids, payload) — which is NOT always the
// display name. EUROC's feed_id is `EUROC/EUR`; BENJI's is
// `BENJI_ETHEREUM_FUNDAMENTAL`. Matching a plain-ticker allow-list
// against these silently dropped 5 feeds (the pre-#53 bug — EUROC
// among them never decoded).
//
// Pre-#53 this was `canonical.IsKnownCrypto(feedID)`; an explicit
// registry is required because (a) feed_id ≠ ticker for 5 feeds and
// (b) the quote currency is per-feed, not a global USD assumption.
var feedRegistry = map[string]feedEntry{
	// Crypto / stablecoin feeds.
	"BTC":       {mustCrypto("BTC"), quoteUSD},
	"ETH":       {mustCrypto("ETH"), quoteUSD},
	"USDC":      {mustCrypto("USDC"), quoteUSD},
	"XLM":       {mustCrypto("XLM"), quoteUSD},
	"PYUSD":     {mustCrypto("PYUSD"), quoteUSD},
	"EUROC/EUR": {mustCrypto("EUROC"), quoteEUR}, // EUR-denominated — note the suffix
	"EUROB":     {mustCrypto("EUROB"), quoteUSD},
	"MXNe":      {mustCrypto("MXNe"), quoteUSD},

	// Tokenized-BTC feeds — BTC-backed crypto tokens (crypto, not rwa).
	"SolvBTC":                 {mustCrypto("SolvBTC"), quoteUSD},
	"SolvBTC_FUNDAMENTAL":     {mustCrypto("SolvBTC_FUNDAMENTAL"), quoteUSD},
	"SolvBTC.BBN_FUNDAMENTAL": {mustCrypto("SolvBTC.BBN_FUNDAMENTAL"), quoteUSD},

	// Tokenized real-world assets — ADR-0028 `rwa` AssetType.
	"BENJI_ETHEREUM_FUNDAMENTAL":  {mustRWA("BENJI"), quoteUSD},
	"iBENJI_ETHEREUM_FUNDAMENTAL": {mustRWA("iBENJI"), quoteUSD},
	"GILTS":                       {mustRWA("GILTS"), quoteUSD},
	"CETES":                       {mustRWA("CETES"), quoteUSD},
	"KTB":                         {mustRWA("KTB"), quoteUSD},
	"TESOURO":                     {mustRWA("TESOURO"), quoteUSD},
	"USTRY":                       {mustRWA("USTRY"), quoteUSD},
	"SPXU":                        {mustRWA("SPXU"), quoteUSD},
}

// lookupFeed resolves a feed_id to its registry entry. ok is false
// for a feed_id outside the registry — RedStone deploying a 20th
// feed surfaces here; the decoder skips + counts it, the same
// graceful per-feed skip as the pre-#53 unknown path.
func lookupFeed(feedID string) (entry feedEntry, ok bool) {
	entry, ok = feedRegistry[feedID]
	return entry, ok
}

// mustCrypto / mustRWA / mustFiat build a canonical reference asset
// for the registry. The codes are compile-time constants vetted
// against the ADR-0014 / ADR-0028 allow-lists — an error means a
// typo in this file, so panic at init rather than degrade silently.
func mustCrypto(code string) canonical.Asset {
	a, err := canonical.NewCryptoAsset(code)
	if err != nil {
		panic("redstone: feed registry: " + err.Error())
	}
	return a
}

func mustRWA(code string) canonical.Asset {
	a, err := canonical.NewRWAAsset(code)
	if err != nil {
		panic("redstone: feed registry: " + err.Error())
	}
	return a
}

func mustFiat(code string) canonical.Asset {
	a, err := canonical.NewFiatAsset(code)
	if err != nil {
		panic("redstone: feed registry: " + err.Error())
	}
	return a
}
