package redstone

import (
	"fmt"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/events"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// opIndexFanoutStride spaces synthetic op_index values derived from
// a single batch update, same concept as the Reflector decoder.
// Redstone emits at most 19 entries today; 1024 holds the full feed
// set with headroom for growth and stays inside uint32.
const opIndexFanoutStride = 1024

// classify reports whether this is a Redstone "REDSTONE" event.
// topic[0] is byte-compared against the pre-encoded constant.
func classify(e *events.Event) bool {
	if len(e.Topic) < 1 {
		return false
	}
	return e.Topic[0] == TopicSymbolRedstone
}

// decodeWritePrices converts one REDSTONE event into a slice of
// canonical.OracleUpdate — one per (feed_id, price) pair.
//
// Topic arity: 1 (REDSTONE only). Body: Map{updater, updated_feeds:
// Vec<PriceData>}. feed_ids are NOT in the body; they come from the
// tx envelope's write_prices(feed_ids, …) call and are passed in via
// events.Event.OpArgs, populated by internal/dispatcher.
//
// Each OracleUpdate shares (ledger, tx_hash, source) but gets a
// distinct OpIndex derived from the vector position so identity
// stays unique in oracle_updates.
func decodeWritePrices(e *events.Event, closedAt time.Time) ([]canonical.OracleUpdate, error) {
	if !classify(e) {
		return nil, ErrNotRedstoneEvent
	}
	if len(e.OpArgs) == 0 {
		return nil, ErrMissingOpArgs
	}

	feedIDs, updater, err := feedIDsFromOpArgs(e.OpArgs)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}

	prices, err := sdkDecodeBody(e.Value)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrMalformedPayload, err)
	}
	if len(prices) == 0 {
		return nil, ErrEmptyUpdates
	}

	// Strict length check — see ErrFeedIDCountMismatch rationale in
	// events.go. If the adapter's freshness verifier dropped any
	// feed, we'd attribute the remaining prices to the wrong assets,
	// so we refuse the whole event instead.
	if len(feedIDs) != len(prices) {
		return nil, fmt.Errorf("%w: %d feed_ids, %d updated_feeds",
			ErrFeedIDCountMismatch, len(feedIDs), len(prices))
	}
	if len(prices) > opIndexFanoutStride {
		return nil, fmt.Errorf("redstone: feed count %d exceeds fanout stride %d",
			len(prices), opIndexFanoutStride)
	}

	observer := updater // relayer address from op args — strkey form already

	out := make([]canonical.OracleUpdate, 0, len(prices))
	for i, pd := range prices {
		entry, ok := lookupFeed(feedIDs[i])
		if !ok {
			// Partial-event skip: land every feed in the ADR-0028
			// registry, drop the rest. A miss means RedStone deployed
			// a feed beyond the 19-feed registry — count it so
			// operators get a signal (F-1234, codex audit-2026-05-12)
			// rather than blocking the whole batch.
			obs.SourceUnknownSymbolsTotal.WithLabelValues("redstone").Inc()
			continue
		}
		if pd.Price.Sign() <= 0 {
			// Redstone publishes non-zero prices by construction —
			// defensive skip in case a contract upgrade relaxes that.
			continue
		}
		u := canonical.OracleUpdate{
			Source:     SourceName,
			ContractID: e.ContractID,
			Ledger:     e.Ledger,
			TxHash:     e.TxHash,
			// OpIndex uses a fixed stride per-event so two Redstone
			// events in one tx (unlikely but possible) can't collide
			// on the oracle_updates PK.
			OpIndex:   uint32(e.OperationIndex)*opIndexFanoutStride + uint32(i),
			Timestamp: pickTimestamp(pd.PackageTimestamp, closedAt),
			Asset:     entry.Base,
			// Per-feed quote — USD for all but EUROC/EUR (ADR-0028).
			// Pre-#53 this was hardcoded USD, mislabelling EUROC.
			Quote:    entry.Quote,
			Price:    pd.Price,
			Decimals: DefaultDecimals,
			Observer: observer,
		}
		out = append(out, u)
	}
	if len(out) == 0 {
		// Every entry was unknown — treat like Reflector's all-skipped
		// case. Surfaces to the dispatcher as a decode error counter
		// bump so an all-RWA batch is visible in metrics.
		return nil, ErrEmptyUpdates
	}
	return out, nil
}

