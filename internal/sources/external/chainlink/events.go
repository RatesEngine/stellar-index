// Package chainlink ingests price observations from Chainlink Data
// Feeds on Ethereum mainnet via JSON-RPC reads against the
// AggregatorV3 contract interface.
//
// Sister to (but distinct from) internal/divergence/chainlink.go —
// that package is a synchronous cross-check helper for the divergence
// service; THIS package is a real ingest source that runs its own
// poller goroutine, dedupes by roundId, and writes
// canonical.OracleUpdate rows to the oracle_updates hypertable
// alongside Reflector / Redstone / Band.
//
// See README.md for design rationale + Phase A vs Phase B scope.
package chainlink

import (
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"golang.org/x/crypto/sha3"
)

// init computes [AnswerUpdatedTopic0] once. We do this in init()
// rather than as a `var`-block expression so the dependency chain
// (sha3 import) stays explicit and the failure mode (a typo in
// AnswerUpdatedSignature) panics at boot, not silently mis-filters
// every log forever.
func init() {
	h := sha3.NewLegacyKeccak256()
	_, _ = h.Write([]byte(AnswerUpdatedSignature))
	AnswerUpdatedTopic0 = "0x" + hex.EncodeToString(h.Sum(nil))
}

// SourceName is the canonical source identifier stamped on every
// emitted canonical.OracleUpdate.Source. Stable — appears in
// metrics labels, /v1/sources, oracle_updates rows.
//
// We deliberately use "chainlink" (not "chainlink-http" — the legacy
// commented-out value in canonical/oracle.go) because the new
// reality is "Chainlink, full stop": once Chainlink ships Soroban
// Data Feeds we'll add a separate "chainlink-stellar" or similar
// source, and the EVM-via-HTTP path keeps this name without
// confusion.
const SourceName = "chainlink"

// DefaultPollInterval — 30s aligns with the proposal's freshness
// ceiling for current-price endpoints (docs/ctx-proposal.md:329).
// Most Chainlink feeds update every few minutes (heartbeat) or on
// 0.5% deviation; polling faster than 30s wastes RPC budget without
// catching anything.
const DefaultPollInterval = 30 * time.Second

// DefaultDecimals — Chainlink's overwhelming standard for
// crypto/USD and fiat/USD feeds. Operators can override per-feed
// via FeedSpec.Decimals; we default here so the operator-facing
// FeedMap stays terse.
const DefaultDecimals uint8 = 8

// DefaultEndpoint is the Cloudflare public Ethereum JSON-RPC
// endpoint. Free, no key, rate-limited per-IP.
//
// Suitable as a fallback for divergence-style cross-checks but
// NOT for production live ingest at scale — you'll get throttled
// long before 516 feeds × 30s polls land. Operators pointing at
// production should override with their own Alchemy / Infura /
// QuickNode endpoint.
const DefaultEndpoint = "https://cloudflare-eth.com"

// Function selectors — keccak256(signature)[:4], precomputed.
//
// AggregatorV3Interface — see
// https://docs.chain.link/data-feeds/api-reference#aggregatorv3interface.
const (
	// SelLatestRoundData = keccak256("latestRoundData()")[:4]
	SelLatestRoundData = "0xfeaf968c"

	// SelLatestAnswer = keccak256("latestAnswer()")[:4]
	// Older AggregatorV2 selector — kept for legacy feed compat.
	SelLatestAnswer = "0x50d25bcd"

	// SelDecimals = keccak256("decimals()")[:4]
	SelDecimals = "0x313ce567"
)

// AnswerUpdatedSignature is the Solidity event signature used for
// keccak256 → topic[0] computation. Computed once at package init
// (see init() below) rather than hardcoded so a typo in the hex
// constant can't silently miss every log forever.
//
//	event AnswerUpdated(int256 indexed current, uint256 indexed roundId, uint256 updatedAt)
//
// `current` and `roundId` are indexed → topics[1] and topics[2];
// `updatedAt` is in the data field. (Documented on Chainlink's
// AggregatorV3Interface reference page.)
const AnswerUpdatedSignature = "AnswerUpdated(int256,uint256,uint256)"

// AnswerUpdatedTopic0 is the keccak256 hash of [AnswerUpdatedSignature],
// 0x-prefixed. Computed at package init from the signature constant.
var AnswerUpdatedTopic0 string

