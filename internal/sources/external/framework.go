// Package external houses the connector framework for off-chain data
// sources — centralised exchanges (Binance, Kraken, Bitstamp, Coinbase,
// …), institutional FX feeds (Polygon.io, OANDA), third-party
// aggregators (CoinGecko, CoinMarketCap, CryptoCompare), and
// sovereign daily anchors (ECB, Fed H.10).
//
// Contrast with the on-chain dispatcher at internal/dispatcher/: that
// package consumes xdr.LedgerCloseMeta from Galexie and routes events
// to event- / op- / contract-call decoders. External sources speak
// HTTPS / WebSocket to vendor APIs on their own cadence, so they live
// outside the ledger loop. Both converge on the same canonical types
// (canonical.Trade, canonical.OracleUpdate) and the same Timescale
// hypertables — only the arrival path differs.
//
// Three orthogonal capabilities per source. A venue implements
// whichever subset it actually supports:
//
//   - [Streamer]     — live WebSocket trade feed (exchange class)
//   - [Poller]       — periodic REST fetch (aggregator / FX / sovereign)
//   - [Backfiller]   — historical OHLC candles via a venue's REST
//     endpoint; synthesised to canonical.Trade per bucket
//
// Sources that only stream live implement Streamer; sources that poll
// quote-boards implement Poller; a source can implement all three.
// Backfiller is always optional — Kraken caps historical at 720
// intervals (~30 days at 1h), Fed H.10 is daily-only, etc.
//
// Source class metadata lives in [Registry] — a Go-map source of truth
// that the aggregator queries at VWAP compute time to decide
// contribution (ClassExchange = yes, ClassAggregator / ClassOracle /
// ClassAuthoritySanity = no). No database table at this scale; swap
// to DB when the list outgrows a single Go file.
package external

import (
	"context"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
	"github.com/RatesEngine/rates-engine/internal/consumer"
)

// Class enumerates how a source participates in downstream aggregation.
//
// The distinction matters because mixing classes in a VWAP is either
// incorrect (averaging aggregates with raw trades double-counts
// upstream markets) or policy-sensitive (oracles publish already-
// aggregated values with their own governance — folding them into our
// VWAP would impose their methodology on our output).
//
// Rates Engine v1 policy: only [ClassExchange] contributes to VWAP.
// Everything else is reported alongside for transparency and
// divergence checking, but excluded from the computed price. Operators
// can override per-source via config.
type Class string

const (
	// ClassExchange — a venue that publishes real executed trades
	// (Binance, Kraken, Coinbase, SDEX, Soroswap). Contributes to
	// VWAP. Off-chain FX providers that source directly from
	// interbank feeds (Polygon.io Forex, OANDA) are also exchange
	// class — their "rate" is the executable bid/ask, not a
	// computed aggregate.
	ClassExchange Class = "exchange"

	// ClassAggregator — a third-party service that publishes
	// already-aggregated prices across many markets (CoinGecko,
	// CoinMarketCap, CryptoCompare). Useful as a divergence signal
	// against our own VWAP, but including in our VWAP would
	// double-count the upstream markets they derive from.
	ClassAggregator Class = "aggregator"

	// ClassOracle — on-chain, signed, governance-backed price
	// publishers (Reflector, Redstone, Band). Like aggregators
	// they publish derived prices, not raw trades; we report them
	// alongside but exclude from VWAP to avoid methodology
	// inheritance. Operator may opt one in per-source via config.
	ClassOracle Class = "oracle"

	// ClassAuthoritySanity — sovereign / central-bank daily
	// reference rates (ECB reference rates, Fed H.10, BOE rates).
	// Cadence too slow for aggregation but authoritative as a
	// sanity anchor — the daily-close divergence check that flags
	// if our live rate drifts more than N bps from the central
	// bank's public close.
	ClassAuthoritySanity Class = "authority_sanity"

	// ClassLending — on-chain lending protocols whose events are
	// directional / state-change signals rather than spot prices
	// (Blend). Auctions surface stress-prices during liquidation;
	// supply / borrow events surface position-side metrics; bad
	// debt flags protocol health. None of these contributes to
	// VWAP — lending events represent decisions taken on top of
	// other oracles' prices, not new price observations. Reported
	// alongside as a secondary validation surface per the proposal
	// (docs/discovery/dexes-amms/blend.md).
	ClassLending Class = "lending"
)

// Subclass is a finer-grained partition within a [Class]. Used by
// the confidence diversity factor (ADR-0019): a CEX (subclass "cex")
// and a DEX (subclass "dex") under the same `ClassExchange` parent
// are economically distinct sources for diversity-counting purposes.
//
// Empty string means "no further partitioning"; sources outside
// ClassExchange (oracles, aggregators, authority anchors) typically
// leave this blank — their parent Class already captures the
// economic distinction.
type Subclass string

const (
	SubclassCEX Subclass = "cex" // centralised exchange, off-chain
	SubclassDEX Subclass = "dex" // decentralised exchange, on-chain
	SubclassFX  Subclass = "fx"  // institutional FX feed
)

