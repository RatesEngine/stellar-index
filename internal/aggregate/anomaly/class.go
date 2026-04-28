package anomaly

import (
	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// AssetClass classifies an asset for anomaly threshold lookup. Stable
// string values appear in operator config + Prometheus labels;
// renaming a class is a wire break.
//
// Classes from least-to-most-volatile under normal market conditions:
//
//   - [ClassStablecoin] — fiat-pegged tokens (USDC, USDT, PYUSD,
//     EUROC, EUROB, MXNe). Normal deviation < 0.1 %; anything > 1 %
//     is a depeg signal.
//   - [ClassTreasury] — tokenised treasuries (USTRY, T-bill tokens).
//     Track US-Treasury yields; expected deviation similar to
//     stablecoins.
//   - [ClassCrypto] — major crypto (XLM, BTC, ETH). Normal 1m
//     deviation 0.5–2 %; flash moves up to 5–10 %.
//   - [ClassGovernance] — DAO/protocol governance tokens (AQUA, ULTRA,
//     etc.). News-driven moves up to 30–50 % per day are routine.
//   - [ClassDefault] — everything not explicitly classified.
//     Conservative thresholds protect against worst-case unknown
//     behaviour.
type AssetClass string

const (
	// ClassStablecoin — fiat-pegged stablecoins. Tight thresholds
	// catch depegs.
	ClassStablecoin AssetClass = "stablecoin"

	// ClassTreasury — tokenised real-world treasuries. Same shape
	// as stablecoins.
	ClassTreasury AssetClass = "treasury"

	// ClassCrypto — major crypto with multi-source coverage.
	ClassCrypto AssetClass = "crypto"

	// ClassGovernance — protocol governance / utility tokens.
	// High legitimate volatility; loose thresholds.
	ClassGovernance AssetClass = "governance"

	// ClassDefault — fallback for unclassified assets. Used for
	// any asset not present in the operator's classifications map.
	ClassDefault AssetClass = "default"
)

// String makes AssetClass usable as a Prometheus label without a
// type assertion.
func (c AssetClass) String() string { return string(c) }

// AllClasses returns every defined class in canonical order.
// Useful for config validation + test enumeration.
func AllClasses() []AssetClass {
	return []AssetClass{
		ClassStablecoin,
		ClassTreasury,
		ClassCrypto,
		ClassGovernance,
		ClassDefault,
	}
}

// Classifier maps a canonical asset to its [AssetClass]. Phase-1
// classification is operator-curated via TOML config; Phase-2 will
// auto-classify based on observed volatility profile.
//
// Classifier is safe for concurrent use after construction. The
// underlying map is not mutated post-[NewClassifier].
type Classifier struct {
	// overrides keys on canonical.Asset.String() for stable lookup;
	// the value is the operator-assigned class.
	overrides map[string]AssetClass
}

// NewClassifier builds a Classifier from a `(asset_id_string →
// class)` map. The map keys are canonical.Asset string forms (e.g.
// "native", "USDC-GA5Z…", "fiat:USD"). Anything not present in the
// map falls through to [ClassDefault].
//
// Empty / nil overrides yields a Classifier that returns
// [ClassDefault] for every asset.
func NewClassifier(overrides map[string]AssetClass) *Classifier {
	cp := make(map[string]AssetClass, len(overrides))
	for k, v := range overrides {
		cp[k] = v
	}
	return &Classifier{overrides: cp}
}

// ClassOf returns the asset's class. Falls back to [ClassDefault]
// when the asset isn't in the operator's classification map.
func (c *Classifier) ClassOf(asset canonical.Asset) AssetClass {
	if cls, ok := c.overrides[asset.String()]; ok {
		return cls
	}
	return ClassDefault
}
