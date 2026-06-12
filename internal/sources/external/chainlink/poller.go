package chainlink

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"math/big"
	"strings"
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/sources/external"
)

// Poller implements external.Poller for Chainlink Data Feeds.
//
// One poll tick: for every pair in the supplied pair list, look up
// the feed spec in FeedMap, call latestRoundData() over JSON-RPC,
// decode the 5-tuple, dedupe by (feed_address, roundId), and emit
// one canonical.OracleUpdate per new round.
//
// Stateless w.r.t. its own writes (the dedup cache rebuilds at boot
// — first poll after restart re-emits the latest round per feed,
// which the storage layer's PK ON CONFLICT clause idempotently
// rejects). Goroutine-safe; the framework's runner serialises
// PollOnce calls per source so internal mutexes only guard against
// future concurrent-PollOnce paths.
type Poller struct {
	// Client is the EVM JSON-RPC client. Constructor injects the
	// operator-supplied endpoint (Alchemy / Infura / public).
	Client *Client

	// Logger receives per-feed warn/info messages. nil → slog.Default.
	Logger *slog.Logger

	// Interval overrides DefaultPollInterval. Zero = default.
	Interval time.Duration

	// FeedMap maps canonical pair string ("crypto:BTC/fiat:USD")
	// to the AggregatorV3 contract address + decimals + invert.
	// Operator-curated; constructor injects from config.
	FeedMap map[string]FeedSpec

	// Cache holds the last roundId emitted per feed address. Empty
	// at construction; populated on each successful PollOnce.
	Cache *roundCache

	// concurrency caps how many feed eth_call requests we have in
	// flight at once during one PollOnce tick. Defaults to 8 — well
	// inside Alchemy's free-tier per-second budget for our pair
	// counts and a polite ceiling for public RPCs.
	Concurrency int
}

// NewPoller builds a Poller with sensible defaults. Caller supplies
// the RPC endpoint (must include the API key for keyed providers
// like Alchemy) and the feed map.
func NewPoller(rpcURL string, feedMap map[string]FeedSpec) *Poller {
	return &Poller{
		Client:      NewClient(rpcURL, nil),
		Interval:    DefaultPollInterval,
		FeedMap:     feedMap,
		Cache:       newRoundCache(),
		Concurrency: 8,
	}
}

// Name implements external.Connector.
func (p *Poller) Name() string { return SourceName }

// Class implements external.Connector.
func (p *Poller) Class() external.Class { return external.ClassOracle }

// PollInterval implements external.Poller.
func (p *Poller) PollInterval() time.Duration {
	if p.Interval <= 0 {
		return DefaultPollInterval
	}
	return p.Interval
}

// PollOnce implements external.Poller. For each pair in `pairs`,
// look up the feed in FeedMap, call latestRoundData(), and emit
// one OracleUpdate if the round is new. Returns updates, never
// trades (Chainlink is oracle-class).
//
// Per-pair errors are logged + counted via the framework's
// outcome="error" metric, not bubbled — one bad feed shouldn't
// stop the other 515.
//
// Bounded concurrency: at most `p.Concurrency` simultaneous
// eth_call requests. Polite default for shared RPC endpoints.
func (p *Poller) PollOnce(ctx context.Context, pairs []canonical.Pair) ([]canonical.Trade, []canonical.OracleUpdate, error) { //nolint:gocognit,gocyclo // dispatch-heavy; splitting hurts readability
	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}

	type result struct {
		update canonical.OracleUpdate
		err    error
		pair   canonical.Pair
		round  Round
	}

	conc := p.Concurrency
	if conc <= 0 {
		conc = 8
	}
	sem := make(chan struct{}, conc)
	results := make(chan result, len(pairs))

	var wg sync.WaitGroup
	for _, pr := range pairs {
		spec, ok := p.FeedMap[pr.String()]
		if !ok {
			// Pair isn't in our feed map — operator misconfig or
			// the framework passed us a non-Chainlink-ish pair.
			// Skip silently; logging every miss every 30s would
			// drown the journal.
			continue
		}
		wg.Add(1)
		sem <- struct{}{}
		go func(pair canonical.Pair, spec FeedSpec) {
			defer wg.Done()
			defer func() { <-sem }()
			rnd, err := p.fetchLatest(ctx, pair, spec)
			if err != nil {
				results <- result{err: err, pair: pair}
				return
			}
			if !p.Cache.shouldEmit(rnd.FeedAddress, rnd.RoundID) {
				// Already emitted this round (or a newer one) — no-op.
				return
			}
			u, err := p.project(pair, spec, rnd)
			if err != nil {
				results <- result{err: err, pair: pair, round: rnd}
				return
			}
			results <- result{update: u, pair: pair, round: rnd}
		}(pr, spec)
	}
	go func() { wg.Wait(); close(results) }()

	var updates []canonical.OracleUpdate
	var firstErr error
	for r := range results {
		if r.err != nil {
			if firstErr == nil {
				firstErr = r.err
			}
			logger.Warn("chainlink feed poll failed",
				"source", SourceName,
				"pair", r.pair.String(),
				"err", r.err)
			continue
		}
		// Empty update means "no new round" (not an error).
		if r.update.Source == "" {
			continue
		}
		updates = append(updates, r.update)
	}

	// Convention from coingecko: nil/nil/nil = "skipped" (poll fired
	// but found nothing). Returning err here would mark the WHOLE
	// tick as "error" even when most feeds succeeded — that's bad
	// signal hygiene. Per-feed errors are already logged above.
	//
	// G10-02 (liveness): but an all-feeds-FAILED cycle is NOT a
	// healthy skip. Pre-fix this branch returned (nil,nil,nil) even
	// when every feed errored, so the runner bumped
	// ExternalPollerLastSuccessUnix and the staleness gauge stayed
	// green while the poller was actually wedged (e.g. bad endpoint,
	// key revoked, RPC down). We now surface firstErr when there were
	// zero successful updates AND at least one feed errored — a
	// genuine "polled and found nothing new" cycle (firstErr == nil)
	// still skips cleanly.
	if len(updates) == 0 {
		if firstErr != nil {
			return nil, nil, firstErr
		}
		return nil, nil, nil
	}
	return nil, updates, nil
}

