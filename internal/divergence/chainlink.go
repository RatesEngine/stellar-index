package divergence

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"strings"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// ChainlinkReference is a [Reference] backed by Chainlink Data
// Feeds via off-chain Ethereum JSON-RPC reads.
//
// Design rationale (per docs/discovery/oracles/chainlink.md):
// Stellar joined Chainlink Scale in 2025/2026 but at audit time
// no Soroban Data Feeds contracts were live on mainnet. Chainlink's
// data is on-chain on Ethereum + L2s; we read it via eth_call
// against the AggregatorV3 contract's `latestAnswer()` view
// function on a public Ethereum RPC endpoint.
//
// Role: divergence cross-check ONLY. Chainlink does not contribute
// to VWAP/TWAP — we compare its reported price against our
// aggregated price for major pairs (BTC/USD, ETH/USD, EUR/USD,
// GBP/USD, etc.) and surface `flags.divergence_warning` on /v1/price
// when the spread exceeds threshold.
//
// Chainlink does NOT publish XLM/USD or USDC/USD on its mainnet
// feeds at audit time, so this reference covers fiat reference
// rates + major crypto pairs that we use as anchors via FX or
// stablecoin proxy. Adding more feed coverage is operator
// configuration only — the FeedMap maps canonical pair → AggregatorV3
// contract address.
type ChainlinkReference struct {
	httpClient *http.Client
	rpcURL     string

	// FeedMap routes canonical pair string ("native/fiat:USD",
	// "fiat:EUR/fiat:USD", etc.) to a Chainlink AggregatorV3
	// contract address (0x-prefixed hex). Operator-curated.
	feedMap map[string]chainlinkFeedSpec
}

// chainlinkFeedSpec captures everything needed to interpret one
// AggregatorV3 contract's output: the address, the price decimals
// (Chainlink standardises on 8 for crypto/USD pairs and 8 for FX
// pairs but always read the contract's `decimals()` to be safe;
// for static config we default to 8 unless overridden), and an
// optional inversion flag for cases where the operator wants
// 1/feed_price (e.g. EUR/USD feed used as USD/EUR signal).
type chainlinkFeedSpec struct {
	Address  string // 0x-prefixed
	Decimals int    // power-of-10 to divide raw answer by
	Invert   bool
}

// ChainlinkOptions configures [NewChainlinkReference].
type ChainlinkOptions struct {
	// HTTPClient — nil falls back to a 10s-timeout client.
	HTTPClient *http.Client

	// RPCURL is the JSON-RPC Ethereum endpoint used for eth_call.
	// Public free options at audit time:
	//
	//   https://cloudflare-eth.com
	//   https://eth.llamarpc.com
	//   https://rpc.ankr.com/eth
	//
	// Pinned to the public free tier intentionally — Chainlink is
	// a CROSS-CHECK reference, not a primary path; rate-limiting
	// or transient outages are operationally acceptable. The
	// divergence worker treats every per-tick failure as
	// "reference unavailable this run" rather than as a real
	// signal.
	//
	// Empty defaults to Cloudflare's public endpoint. Tests pass
	// httptest.Server URLs.
	RPCURL string

	// FeedMap maps canonical pair string → feed metadata. When empty
	// the constructor seeds a built-in default covering BTC/ETH/LINK
	// vs USD plus EUR/GBP/JPY vs USD (see defaultChainlinkFeedMap).
	// Operator-supplied entries merge OVER the defaults.
	//
	// Pair string format mirrors canonical.Pair.String():
	// "<base>/<quote>" e.g. "native/fiat:USD" (XLM/USD via
	// hypothetical Chainlink XLM feed if/when it ships) or
	// "fiat:EUR/fiat:USD" (EUR/USD reference rate).
	FeedMap map[string]ChainlinkFeed
}

// ChainlinkFeed is the operator-facing feed-config shape (no
// internal type-leakage).
type ChainlinkFeed struct {
	// Address is the 0x-prefixed Ethereum contract address of the
	// Chainlink AggregatorV3 feed.
	Address string

	// Decimals is the divisor power-of-10 applied to the raw
	// `latestAnswer()` int256. Chainlink crypto/USD feeds are 8;
	// some FX feeds are 8 too. When in doubt, query the feed's
	// `decimals()` view function once at config-load time.
	Decimals int

	// Invert is true when the canonical pair is the reciprocal of
	// the feed's natural quote (e.g. operator wants USD/EUR but
	// the feed publishes EUR/USD). When set, LookupPrice returns
	// 1 / raw_price instead of raw_price.
	Invert bool
}

