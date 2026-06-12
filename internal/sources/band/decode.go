package band

import (
	"errors"
	"fmt"
	"math/big"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/obs"
	"github.com/RatesEngine/rates-engine/internal/scval"
)

// opIndexFanoutStride is the synthetic OpIndex spacing per Band
// call, analogous to the Reflector / Redstone strides. Band's
// symbol_rates vector in practice is small (one batch per relayer
// submission), well under 1024.
const opIndexFanoutStride = 1024

// sanityFutureWindow bounds how far past the ledger close a decoded
// oracle timestamp may sit before we treat it as garbage and fall
// back to the ledger close time. A real relayer payload is at or
// before the ledger close; a small grace window absorbs clock skew
// without admitting sentinel / overflow values that error the
// timestamptz INSERT (cf. the soroswap-router deadline_ts fix).
const sanityFutureWindow = 24 * time.Hour

// decodeRelayArgs converts one Band relay/force_relay InvokeContract
// call into a slice of canonical.OracleUpdate — one per (symbol,
// rate) pair in symbol_rates.
//
// fnName selects between the two function shapes:
//
//   - relay:       args = [from, symbol_rates, resolve_time, request_id]
//   - force_relay: args = [symbol_rates, resolve_time, request_id]
//
// Updates share (ledger, tx_hash) but get distinct OpIndex values
// derived from the vector position, mirroring the Reflector /
// Redstone fan-out.
func decodeRelayArgs( //nolint:gocognit,gocyclo,funlen // dispatch-heavy; splitting would reduce linearity
	fnName string,
	args []string,
	contractID string,
	ledger uint32,
	txHash string,
	opIndex int,
	opSource, txSource string,
	closedAt time.Time,
) ([]canonical.OracleUpdate, error) {
	var ratesIdx, timeIdx int
	var relayerFrom string // observer strkey

	switch fnName {
	case FnRelay:
		if len(args) < 4 {
			return nil, fmt.Errorf("%w: relay expects 4 args, got %d", ErrMalformedArgs, len(args))
		}
		// args[0] = from: Address
		fromSv, err := scval.Parse(args[0])
		if err != nil {
			return nil, fmt.Errorf("%w: args[0] from: %w", ErrMalformedArgs, err)
		}
		relayerFrom, err = scval.AsAddressStrkey(fromSv)
		if err != nil {
			return nil, fmt.Errorf("%w: args[0] from: %w", ErrMalformedArgs, err)
		}
		ratesIdx, timeIdx = 1, 2
	case FnForceRelay:
		if len(args) < 3 {
			return nil, fmt.Errorf("%w: force_relay expects 3 args, got %d", ErrMalformedArgs, len(args))
		}
		// No `from` arg — admin-only path. Observer falls back to the
		// op source account (or tx source) so the row still carries
		// attribution rather than being anonymous.
		relayerFrom = pickObserver(opSource, txSource)
		ratesIdx, timeIdx = 0, 1
	default:
		return nil, ErrNotBandCall
	}

	// args[ratesIdx] = symbol_rates: Vec<(Symbol, u64)>.
	// Bound guaranteed by the len(args) check in the switch above
	// — FnRelay: len ≥ 4, ratesIdx = 1; FnForceRelay: len ≥ 3,
	// ratesIdx = 0. gosec can't trace the invariant across cases.
	ratesSv, err := scval.Parse(args[ratesIdx]) //nolint:gosec // bounds-checked in switch case above
	if err != nil {
		return nil, fmt.Errorf("%w: symbol_rates: %w", ErrMalformedArgs, err)
	}
	pairs, err := scval.AsVec(ratesSv)
	if err != nil {
		return nil, fmt.Errorf("%w: symbol_rates not a Vec: %w", ErrMalformedArgs, err)
	}
	if len(pairs) == 0 {
		return nil, ErrEmptyRates
	}
	if len(pairs) > opIndexFanoutStride {
		return nil, fmt.Errorf("band: symbol_rates length %d exceeds fanout stride %d",
			len(pairs), opIndexFanoutStride)
	}

	// args[timeIdx] = resolve_time: u64 (UNIX seconds).
	// Bound guaranteed by the len(args) check in the switch above
	// — FnRelay: len ≥ 4, timeIdx = 2; FnForceRelay: len ≥ 3,
	// timeIdx = 1. gosec can't trace the invariant across cases.
	timeSv, err := scval.Parse(args[timeIdx]) //nolint:gosec // bounds-checked in switch case above
	if err != nil {
		return nil, fmt.Errorf("%w: resolve_time: %w", ErrMalformedArgs, err)
	}
	resolveSeconds, err := scval.AsU64(timeSv)
	if err != nil {
		return nil, fmt.Errorf("%w: resolve_time: %w", ErrMalformedArgs, err)
	}
	ts := time.Unix(int64(resolveSeconds), 0).UTC()
	// Defensive fallback: a relayer submitting resolve_time=0 (or
	// pre-epoch values) would stamp a bogus timestamp on the row.
	// Symmetrically, a sentinel / garbage far-future u64 (e.g.
	// ~3e18s, the same class as the router deadline_ts overflow) can
	// overflow timestamptz range and error the whole INSERT. Clamp
	// both ends to ledger close time. Real-world Band payloads are
	// always post-2020 UNIX seconds at or before the ledger close.
	if resolveSeconds < 1_000_000_000 || ts.After(closedAt.Add(sanityFutureWindow)) {
		ts = closedAt
	}

	usdQuote, err := canonical.NewFiatAsset("USD")
	if err != nil {
		return nil, fmt.Errorf("band: USD fiat quote unavailable: %w", err)
	}

	out := make([]canonical.OracleUpdate, 0, len(pairs))
	for i, pair := range pairs {
		elts, err := scval.AsTupleN(pair, 2)
		if err != nil {
			return nil, fmt.Errorf("%w: symbol_rates[%d]: %w", ErrMalformedArgs, i, err)
		}
		sym, err := scval.AsSymbol(elts[0])
		if err != nil {
			return nil, fmt.Errorf("%w: symbol_rates[%d] symbol: %w", ErrMalformedArgs, i, err)
		}
		rate, err := scval.AsU64(elts[1])
		if err != nil {
			return nil, fmt.Errorf("%w: symbol_rates[%d] rate: %w", ErrMalformedArgs, i, err)
		}
		// USD is a special-case in Band's storage (always 1 at E9,
		// not relayer-set). We skip it in relay payloads — if a
		// relayer somehow pushes USD its storage write is rejected
		// on-chain and emitting an OracleUpdate would be false
		// signal. See band-soroban/src/storage/ref_data.rs:30-38.
		if sym == "USD" {
			continue
		}
		asset, err := symbolToAsset(sym)
		if err != nil {
			if errors.Is(err, ErrUnknownSymbol) {
				// Partial-event skip, same pattern as Reflector.
				// F-1234 (codex audit-2026-05-12): count the skip
				// so the decode-error runbook signals on upstream
				// coverage drift.
				obs.SourceUnknownSymbolsTotal.WithLabelValues("band").Inc()
				continue
			}
			return nil, fmt.Errorf("symbol_rates[%d] %q: %w", i, sym, err)
		}
		if rate == 0 {
			continue
		}
		u := canonical.OracleUpdate{
			Source:     SourceName,
			ContractID: contractID,
			Ledger:     ledger,
			TxHash:     txHash,
			OpIndex:    uint32(opIndex)*opIndexFanoutStride + uint32(i),
			Timestamp:  ts,
			Asset:      asset,
			Quote:      usdQuote,
			Price:      canonical.NewAmount(new(big.Int).SetUint64(rate)),
			Decimals:   DefaultDecimals,
			Observer:   relayerFrom,
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		return nil, ErrEmptyRates
	}
	return out, nil
}

// symbolToAsset maps a Band symbol to a canonical.Asset. Band
// publishes a mix of crypto tickers (BTC, ETH, XLM …) and fiat
// codes (USD is special-cased above; EUR, JPY, ...) via the same
// symbol_rates channel. We try crypto first (smaller allow-list,
// tighter match for oracle symbols); then fiat; then ErrUnknownSymbol
// for anything else — operator extends the allow-list as coverage
// grows.
func symbolToAsset(sym string) (canonical.Asset, error) {
	switch {
	case canonical.IsKnownCrypto(sym):
		return canonical.NewCryptoAsset(sym)
	case canonical.IsKnownFiat(sym):
		return canonical.NewFiatAsset(sym)
	}
	return canonical.Asset{}, fmt.Errorf("%w: %q", ErrUnknownSymbol, sym)
}

// pickObserver returns the best-effort attribution strkey for a
// force_relay call (which has no `from` arg). Op source wins if
// the op carries its own source account; otherwise the tx source.
// Empty string is acceptable — OracleUpdate.Observer is optional.
func pickObserver(opSource, txSource string) string {
	if opSource != "" {
		return opSource
	}
	return txSource
}