// fetchLatest calls latestRoundData on the given feed and decodes
// the 5-tuple. Wraps the network + decode errors with the pair
// context the caller logs.
func (p *Poller) fetchLatest(ctx context.Context, pair canonical.Pair, spec FeedSpec) (Round, error) {
	rawHex, err := p.Client.EthCall(ctx, spec.Address, SelLatestRoundData, "latest")
	if err != nil {
		return Round{}, fmt.Errorf("eth_call %s: %w", pair.String(), err)
	}
	rnd, err := decodeLatestRoundData(rawHex, spec.Address)
	if err != nil {
		return Round{}, fmt.Errorf("decode %s: %w", pair.String(), err)
	}
	return rnd, nil
}

// project converts a decoded Round into a canonical.OracleUpdate
// suitable for InsertOracleUpdate. Applies the per-feed Decimals +
// Invert transforms before populating the row.
//
// Identity for off-chain sources is the synthesized tx_hash + ts
// pair: Ledger=0, OpIndex=0, TxHash=sha256(feed||roundId)[:64],
// Timestamp=Round.UpdatedAt. The PK (source, ledger, tx_hash,
// op_index, ts) gives idempotent inserts under restart and
// retry — re-emitting the same round produces the same TxHash.
func (p *Poller) project(pair canonical.Pair, spec FeedSpec, rnd Round) (canonical.OracleUpdate, error) {
	decimals := spec.Decimals
	if decimals == 0 {
		decimals = DefaultDecimals
	}

	// Apply Invert: 1/answer at the same decimal scale. Inversion
	// happens on the BIG INT — `(10^(2*decimals)) / answer` keeps
	// the result at `decimals` scale. We don't downcast to float
	// here because oracle_updates stores raw integers per ADR-0003.
	rawAnswer, ok := new(big.Int).SetString(rnd.Answer, 10)
	if !ok {
		return canonical.OracleUpdate{}, fmt.Errorf("%w: bad decimal answer %q", ErrMalformedResult, rnd.Answer)
	}
	answer := rawAnswer
	if spec.Invert {
		if rawAnswer.Sign() == 0 {
			return canonical.OracleUpdate{}, fmt.Errorf("%w: cannot invert zero", ErrMalformedResult)
		}
		// numerator = 10^(2 * decimals); inverted = numerator / rawAnswer
		// keeps the result at `decimals` scale.
		exp := new(big.Int).Exp(big.NewInt(10), big.NewInt(int64(2*decimals)), nil)
		answer = new(big.Int).Quo(exp, rawAnswer)
	}
	if answer.Sign() <= 0 {
		return canonical.OracleUpdate{}, fmt.Errorf("%w: post-invert non-positive %s", ErrNonPositivePrice, answer.String())
	}

	return canonical.OracleUpdate{
		Source:     SourceName,
		ContractID: "", // off-chain; ETH contract address belongs in a separate observability surface, not the canonical row
		Ledger:     0,
		TxHash:     syntheticTxHash(spec.Address, rnd.RoundID),
		OpIndex:    0,
		Timestamp:  rnd.UpdatedAt,
		Asset:      pair.Base,
		Quote:      pair.Quote,
		Price:      canonical.NewAmount(answer),
		Decimals:   decimals,
		Observer:   "",
	}, nil
}

// syntheticTxHash derives a deterministic 64-char hex tx_hash from
// the (feed address, roundId) pair. SHA-256 chosen for determinism
// + collision resistance across feeds; the actual wire format is
// tx_hash CHAR(64) so we hex-encode the 32-byte SHA-256 output.
//
// roundID is the FULL uint80 proxy round id (*big.Int) — hashing the
// wide value means two rounds from different phases that happen to
// share a low-64-bit aggregatorRoundId produce DISTINCT tx hashes,
// so they don't collide on the storage PK (F-1323/G10-01).
//
// Mirrors the synthetic-hash pattern in coingecko/coinmarketcap
// pollers — different inputs (their tickers + currency + ts vs our
// feed + roundId), same idea.
func syntheticTxHash(feedAddress string, roundID *big.Int) string {
	h := sha256.New()
	_, _ = h.Write([]byte(strings.ToLower(feedAddress)))
	_, _ = h.Write([]byte(":"))
	if roundID == nil {
		roundID = new(big.Int)
	}
	// Decimal string of the wide id. It's already a unique,
	// deterministic representation per distinct round (no two distinct
	// wide ids share a decimal string), so the SHA-256 input — and
	// thus the synthetic tx_hash — is stable + collision-free across
	// phase rollovers.
	_, _ = h.Write([]byte(roundID.String()))
	return hex.EncodeToString(h.Sum(nil))
}