// NewChainlinkReference constructs a Chainlink-backed reference.
//
// When opts.FeedMap is empty, the reference falls back to a built-in
// default covering the major crypto and fiat AggregatorV3 contracts
// on Ethereum mainnet (BTC/USD, ETH/USD, LINK/USD, EUR/USD, GBP/USD,
// JPY/USD). Without this fallback every divergence cross-check call
// for a default-config deployment returned ErrAssetUnsupported and
// `divergence_observations` stayed empty for any operator who hadn't
// manually populated `[divergence.chainlink].feed_map` — same shape
// as the CoinGecko default-IDMap gap fixed in #1249.
//
// Operator-supplied entries merge OVER the defaults (operator wins),
// so an operator can still narrow the set, override an address, or
// flip an Invert flag.
//
// Pinned to Ethereum mainnet AggregatorV3 contract addresses; these
// are immutable proxies in practice — Chainlink upgrades the
// underlying aggregator while keeping the proxy address stable. If
// a feed is ever migrated to a new proxy, operators override via
// the FeedMap.
func NewChainlinkReference(opts ChainlinkOptions) *ChainlinkReference {
	httpClient := opts.HTTPClient
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 10 * time.Second}
	}
	rpcURL := opts.RPCURL
	if rpcURL == "" {
		rpcURL = "https://cloudflare-eth.com"
	}
	rpcURL = strings.TrimRight(rpcURL, "/")
	feedMap := defaultChainlinkFeedMap()
	for k, v := range opts.FeedMap {
		spec := chainlinkFeedSpec(v)
		if spec.Decimals == 0 {
			spec.Decimals = 8 // Chainlink's overwhelming default
		}
		feedMap[k] = spec
	}
	return &ChainlinkReference{
		httpClient: httpClient,
		rpcURL:     rpcURL,
		feedMap:    feedMap,
	}
}

// defaultChainlinkFeedMap returns the built-in seed of pair →
// AggregatorV3 contract addresses. Covers what the aggregator's
// defaultPairs() computes by default (BTC, ETH, LINK against USD)
// plus the major fiat-anchor reference rates (EUR, GBP, JPY against
// USD) used by the FX-cross fallback path.
//
// All addresses are Chainlink mainnet (Ethereum) proxies — see
// https://docs.chain.link/data-feeds/price-feeds/addresses. Decimals
// is 8 on every entry (Chainlink's standard for crypto/USD and
// fiat/USD).
//
// XLM/USD, USDC/USD, USDT/USD are deliberately absent — Chainlink
// does not publish these on Ethereum mainnet at audit time
// (docs/discovery/oracles/chainlink.md). Operators wanting cross-
// checks on those pairs can configure them via the FeedMap once
// Chainlink ships them.
func defaultChainlinkFeedMap() map[string]chainlinkFeedSpec {
	const dec = 8
	return map[string]chainlinkFeedSpec{
		// Crypto / USD — covers our default top-of-book pairs.
		"crypto:BTC/fiat:USD":  {Address: "0xF4030086522a5bEEa4988F8cA5B36dbC97BeE88c", Decimals: dec},
		"crypto:ETH/fiat:USD":  {Address: "0x5f4eC3Df9cbd43714FE2740f5E3616155c5b8419", Decimals: dec},
		"crypto:LINK/fiat:USD": {Address: "0x2c1d072e956AFFC0D435Cb7AC38EF18d24d9127c", Decimals: dec},
		// Fiat / USD — anchors the FX-cross fallback used when the
		// aggregator triangulates X/fiat:Y via X/fiat:USD +
		// fiat:USD/fiat:Y.
		"fiat:EUR/fiat:USD": {Address: "0xb49f677943BC038e9857d61E7d053CaA2C1734C1", Decimals: dec},
		"fiat:GBP/fiat:USD": {Address: "0x5c0Ab2d9b5a7ed9f470386e82BB36A3613cDd4b5", Decimals: dec},
		"fiat:JPY/fiat:USD": {Address: "0xBcE206caE7f0ec07b545EddE332A47C2F75bbeb3", Decimals: dec},
	}
}

// Name implements [Reference].
func (*ChainlinkReference) Name() string { return "chainlink" }