// Metadata is the source-registry record. Static at startup — not
// mutated during ingest. Operators may override DefaultWeight and
// IncludeInVWAP via config, but Class, Subclass and Paid are facts
// about the venue that don't change per deployment.
type Metadata struct {
	Class         Class
	Subclass      Subclass
	DefaultWeight int
	IncludeInVWAP bool
	// Paid indicates the source requires a commercial license or API
	// key to function. Exposed in /v1/sources so operators know
	// which connectors need credential setup.
	Paid bool
	// BackfillAvailable reports whether the source implements the
	// Backfiller interface with useful depth. Kraken would be false
	// here (30-day cap at 1h is too shallow to matter); Binance and
	// Bitstamp would be true.
	BackfillAvailable bool
	// BackfillSafe reports whether this source's decoder is safe to
	// run during a backfill against historical ledgers.
	//
	// For on-chain Soroban sources (soroswap, aquarius, phoenix, …)
	// "safe" means the decoder has been audited against every WASM
	// version that ran for the replay range — Soroban contracts can
	// `update_contract` in place without changing their address, so
	// event body schemas can vary across the same contract over time.
	// Live ingest only ever sees current WASM; backfill sees every
	// prior version. Decoding old events with a current-only decoder
	// produces silently wrong trades. See CLAUDE.md "Soroban DeFi
	// contracts upgrade in place" + docs/architecture/contract-
	// schema-evolution.md for the full picture.
	//
	// For off-chain sources (CEX/FX/aggregator/oracle-via-API)
	// BackfillSafe is always true: their backfill hits a vendor REST
	// endpoint whose schema we control via the connector code, not a
	// historical on-chain artifact. SDEX is also true (classic
	// Stellar, no WASM upgrades).
	//
	// This flag gates `ratesengine-ops backfill` from running a
	// source against historical ranges before its decoder has been
	// audited. Default-false for on-chain Soroban sources; flip to
	// true per-source as `wasm-history` audits land.
	BackfillSafe bool
}

// Connector is the common root interface. Every venue package
// exposes a concrete type that implements Connector plus at least one
// of [Streamer] / [Poller] / [Backfiller].
type Connector interface {
	// Name returns the canonical source identifier. Must match the
	// key in [Registry] and the value stamped on emitted
	// canonical.Trade.Source / canonical.OracleUpdate.Source.
	Name() string
	// Class returns the source's aggregation class. Short-hand for
	// Registry[Name()].Class — concrete types return it directly so
	// callers don't need to import the registry.
	Class() Class
}

// Streamer is implemented by venues that push live trades via
// WebSocket or similar persistent connection. Connectors handle
// reconnect, heartbeats, and rate limiting internally — callers just
// read from the returned channel.
type Streamer interface {
	Connector
	// Start opens the connection, subscribes to the requested pairs,
	// and returns a channel that emits canonical.Trade values. The
	// channel is closed when ctx is cancelled or the source hits an
	// unrecoverable error (invalid credential, persistent venue
	// outage past the backoff ceiling).
	//
	// Transient problems (single dropped frame, one reconnect cycle)
	// are handled internally and surfaced via metrics; they do not
	// close the channel. Only a fatal "this source is dead" state
	// returns an error from Start or closes the channel without
	// cancellation.
	//
	// Caller supplies the pair list. An empty list means "all pairs
	// this source supports" — venues that enumerate symbols on
	// connect (Binance, Kraken) can honour that; others return an
	// error.
	Start(ctx context.Context, pairs []canonical.Pair) (<-chan canonical.Trade, error)
}

// Poller is implemented by venues with REST endpoints that serve the
// current quote board. Called on a fixed cadence by the framework's
// runner; the connector itself is stateless between calls.
type Poller interface {
	Connector
	// PollOnce hits the venue once and returns whatever it serves.
	// Exchange-class pollers (rare — most exchanges stream) return
	// Trades. Aggregator / oracle / sovereign pollers return
	// OracleUpdates (derived prices with no executed-trade context).
	// A connector returns only one of the two; the unused slice is
	// nil.
	PollOnce(ctx context.Context, pairs []canonical.Pair) (trades []canonical.Trade, updates []canonical.OracleUpdate, err error)
	// PollInterval is the minimum gap between PollOnce calls.
	// Framework enforces — connector just declares its cadence.
	PollInterval() time.Duration
}

// Backfiller is implemented by venues whose REST APIs expose
// historical OHLC candles. Output is synthesised canonical.Trade —
// one Trade per candle at the candle's VWAP (or close price, when
// VWAP unavailable) carrying the candle's volume. Open/high/low fields
// are dropped; consumers that need full candle fidelity read from the
// continuous-aggregate tables instead.
//
// Each venue has its own historical depth limit; callers should
// consult Metadata.BackfillAvailable and the venue's doc rather than
// assuming "all the way to listing." Kraken, for example, caps at
// 720 intervals regardless of granularity.
type Backfiller interface {
	Connector
	Backfill(ctx context.Context, pair canonical.Pair, from, to time.Time, granularity time.Duration) ([]canonical.Trade, error)
}

// TradeEvent is the consumer.Event wrapper for trades arriving from
// external connectors. Mirrors soroswap.TradeEvent / aquarius.TradeEvent
// / comet.TradeEvent so the indexer's sink type-switch stays uniform.
//
// Unlike per-venue wrappers in the on-chain source packages, there's
// only one wrapper here — external trades all land in the same
// trades hypertable via the same InsertTrade path, and the Source
// field on canonical.Trade already identifies the venue.
type TradeEvent struct {
	Trade canonical.Trade
}

// EventKind implements [consumer.Event].
func (TradeEvent) EventKind() string { return "external.trade" }

// Source implements [consumer.Event]. Delegates to the embedded
// Trade's Source so metrics label by venue, not by event kind.
func (e TradeEvent) Source() string { return e.Trade.Source }

// UpdateEvent wraps OracleUpdate-shaped output from aggregator /
// oracle / sovereign pollers. Parallel to reflector.UpdateEvent /
// redstone.UpdateEvent / band.UpdateEvent.
type UpdateEvent struct {
	Update canonical.OracleUpdate
}

// EventKind implements [consumer.Event].
func (UpdateEvent) EventKind() string { return "external.update" }

// Source implements [consumer.Event].
func (e UpdateEvent) Source() string { return e.Update.Source }

// Compile-time checks.
var (
	_ consumer.Event = TradeEvent{}
	_ consumer.Event = UpdateEvent{}
)
