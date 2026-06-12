package reflector

import (
	"errors"
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// opIndexFanoutStride spaces the synthetic op_index values emitted
// from one Reflector event's price vector. 1024 comfortably holds
// any vector size we've observed (dozens of assets) with room to
// grow; well within uint32 since Stellar caps ops/tx at 100.
const opIndexFanoutStride = 1024

// reflectorTopicArity is the minimum topic count on a Reflector
// UpdateEvent: ["REFLECTOR", "update", <timestamp: u64>]. Anything
// shorter is by definition not our event.
const reflectorTopicArity = 3

// sanityFutureWindow bounds how far past the ledger close a decoded
// oracle timestamp may sit before we treat it as garbage and fall
// back to the ledger close time. Absorbs clock skew without admitting
// sentinel / overflow values that error the timestamptz INSERT
// (cf. the soroswap-router deadline_ts fix).
const sanityFutureWindow = 24 * time.Hour

// epochFloor is the lower sanity bound for a decoded oracle
// timestamp — anything before it (e.g. a 0 / pre-epoch value) is
// treated as garbage. Reflector launched well after 2020.
var epochFloor = time.Unix(1_000_000_000, 0).UTC() // 2001-09-09

// classify reports whether this is a Reflector "update" event. We
// match topic[0]=REFLECTOR + topic[1]=update; anything else returns
// false and the caller skips. We do NOT require topic[2] at this
// stage — a malformed topic arity is surfaced later as
// ErrMalformedPayload so the decode-errors metric catches it
// separately from the common "not our event" case.
func classify(e *events.Event) bool {
	if len(e.Topic) < 2 {
		return false
	}
	return e.Topic[0] == TopicSymbolReflector &&
		e.Topic[1] == TopicSymbolUpdate
}

// decodeUpdate converts one REFLECTOR.update event into a slice of
// canonical.OracleUpdate — one per (asset, price) pair in the
// event's update_data vector.
//
// Each OracleUpdate shares the same (ledger, tx_hash) but gets a
// distinct OpIndex derived from the vector index so identity stays
// unique in the oracle_updates hypertable.
//
// variant determines the source-name to stamp; decimals is the
// contract-declared price scale (typically 14); observer is the tx
// source account (the relayer).
func decodeUpdate(e *events.Event, variant Variant, decimals uint8, observer string, closedAt time.Time) ([]canonical.OracleUpdate, error) {
	if !classify(e) {
		return nil, ErrNotReflectorEvent
	}
	if len(e.Topic) < reflectorTopicArity {
		return nil, fmt.Errorf("%w: expected %d topics (REFLECTOR, update, timestamp), got %d",
			ErrMalformedPayload, reflectorTopicArity, len(e.Topic))
	}

	prices, err := decodeUpdateBody(e.Value)
	if err != nil {
		// Double-%w preserves both sentinels — callers can
		// errors.Is against ErrMalformedPayload (for the "any
		// decode problem" gate) AND against specific errors like
		// ErrUnknownSymbol (for targeted ops tooling).
		return nil, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}
	if len(prices) == 0 {
		return nil, ErrEmptyPrices
	}
	if len(prices) > opIndexFanoutStride {
		// Refuse rather than silently emitting PK-colliding rows.
		// See ErrPriceVectorOverflow for rationale.
		return nil, fmt.Errorf("%w: got %d prices", ErrPriceVectorOverflow, len(prices))
	}

	// Timestamp: the contract puts it in topic[2] as u64
	// MILLISECONDS (not seconds — verified against mainnet capture
	// 2026-04-23 + reflector-contract/oracle/src/price_oracle.rs:74,
	// which divides by 1000 to expose seconds via `last_timestamp`.
	// Internal storage is ms; the event carries the raw internal
	// value).
	//
	// Fall back to ledger close time if the topic decode fails so an
	// isolated encoding quirk doesn't drop an entire event's worth
	// of price updates.
	ts := closedAt
	if tsMs, terr := decodeUpdateTimestamp(e.Topic[2]); terr == nil {
		decoded := time.UnixMilli(int64(tsMs)).UTC()
		// Accept the contract timestamp only when it sits within a
		// sane window of the ledger close. A sentinel / garbage
		// far-future u64 (same class as the router deadline_ts
		// overflow) would otherwise overflow timestamptz range and
		// error the whole INSERT; a pre-epoch value would stamp a
		// bogus row. Outside the window we keep the ledger close time.
		if !decoded.Before(epochFloor) && !decoded.After(closedAt.Add(sanityFutureWindow)) {
			ts = decoded
		}
	}

	sourceName := variant.SourceName()
	out := make([]canonical.OracleUpdate, 0, len(prices))
	for i, entry := range prices {
		if entry.Price.Sign() <= 0 {
			// Reflector filters zero-price entries at the contract
			// level (oracle/src/events.rs:24 — zero prices skipped
			// before publish), so in practice we should never see
			// one. Defensive skip keeps us correct if the contract
			// relaxes that filter.
			continue
		}
		u := canonical.OracleUpdate{
			Source:     sourceName,
			ContractID: e.ContractID,
			Ledger:     e.Ledger,
			TxHash:     e.TxHash,
			// OpIndex uses a FIXED stride, not len(prices) —
			// otherwise two events in the same tx with different
			// vector sizes could collide on identity. See
			// ErrPriceVectorOverflow for the guard.
			OpIndex:   uint32(e.OperationIndex)*opIndexFanoutStride + uint32(i),
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
// Reflector contract. DEX quotes XLM; CEX + FX quote USD per the
// Reflector docs (ADR-0010 fiat sentinel).
func quoteForVariant(v Variant) canonical.Asset {
	switch v {
	case VariantDEX:
		return canonical.NativeAsset()
	case VariantCEX, VariantFX:
		return usdFiat
	default:
		return canonical.NativeAsset()
	}
}

// usdFiat is the implicit USD quote for CEX/FX Reflector variants.
// Parsed once at package init: a regression that drops USD from the
// fiat allow-list fires a loud init panic instead of silently
// writing zero-asset oracle updates on every Reflector event.
var usdFiat = mustUSDFiat()

func mustUSDFiat() canonical.Asset {
	a, err := canonical.NewFiatAsset("USD")
	if err != nil {
		panic("reflector: NewFiatAsset(\"USD\") must succeed: " + err.Error())
	}
	return a
}

// PriceEntry is one (asset, price) pair from the update_data vector.
type PriceEntry struct {
	Asset canonical.Asset
	Price canonical.Amount
}

// ─── Real SCVal decoders ────────────────────────────────────────
// Tests swap these via the package-level vars.

var (
	decodeUpdateBody      = sdkDecodeUpdateBody
	decodeUpdateTimestamp = sdkDecodeUpdateTimestamp
)

// sdkDecodeUpdateBody decodes Event.Value (base64 SCVal) into the
// payload emitted by Reflector's UpdateEvent.
//
// Contract reference: reflector-contract/oracle/src/events.rs:4-10
// (soroban-sdk 25.3.0 at VERSIONS.md's pinned SHA):
//
//	#[contractevent(topics = ["REFLECTOR", "update"])]
//	pub struct UpdateEvent {
//	    #[topic] timestamp: u64,
//	    update_data: Vec<(Val, i128)>,
//	}
//
// On the wire (verified 2026-04-23 against four mainnet DEX-oracle
// captures in test/fixtures/reflector/v6-2026-04-23/), the
// soroban-sdk #[contractevent] macro wraps non-topic fields in a
// Map keyed by field name — even when there is only one such
// field. So the body we receive is:
//
//	Map { "update_data": Vec<(Val, i128)> }
//
// NOT the raw Vec. We look up the field by name (per
// docs/architecture/contract-schema-evolution.md — decode-by-name-
// not-position lets us survive benign field additions across
// upgrades).
//
// `Val` in each pair is either:
//   - `ScAddress` — for Asset::Stellar(address); yields a
//     canonical.NewSorobanAsset(C-strkey).
//   - `ScSymbol`  — for Asset::Other(symbol); yields a
//     canonical.NewFiatAsset(symbol) when the symbol matches our
//     ADR-0010 allow-list, else ErrUnknownSymbol wrapped so
//     operators can decide whether to extend the allow-list.
//
// Per ADR-0013 + contract-schema-evolution.md this is the ONLY
// decoder path; tests override via the package-level var, not by
// editing this function.
func sdkDecodeUpdateBody(valueB64 string) ([]PriceEntry, error) {
	body, err := scval.Parse(valueB64)
	if err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}
	// Outer shape: Map { "update_data": Vec<(Val, i128)> }.
	entries, err := scval.AsMap(body)
	if err != nil {
		return nil, fmt.Errorf("body not a Map: %w", err)
	}
	updateDataSv, err := scval.MustMapField(entries, "update_data")
	if err != nil {
		return nil, fmt.Errorf("body map missing update_data: %w", err)
	}
	pairs, err := scval.AsVec(updateDataSv)
	if err != nil {
		return nil, fmt.Errorf("update_data not a Vec: %w", err)
	}
	out := make([]PriceEntry, 0, len(pairs))
	for i, pair := range pairs {
		entry, err := decodeUpdateDataEntry(pair)
		if err != nil {
			if errors.Is(err, ErrUnknownSymbol) {
				// Unknown symbol = gap in our canonical asset
				// model, not a structural event problem. Skip this
				// one entry and continue — losing a single asset
				// slot in a mixed-payload event is strictly better
				// than dropping all prices in that event.
				// F-1234 (codex audit-2026-05-12): count the skip
				// on ratesengine_source_unknown_symbols_total so
				// operators can spot upstream coverage drift
				// without parsing logs. The metric label uses the
				// generic "reflector" because the decoder is
				// shared across reflector-dex/cex/fx; the parent
				// dispatcher attributes per-contract context if
				// needed.
				obs.SourceUnknownSymbolsTotal.WithLabelValues("reflector").Inc()
				continue
			}
			return nil, fmt.Errorf("update_data[%d]: %w", i, err)
		}
		out = append(out, entry)
	}
	return out, nil
}

// decodeUpdateDataEntry turns one 2-tuple of the Vec<(Val, i128)>
// payload into a PriceEntry. Splits the union-dispatch on the asset
// slot (Address | Symbol) out from the outer loop so per-slot
// failures carry a clear index in their wrapping error.
func decodeUpdateDataEntry(pair scval.ScVal) (PriceEntry, error) {
	elts, err := scval.AsTupleN(pair, 2)
	if err != nil {
		return PriceEntry{}, fmt.Errorf("not a 2-tuple: %w", err)
	}
	kind, err := scval.DecodeAddressOrSymbol(elts[0])
	if err != nil {
		return PriceEntry{}, fmt.Errorf("asset: %w", err)
	}
	var asset canonical.Asset
	switch {
	case kind.Address != "":
		asset, err = canonical.NewSorobanAsset(kind.Address)
		if err != nil {
			return PriceEntry{}, fmt.Errorf("soroban asset from %q: %w", kind.Address, err)
		}
	case kind.Symbol != "":
		// Reflector's Asset::Other(Symbol) covers both fiat tickers
		// (FX oracle: "USD", "EUR", "ARS" …) and crypto tickers
		// (CEX oracle: "BTC", "ETH", "USDT" …). Try fiat first
		// (smaller allow-list, ISO-4217-constrained), then crypto
		// (ADR-0014). Anything matching neither skips out via
		// ErrUnknownSymbol — consumer continues processing
		// the rest of the event.
		switch {
		case canonical.IsKnownFiat(kind.Symbol):
			asset, err = canonical.NewFiatAsset(kind.Symbol)
			if err != nil {
				return PriceEntry{}, fmt.Errorf("fiat asset from %q: %w", kind.Symbol, err)
			}
		case canonical.IsKnownCrypto(kind.Symbol):
			asset, err = canonical.NewCryptoAsset(kind.Symbol)
			if err != nil {
				return PriceEntry{}, fmt.Errorf("crypto asset from %q: %w", kind.Symbol, err)
			}
		default:
			return PriceEntry{}, fmt.Errorf("%w: symbol %q matches neither fiat (ADR-0010) nor crypto (ADR-0014) allow-lists",
				ErrUnknownSymbol, kind.Symbol)
		}
	default:
		return PriceEntry{}, fmt.Errorf("%w: asset slot is neither Address nor Symbol",
			ErrMalformedPayload)
	}
	price, err := scval.AsAmountFromI128(elts[1])
	if err != nil {
		return PriceEntry{}, fmt.Errorf("price: %w", err)
	}
	return PriceEntry{Asset: asset, Price: price}, nil
}

// sdkDecodeUpdateTimestamp decodes Event.Topic[2] (base64 SCVal U64)
// into a uint64 seconds timestamp. Matches the #[topic] declaration
// on UpdateEvent.timestamp.
func sdkDecodeUpdateTimestamp(topicB64 string) (uint64, error) {
	sv, err := scval.Parse(topicB64)
	if err != nil {
		return 0, fmt.Errorf("parse topic[2]: %w", err)
	}
	return scval.AsU64(sv)
}
