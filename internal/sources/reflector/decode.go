package reflector

import (
	"fmt"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/stellarrpc"
)

// classify reports whether this is a Reflector "update" event. We
// match both topics (position 0 = "REFLECTOR", position 1 =
// "update"); anything else returns false and the caller skips.
func classify(e *stellarrpc.Event) bool {
	if len(e.Topic) < 2 {
		return false
	}
	return e.Topic[0] == TopicSymbolReflector &&
		e.Topic[1] == TopicSymbolUpdate
}

// decodeUpdate converts one REFLECTOR.update event into a slice
// of canonical.OracleUpdate — one per (asset, price) pair in the
// event's prices vector.
//
// Each OracleUpdate shares the same (ledger, tx_hash) but gets a
// distinct OpIndex derived from the prices-vector index so
// identity stays unique in the oracle_updates hypertable.
//
// variant determines which source-name to stamp; decimals is the
// contract-declared price scale (typically 14, see [DefaultDecimals]);
// observer is the tx source account (the relayer — Q4).
func decodeUpdate(e *stellarrpc.Event, variant Variant, decimals uint8, observer string, closedAt time.Time) ([]canonical.OracleUpdate, error) {
	if !classify(e) {
		return nil, ErrNotReflectorEvent
	}

	prices, timestamp, err := decodeUpdateBody(e.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrMalformedPayload, err)
	}
	if len(prices) == 0 {
		return nil, ErrEmptyPrices
	}

	// Prefer the oracle's own timestamp when present; fall back to
	// ledger close time. On-chain, timestamp is second-precision.
	ts := closedAt
	if timestamp > 0 {
		ts = time.Unix(int64(timestamp), 0).UTC()
	}

	sourceName := variant.SourceName()
	out := make([]canonical.OracleUpdate, 0, len(prices))
	for i, entry := range prices {
		if entry.Price.Sign() <= 0 {
			// Reflector sometimes emits zero-price entries for
			// deactivated assets. Skip — the canonical.Validate
			// would reject them anyway.
			continue
		}
		u := canonical.OracleUpdate{
			Source:     sourceName,
			ContractID: e.ContractID,
			Ledger:     e.Ledger,
			TxHash:     e.TxHash,
			// OpIndex = tx-level operation-index × vector-size + vector-position
			// keeps identity unique even when two price updates
			// land in the same (ledger, tx) pair.
			OpIndex:   uint32(e.OperationIndex)*uint32(len(prices)) + uint32(i),
			Timestamp: ts,
			Asset:     entry.Asset,
			Quote:     quoteForVariant(variant),
			Price:     entry.Price,
			Decimals:  decimals,
			Observer:  observer,
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, ErrEmptyPrices
	}
	return out, nil
}

// quoteForVariant returns the implicit quote-currency for a given
// Reflector contract.
//
// DEX prices are quoted in Stellar's native XLM (Reflector DEX
// reads SDEX trades). CEX + FX prices are quoted in USD per the
// Reflector docs. We use the ADR-0010 fiat sentinel for USD.
func quoteForVariant(v Variant) canonical.Asset {
	switch v {
	case VariantDEX:
		return canonical.NativeAsset()
	case VariantCEX, VariantFX:
		// NewFiatAsset("USD") can't error — USD is in the allow-list.
		a, _ := canonical.NewFiatAsset("USD")
		return a
	default:
		return canonical.NativeAsset()
	}
}

// PriceEntry is one (asset, price) pair from the prices vector.
type PriceEntry struct {
	Asset canonical.Asset
	Price canonical.Amount
}

// ─── Stubs awaiting the SDK-backed decoder ─────────────────────────
// Tests swap these via the package-level var.

var decodeUpdateBody = stubDecodeUpdateBody

// stubDecodeUpdateBody returns an error so the fail-closed path is
// the default. Tests override to synthesise real shapes; the real
// implementation lands with the SDK dep (SCVal Map + Vec + Address
// decoding).
func stubDecodeUpdateBody(valueB64 string) (prices []PriceEntry, timestamp uint64, err error) {
	return nil, 0, fmt.Errorf("reflector: SCVal decoder not yet installed (TODO(#0))")
}