// ─── SCVal decoders ─────────────────────────────────────────────
// sdkDecodeBody / sdkDecodeFeedIDsFromArg / sdkDecodeAddress are
// called directly below. (A package-var indirection seam used to sit
// here for test fixture-swapping; removed as unused — no test ever
// swapped them.)

// priceDataDecoded mirrors the adapter's PriceData struct
// (common/src/lib.rs:12-18) at the canonical-types boundary. The
// timestamps are `u64` ms on the wire — package_timestamp is when
// RedStone signed the payload off-chain, write_timestamp is when it
// landed on-chain. We stamp package_timestamp on OracleUpdate
// (the oracle's published time, not the block close time) — matches
// the OracleUpdate contract in canonical/oracle.go.
type priceDataDecoded struct {
	Price            canonical.Amount
	PackageTimestamp uint64
	WriteTimestamp   uint64
}

// sdkDecodeBody decodes the WritePrices event body:
//
//	Map { "updater": Address, "updated_feeds": Vec<PriceData> }
//
// On the wire the Rust adapter contract (redstone-adapter's
// event.rs) emits the body as `ScVal::Bytes` wrapping the XDR-
// encoded struct: `self.to_xdr(env).to_val()`. We unwrap that Bytes
// layer once, then re-parse as ScVal::Map below. If we ever see a
// body that's already a Map (e.g. a future contract upgrade that
// drops the custom to_xdr), the fallback path still works.
//
// We only return the updated_feeds list — the updater is pulled
// from the op args instead (the event's updater and args' updater
// must agree by contract, and args are authoritative for observer
// attribution since they include the full strkey regardless of
// muxed variants).
func sdkDecodeBody(valueB64 string) ([]priceDataDecoded, error) {
	body, err := scval.Parse(valueB64)
	if err != nil {
		return nil, fmt.Errorf("parse body: %w", err)
	}
	// Unwrap the Bytes-wrapped XDR payload if present. The adapter
	// uses `to_xdr().to_val()` which produces ScVal::Bytes holding
	// the XDR-encoded Map; re-parsing those bytes yields the Map
	// shape our existing downstream logic already handles.
	if raw, bytesErr := scval.AsBytes(body); bytesErr == nil {
		inner, parseErr := scval.ParseBytes(raw)
		if parseErr != nil {
			return nil, fmt.Errorf("unwrap Bytes body: %w", parseErr)
		}
		body = inner
	}
	entries, err := scval.AsMap(body)
	if err != nil {
		return nil, fmt.Errorf("body not a Map: %w", err)
	}
	updsSv, err := scval.MustMapField(entries, "updated_feeds")
	if err != nil {
		return nil, fmt.Errorf("body map missing updated_feeds: %w", err)
	}
	items, err := scval.AsVec(updsSv)
	if err != nil {
		return nil, fmt.Errorf("updated_feeds not a Vec: %w", err)
	}
	out := make([]priceDataDecoded, 0, len(items))
	for i, item := range items {
		pd, err := decodePriceData(item)
		if err != nil {
			return nil, fmt.Errorf("updated_feeds[%d]: %w", i, err)
		}
		out = append(out, pd)
	}
	return out, nil
}

// decodePriceData decodes one PriceData map entry:
//
//	Map { "price": U256, "package_timestamp": u64, "write_timestamp": u64 }
func decodePriceData(sv scval.ScVal) (priceDataDecoded, error) {
	entries, err := scval.AsMap(sv)
	if err != nil {
		return priceDataDecoded{}, fmt.Errorf("PriceData not a Map: %w", err)
	}
	priceSv, err := scval.MustMapField(entries, "price")
	if err != nil {
		return priceDataDecoded{}, fmt.Errorf("missing price: %w", err)
	}
	price, err := scval.AsAmountFromU256(priceSv)
	if err != nil {
		return priceDataDecoded{}, fmt.Errorf("price: %w", err)
	}
	pkgSv, err := scval.MustMapField(entries, "package_timestamp")
	if err != nil {
		return priceDataDecoded{}, fmt.Errorf("missing package_timestamp: %w", err)
	}
	pkg, err := scval.AsU64(pkgSv)
	if err != nil {
		return priceDataDecoded{}, fmt.Errorf("package_timestamp: %w", err)
	}
	wrSv, err := scval.MustMapField(entries, "write_timestamp")
	if err != nil {
		return priceDataDecoded{}, fmt.Errorf("missing write_timestamp: %w", err)
	}
	wr, err := scval.AsU64(wrSv)
	if err != nil {
		return priceDataDecoded{}, fmt.Errorf("write_timestamp: %w", err)
	}
	return priceDataDecoded{
		Price:            price,
		PackageTimestamp: pkg,
		WriteTimestamp:   wr,
	}, nil
}

