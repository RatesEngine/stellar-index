package canonical

// Off-chain crypto-ticker asset helpers — see ADR-0014.
//
// The Asset type carries an AssetCrypto variant for off-chain crypto
// reference tickers (BTC, ETH, USDT, …). These are NOT Stellar assets
// — they're abstract ticker references used by oracle prices (notably
// Reflector's CEX oracle) where the oracle quotes "BTC/USD" against
// a global crypto asset concept rather than any specific on-chain
// token.
//
// Wire form: `crypto:<TICKER>` (e.g. `crypto:BTC`). The `crypto:`
// prefix is unambiguous, so ParseAsset dispatches in O(1).
//
// Distinction from classic assets: `USDC:GA5ZSEJY…` is Circle's
// Stellar-classic USDC (a specific on-chain token), whereas
// `crypto:USDC` is the global USDC concept. They are intentionally
// NOT Equal() under canonical.Asset.

// knownCryptoCodes is the allow-list of recognized crypto tickers.
// Extension is a one-line amendment to ADR-0014 (never a superseding
// ADR). Codes chosen from mainnet Reflector CEX oracle traffic
// observed 2026-04-23 plus the largest-cap global crypto assets that
// are likely to appear.
var knownCryptoCodes = map[string]struct{}{
	"ADA": {}, "ATOM": {}, "AVAX": {}, "BCH": {}, "BNB": {},
	"BTC": {}, "DOGE": {}, "DOT": {}, "ETH": {}, "LINK": {},
	"LTC": {}, "MATIC": {}, "NEAR": {}, "SHIB": {}, "SOL": {},
	"TON": {}, "TRX": {}, "UNI": {}, "USDC": {}, "USDT": {},
	"XLM": {}, "XRP": {},
}

// IsKnownCrypto reports whether code is in the ADR-0014 allow-list.
// Callers use this to filter Reflector CEX-oracle symbols on the
// decoder hot path — unknown tickers are skipped rather than
// silently coerced.
func IsKnownCrypto(code string) bool {
	_, ok := knownCryptoCodes[code]
	return ok
}

// NewCryptoAsset constructs a crypto-ticker asset. Returns
// ErrInvalidAsset if the code isn't allow-listed.
func NewCryptoAsset(code string) (Asset, error) {
	if !IsKnownCrypto(code) {
		return Asset{}, errorf(ErrInvalidAsset, "unknown crypto code %q (see ADR-0014)", code)
	}
	return Asset{Type: AssetCrypto, Code: code}, nil
}
