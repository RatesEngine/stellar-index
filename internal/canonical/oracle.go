package canonical

import (
	"fmt"
	"math/big"
	"time"
)

// OracleUpdate is one price observation published by an on-chain or
// off-chain oracle (Reflector, Redstone, Band, Chainlink-HTTP, …).
//
// Identity mirrors [Trade]: (Source, Ledger, TxHash, OpIndex) is the
// unique key, giving us the same stable cross-region dedup semantics
// for oracle feeds as we have for trades.
//
// # Price representation
//
// Oracles return prices at source-specific fixed scales. Reflector
// Pulse emits i128 price values at a contract-declared `decimals`
// (typically 14). Band emits E9-scaled relayed rates + E18-scaled
// pair rates. Redstone emits U256 values at 8 decimals for most
// feeds.
//
// Rather than normalise to a single decimal choice at ingest time
// (which either loses precision or forces a float), we preserve the
// raw integer in [Price] and record the source-declared [Decimals].
// The aggregation layer scales on read per query need:
//
//	decimalValue := new(big.Float).Quo(
//	    new(big.Float).SetInt(update.Price.BigInt()),
//	    new(big.Float).SetInt(new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(update.Decimals)), nil)),
//	)
//
// That arithmetic lives in `internal/aggregate/scale.go`
// (future), not here.
//
// # Pair vs single-asset
//
// Some oracle sources emit single-asset-USD updates ("XLM = 0.12 USD");
// others emit cross-pair ("XLM/EUR = 0.11"). [Quote] is the
// denominator asset for the price; for single-asset-USD the Quote is
// a synthetic "USD" off-chain reference (not a Stellar asset). We
// represent those as ClassicAsset{Code:"USD", Issuer:"<fixed-sentinel-issuer>"}
// per ADR TBD, but for now [Quote] can be the literal
// `Asset{Type: AssetClassic, Code: "USD"}` with an empty Issuer and
// a documentation note.
//
// TODO(#0): file an ADR for the off-chain fiat representation in
// canonical form. Current placeholder is code-only Asset; not ideal.
type OracleUpdate struct {
	// Source is the oracle name: "reflector-dex", "reflector-cex",
	// "reflector-fx", "redstone", "band", "chainlink-http",
	// "coingecko", "coinmarketcap". Stable — part of identity.
	Source string `json:"source"`

	// ContractID is the originating contract address for on-chain
	// oracles (Reflector's per-data-source contract, Band's
	// StandardReference). Empty string for off-chain sources.
	ContractID string `json:"contract_id,omitempty"`

	// Ledger, TxHash, OpIndex — on-chain identity. For off-chain
	// sources we synthesise Ledger as "0", TxHash as the payload
	// hash, OpIndex as 0.
	Ledger  uint32 `json:"ledger"`
	TxHash  string `json:"tx_hash"`
	OpIndex uint32 `json:"op_index"`

	// Timestamp is the oracle's published timestamp, not the ledger
	// close time. Always UTC.
	Timestamp time.Time `json:"ts"`

	// Asset is the base being priced.
	Asset Asset `json:"asset"`

	// Quote is the denominator asset (typically USD; may be another
	// Stellar asset for cross-pair oracles).
	Quote Asset `json:"quote"`

	// Price is the raw integer value at [Decimals] scale. Never
	// truncated — ADR-0003.
	Price Amount `json:"price"`

	// Decimals is the source-declared scale for [Price]. Typical
	// values: 14 (Reflector Pulse), 8 (Redstone per-feed), 9 (Band
	// relayed), 18 (Band pair).
	Decimals uint8 `json:"decimals"`

	// Confidence is an optional 0–1.0 confidence score. Oracles that
	// don't publish one leave it at 0. See
	// internal/divergence/* for how this flows into API
	// divergence_warning flags.
	Confidence float64 `json:"confidence,omitempty"`

	// Observer is an optional attribution — for on-chain oracle
	// events the transaction submitter; useful for auditing
	// Reflector relayers.
	Observer string `json:"observer,omitempty"`
}

