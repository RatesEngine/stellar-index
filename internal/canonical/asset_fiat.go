package canonical

// Off-chain fiat asset helpers — see ADR-0010.
//
// The Asset type carries an AssetFiat variant for off-chain fiat
// currencies (USD, EUR, …). These are NOT Stellar assets; they're
// abstract reference currencies used by oracle prices + FX feeds.
//
// Wire form: `fiat:<ISO4217>` (e.g. `fiat:USD`). The `fiat:` prefix
// is unambiguous, so ParseAsset dispatches in O(1).

// knownFiatCodes is the allow-list of 3-letter fiat codes. Extending
// it is a one-line amendment to ADR-0010 (never a superseding ADR).
// Codes chosen from ISO-4217 plus currencies the spec explicitly
// names or that our CEX/FX connectors will price against.
//
// 2026-04-23: extended after observing real Reflector FX oracle
// traffic — ARS (seen in mainnet capture under v6-2026-04-23/), plus
// a wider set of ISO-4217 fiat codes that Reflector-operator-grade
// FX feeds publish. Crypto tickers (BTC, ETH, SOL …) emitted by the
// CEX feed are NOT on this list — they represent a different asset
// class that needs its own canonical type (tracked separately,
// outside PR 164a's scope).
var knownFiatCodes = map[string]struct{}{
	"ARS": {}, "AUD": {}, "BRL": {}, "CAD": {}, "CHF": {}, "CLP": {},
	"CNY": {}, "COP": {}, "EUR": {}, "GBP": {}, "HKD": {}, "IDR": {},
	"ILS": {}, "INR": {}, "JPY": {}, "KRW": {}, "MXN": {}, "MYR": {},
	"NGN": {}, "NOK": {}, "NZD": {}, "PHP": {}, "PLN": {}, "RUB": {},
	"SEK": {}, "SGD": {}, "THB": {}, "TRY": {}, "UAH": {}, "USD": {},
	"VND": {}, "ZAR": {},
}

// IsKnownFiat reports whether code is in the ADR-0010 allow-list.
// Callers use this to validate operator-supplied fiat configuration
// before constructing an [Asset] at startup.
func IsKnownFiat(code string) bool {
	_, ok := knownFiatCodes[code]
	return ok
}

// NewFiatAsset constructs a fiat asset. Returns ErrInvalidAsset if
// the code isn't allow-listed.
func NewFiatAsset(code string) (Asset, error) {
	if !IsKnownFiat(code) {
		return Asset{}, errorf(ErrInvalidAsset, "unknown fiat code %q (see ADR-0010)", code)
	}
	return Asset{Type: AssetFiat, Code: code}, nil
}