// LookupPrice implements [Reference].
//
// Constructs an eth_call JSON-RPC request against the configured
// AggregatorV3 contract's `latestAnswer()` view function (selector
// 0x50d25bcd). Decodes the int256 result, applies the feed's
// decimals, and (optionally) inverts. Returns ErrAssetUnsupported
// when the pair has no feed mapping; transport / decode errors
// surface as wrapped errors so the divergence worker can treat
// them as "reference unavailable this run".
//
// observedAt is currently ignored — `latestAnswer()` returns the
// most-recent feed value with no historical lookup. Acceptable
// for divergence cross-check (the bucket is at most minutes old;
// Chainlink updates its feeds on heartbeat + deviation triggers
// so the gap is bounded). Future implementations could use
// `getRoundData(roundId)` + binary search if historical accuracy
// becomes load-bearing.
func (r *ChainlinkReference) LookupPrice(ctx context.Context, pair canonical.Pair, _ time.Time) (float64, error) {
	spec, ok := r.feedMap[pair.String()]
	if !ok {
		return 0, fmt.Errorf("%w: chainlink: no feed configured for %s", ErrAssetUnsupported, pair.String())
	}

	// `latestAnswer()` selector. AggregatorV3Interface.
	// keccak256("latestAnswer()")[:4] = 50d25bcd
	const latestAnswerSelector = "0x50d25bcd"

	body, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "eth_call",
		"params": []any{
			map[string]string{"to": spec.Address, "data": latestAnswerSelector},
			"latest",
		},
	})
	if err != nil {
		return 0, fmt.Errorf("chainlink: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.rpcURL, bytes.NewReader(body))
	if err != nil {
		return 0, fmt.Errorf("chainlink: new request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := r.httpClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("chainlink: rpc transport: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return 0, fmt.Errorf("chainlink: read body: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("chainlink: rpc status %d: %s", resp.StatusCode, string(respBody))
	}

	var rpcResp struct {
		Result string `json:"result"`
		Error  *struct {
			Code    int    `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(respBody, &rpcResp); err != nil {
		return 0, fmt.Errorf("chainlink: decode response: %w", err)
	}
	if rpcResp.Error != nil {
		return 0, fmt.Errorf("chainlink: rpc error %d: %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}
	if rpcResp.Result == "" || rpcResp.Result == "0x" {
		return 0, fmt.Errorf("chainlink: empty rpc result for %s", pair.String())
	}

	answer, err := decodeChainlinkInt256(rpcResp.Result)
	if err != nil {
		return 0, fmt.Errorf("chainlink: decode answer for %s: %w", pair.String(), err)
	}
	if answer.Sign() <= 0 {
		return 0, fmt.Errorf("chainlink: non-positive answer for %s: %s", pair.String(), answer.String())
	}

	priceFloat, err := scaleChainlinkAnswer(answer, spec.Decimals)
	if err != nil {
		return 0, fmt.Errorf("chainlink: scale answer for %s: %w", pair.String(), err)
	}
	if spec.Invert {
		if priceFloat == 0 {
			return 0, fmt.Errorf("chainlink: cannot invert zero answer for %s", pair.String())
		}
		priceFloat = 1.0 / priceFloat
	}
	return priceFloat, nil
}

// decodeChainlinkInt256 parses a 0x-prefixed hex string returned by
// eth_call into a *big.Int interpreted as a signed 256-bit value
// (two's complement). Handles negative answers (rare but possible
// for some feed types).
func decodeChainlinkInt256(hexStr string) (*big.Int, error) {
	hexStr = strings.TrimPrefix(hexStr, "0x")
	// Pad to 32 bytes if shorter (defensive — the RPC always
	// returns 32 bytes for an int256, but a malformed proxy could
	// trim leading zeros).
	if len(hexStr)%2 != 0 {
		hexStr = "0" + hexStr
	}
	if len(hexStr) > 64 {
		return nil, fmt.Errorf("hex too long (%d chars, want ≤64)", len(hexStr))
	}
	for len(hexStr) < 64 {
		hexStr = "0" + hexStr
	}
	raw, err := hex.DecodeString(hexStr)
	if err != nil {
		return nil, fmt.Errorf("hex decode: %w", err)
	}
	// int256 is two's complement: top bit set → negative.
	val := new(big.Int).SetBytes(raw)
	if raw[0]&0x80 != 0 {
		// Negative — subtract 2^256.
		twoTo256 := new(big.Int).Lsh(big.NewInt(1), 256)
		val = new(big.Int).Sub(val, twoTo256)
	}
	return val, nil
}

// scaleChainlinkAnswer divides answer by 10^decimals and returns
// the result as a float64. Loses precision above ~10^15 — fine
// for cross-check purposes; the divergence threshold is
// percentage-based.
func scaleChainlinkAnswer(answer *big.Int, decimals int) (float64, error) {
	if decimals < 0 || decimals > 38 {
		return 0, fmt.Errorf("decimals %d out of range [0, 38]", decimals)
	}
	if decimals == 0 {
		return float64(answer.Int64()), nil
	}
	div := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(decimals)), nil)
	q := new(big.Rat).SetFrac(answer, div)
	f, _ := q.Float64()
	return f, nil
}