// ID returns the stable identifier used as primary key in the
// `oracle_updates` hypertable and the dedup key across regions.
//
// Format: `<source>:<ledger>:<tx_hash>:<op_index>`.
func (u OracleUpdate) ID() string {
	return fmt.Sprintf("%s:%d:%s:%d", u.Source, u.Ledger, u.TxHash, u.OpIndex)
}

// Validate returns nil iff every invariant holds. Callers in the
// ingestion path should call this before writing; a violation is
// always an upstream bug.
func (u OracleUpdate) Validate() error {
	if u.Source == "" {
		return fmt.Errorf("%w: empty source", ErrInvalidOracle)
	}
	if u.TxHash == "" {
		return fmt.Errorf("%w: empty tx_hash", ErrInvalidOracle)
	}
	if !validTxHash(u.TxHash) {
		return fmt.Errorf("%w: tx_hash %q is not 64 hex chars", ErrInvalidOracle, u.TxHash)
	}
	if u.Timestamp.IsZero() {
		return fmt.Errorf("%w: zero timestamp", ErrInvalidOracle)
	}
	if err := u.Asset.Validate(); err != nil {
		return fmt.Errorf("%w: asset: %w", ErrInvalidOracle, err)
	}
	// Quote validates via the ordinary Asset.Validate path —
	// AssetFiat is a first-class variant per ADR-0010. No sentinel.
	if err := u.Quote.Validate(); err != nil {
		return fmt.Errorf("%w: quote: %w", ErrInvalidOracle, err)
	}
	if u.Price.Sign() <= 0 {
		return fmt.Errorf("%w: price must be positive, got %s", ErrInvalidOracle, u.Price)
	}
	if u.Decimals > 38 {
		return fmt.Errorf("%w: decimals %d exceeds NUMERIC precision limit (38)", ErrInvalidOracle, u.Decimals)
	}
	if u.Confidence < 0 || u.Confidence > 1 {
		return fmt.Errorf("%w: confidence %f out of [0,1]", ErrInvalidOracle, u.Confidence)
	}
	// Observer is optional (off-chain sources synthesise it empty),
	// but when present it MUST be a valid G-strkey. Empty string
	// is NOT a valid G-strkey so we can't call validateAccountID
	// unconditionally.
	if u.Observer != "" {
		if err := validateAccountID(u.Observer); err != nil {
			return fmt.Errorf("%w: observer: %w", ErrInvalidOracle, err)
		}
	}
	// ContractID is optional (off-chain sources don't have one).
	// When present it MUST be a valid C-strkey.
	if u.ContractID != "" {
		if err := validateContractID(u.ContractID); err != nil {
			return fmt.Errorf("%w: contract_id: %w", ErrInvalidOracle, err)
		}
	}
	return nil
}

// PriceFloat returns Price / 10^Decimals as a *big.Float. Convenience
// for display / triangulation; never store the result — it's lossy
// beyond big.Float's 53-bit mantissa default.
func (u OracleUpdate) PriceFloat() *big.Float {
	f := new(big.Float).SetInt(u.Price.BigInt())
	if u.Decimals == 0 {
		return f
	}
	scale := new(big.Float).SetInt(
		new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(u.Decimals)), nil),
	)
	return new(big.Float).Quo(f, scale)
}

// Equal compares by identity only (Source + Ledger + TxHash +
// OpIndex). Confidence + Observer are observational; two records of
// the same underlying publication from different observers should
// still be Equal.
func (u OracleUpdate) Equal(o OracleUpdate) bool {
	return u.Source == o.Source &&
		u.Ledger == o.Ledger &&
		u.TxHash == o.TxHash &&
		u.OpIndex == o.OpIndex
}

// ─── internal helpers ──────────────────────────────────────────────

// (Formerly had isFiatSentinel here — removed per ADR-0010 which
// promoted fiat to a first-class AssetType variant.)
//
// validTxHash defined in trade.go; we re-use it here.