// FeedSpec describes one Chainlink AggregatorV3 contract: where it
// lives, what canonical pair we project its answer into, and how
// to scale the raw int256.
//
// The pair is implicit in the FeedMap key; FeedSpec only carries
// the per-address fields.
type FeedSpec struct {
	// Address is the 0x-prefixed Ethereum contract address of the
	// AggregatorV3 proxy. Chainlink upgrades the underlying
	// aggregator via this proxy; the address is stable across
	// upgrades.
	Address string

	// Decimals is the divisor power-of-10 applied to the raw int256
	// answer to obtain the human-scale price. Defaults to 8
	// (DefaultDecimals) when zero.
	Decimals uint8

	// Invert flips price → 1/price after scaling. Useful when the
	// canonical pair is the reciprocal of the feed's natural quote
	// (e.g. operator wants USD/EUR but the feed publishes EUR/USD).
	// Same semantic as divergence/chainlink.go's Invert.
	Invert bool
}

// Round is the decoded shape of one latestRoundData() / getRoundData()
// return tuple. Used as the dedup key (RoundID) and the source of
// truth for the OracleUpdate's timestamp + price.
type Round struct {
	// FeedAddress is the 0x-prefixed contract address this round
	// came from. Used in dedup keys + log line context.
	FeedAddress string

	// RoundID is the chainlink-internal round identifier. Dedup is
	// by (FeedAddress, RoundID) — repeated polls of an unchanged
	// feed must not produce duplicate OracleUpdate rows.
	RoundID uint64

	// Answer is the raw int256 price at the feed's native decimals.
	// Preserved as-is (no scaling) — canonical.OracleUpdate.Price
	// stores raw integers per ADR-0003.
	Answer string // decimal-string form for cross-package portability

	// UpdatedAt is the timestamp Chainlink stamped into the round's
	// `updatedAt` field — the authoritative oracle-publication
	// timestamp. Always UTC.
	UpdatedAt time.Time
}

// Errors used by the package. Callers classify via errors.Is.
var (
	// ErrEmptyResult — the RPC returned `0x` or empty string.
	// Usually means the contract address is wrong, or the call
	// reverted server-side. The poller logs + skips; the next tick
	// retries.
	ErrEmptyResult = errors.New("chainlink: empty rpc result")

	// ErrMalformedResult — RPC returned data we can't decode as
	// the expected ABI shape (wrong length, malformed hex, etc).
	ErrMalformedResult = errors.New("chainlink: malformed rpc result")

	// ErrUnknownFeed — operator asked us to poll a pair that
	// isn't in our FeedMap. Surfaced at config-load + per-poll
	// guard.
	ErrUnknownFeed = errors.New("chainlink: pair not in configured FeedMap")

	// ErrNonPositivePrice — Chainlink can in principle return
	// negative answers (some legacy feeds did during stress); we
	// drop those rather than write a row that would fail
	// `oracle_updates`'s `CHECK (price > 0)`.
	ErrNonPositivePrice = errors.New("chainlink: non-positive answer")
)

// roundCache is the per-feed dedup memory: last roundId we emitted
// per feed address. Indexed by feed address (lowercased) for
// canonical compares.
//
// Survives indexer restart by virtue of the oracle_updates PK
// (source, ledger=0, tx_hash, op_index, ts) — re-emitting the same
// (roundId, updatedAt) pair after restart would attempt an INSERT
// that the storage layer's ON CONFLICT clause rejects, so even
// without the in-memory cache we'd be safe. The cache just saves
// the round-trip.
type roundCache struct {
	mu   sync.Mutex
	last map[string]uint64 // feed address (lowercase) → last roundId emitted
}

func newRoundCache() *roundCache {
	return &roundCache{last: make(map[string]uint64)}
}

// shouldEmit reports whether this (feed, roundId) is new — i.e.
// strictly greater than the last seen. Returns true and updates
// the cache; returns false if we've already emitted this round or
// a newer one.
func (c *roundCache) shouldEmit(feedAddr string, roundID uint64) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	prev, ok := c.last[feedAddr]
	if ok && roundID <= prev {
		return false
	}
	c.last[feedAddr] = roundID
	return true
}