// feedIDsFromOpArgs parses the dispatcher-supplied InvokeContract
// args and returns the feed_ids + updater strkey. Argument layout
// per adapter/lib.rs:78:
//
//	write_prices(updater: Address, feed_ids: Vec<String>, payload: Bytes)
//
// We enforce arity ≥ 3 (extra args from a contract upgrade would
// surface here). We do NOT verify the function name was write_prices
// — the dispatcher only plumbs the Args slice, not the function name
// (see the WriteFnName note and the body comment below) — so this
// leans on the dispatcher's contract-ID scoping instead.
func feedIDsFromOpArgs(opArgs []string) (feedIDs []string, updater string, err error) {
	// The InvokeContract wire layout stores the function name OUTSIDE
	// the Args slice (it lives alongside them in InvokeContractArgs).
	// Our dispatcher currently only plumbs through Args — the
	// function-name check is a separate concern. For now we trust the
	// dispatcher's contract-ID scoping: a decoder only runs when the
	// event's contract_id matches the configured Adapter, and the
	// Adapter only emits REDSTONE from write_prices. If that contract
	// emits a new REDSTONE-topic event in a future WASM, this
	// position-based decode would need to re-verify — covered by
	// docs/architecture/contract-schema-evolution.md's per-WASM-hash
	// audit gate.
	if len(opArgs) < 3 {
		return nil, "", fmt.Errorf("op args arity %d, want ≥3 (updater, feed_ids, payload)", len(opArgs))
	}
	addrSv, err := scval.Parse(opArgs[0])
	if err != nil {
		return nil, "", fmt.Errorf("args[0] updater: %w", err)
	}
	updater, err = sdkDecodeAddress(addrSv)
	if err != nil {
		return nil, "", fmt.Errorf("args[0] updater: %w", err)
	}
	feedsSv, err := scval.Parse(opArgs[1])
	if err != nil {
		return nil, "", fmt.Errorf("args[1] feed_ids: %w", err)
	}
	feedIDs, err = sdkDecodeFeedIDsFromArg(feedsSv)
	if err != nil {
		return nil, "", fmt.Errorf("args[1] feed_ids: %w", err)
	}
	// args[2] is the signed payload bytes — not needed for attribution.
	return feedIDs, updater, nil
}

// sdkDecodeFeedIDsFromArg decodes a Vec<String> where each element
// is an ScString holding a feed_id like "BTC".
func sdkDecodeFeedIDsFromArg(sv scval.ScVal) ([]string, error) {
	items, err := scval.AsVec(sv)
	if err != nil {
		return nil, fmt.Errorf("not a Vec: %w", err)
	}
	out := make([]string, 0, len(items))
	for i, it := range items {
		s, err := scval.AsString(it)
		if err != nil {
			return nil, fmt.Errorf("feed_ids[%d]: %w", i, err)
		}
		out = append(out, s)
	}
	return out, nil
}

// sdkDecodeAddress decodes an Address SCVal to its G-strkey form.
// Delegates to scval.AsAddressStrkey which owns the strkey
// formatting for all address types.
func sdkDecodeAddress(sv scval.ScVal) (string, error) {
	return scval.AsAddressStrkey(sv)
}

// sanityFutureWindow bounds how far past the ledger close a decoded
// oracle timestamp may sit before we treat it as garbage and fall
// back to the ledger close time. Absorbs clock skew without admitting
// sentinel / overflow values that error the timestamptz INSERT
// (cf. the soroswap-router deadline_ts fix).
const sanityFutureWindow = 24 * time.Hour

// pickTimestamp prefers the contract-supplied PackageTimestamp
// (ms UNIX) but falls back to the ledger close time when the
// contract reports 0 — a defensive case against a contract
// upgrade that relaxes the "non-zero timestamp" invariant — or when
// the value is a sentinel / garbage far-future timestamp (same class
// as the router deadline_ts overflow) that would error the
// timestamptz INSERT.
func pickTimestamp(packageMs uint64, closedAt time.Time) time.Time {
	if packageMs == 0 {
		return closedAt.UTC()
	}
	ts := time.UnixMilli(int64(packageMs)).UTC()
	if ts.After(closedAt.Add(sanityFutureWindow)) {
		return closedAt.UTC()
	}
	return ts
}
