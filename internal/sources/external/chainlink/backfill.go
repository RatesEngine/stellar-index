package chainlink

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
)

// DefaultBackfillChunkBlocks is the number of blocks per
// eth_getLogs call. Alchemy / Infura / QuickNode all cap response
// payloads at ~10MB or ~10k logs; 5k is the safe default that
// avoids "query returned more than 10000 results" errors at the
// long-tail of high-traffic feed addresses.
//
// At ~12s per Ethereum block post-Merge, 5k blocks ≈ 16 hours of
// chain history per call. 90 days of backfill ≈ 130 chunks/feed.
const DefaultBackfillChunkBlocks uint64 = 5_000

// MainnetMergeBlock is a sane lower bound for backfill walks.
// Pre-Merge blocks predate most Chainlink ETH feeds in their
// modern form anyway; clamping here avoids wasting RPC budget on
// 5+ years of empty history. Operators can override via
// BackfillOptions.MinBlock.
const MainnetMergeBlock uint64 = 15_537_393

// BackfillOptions tunes the historical walk.
type BackfillOptions struct {
	// FromBlock is the inclusive lower bound. Zero defaults to
	// MainnetMergeBlock (Sep 2022); operators wanting older
	// history pass an explicit value.
	FromBlock uint64

	// ToBlock is the inclusive upper bound. Zero defaults to the
	// current head block (as reported by eth_blockNumber).
	ToBlock uint64

	// ChunkBlocks overrides DefaultBackfillChunkBlocks. Larger
	// chunks = fewer RPC calls but higher chance of hitting the
	// provider's response-size cap.
	ChunkBlocks uint64

	// Sleep is the per-chunk pause. Zero = no pause; operators
	// throttling under provider rate limits can set e.g. 100ms.
	Sleep time.Duration
}

// Backfill walks every configured feed's AnswerUpdated event log
// across the requested block range and emits one OracleUpdate per
// historical round.
//
// One feed is walked at a time (sequential, not parallel) to keep
// the RPC budget predictable and avoid burst-hammering a single
// endpoint. ~33k calls at 5k blocks/chunk × 516 feeds = ~5h wall
// time at a polite 30-50 req/s, well within Alchemy free tier.
//
// The output channel receives `canonical.OracleUpdate` values and
// is CLOSED on completion (or error) so callers can range over it
// safely. Per-chunk errors are logged + counted but don't stop
// the walk — one bad chunk shouldn't abandon the rest of the
// 90-day range. The first hard error (RPC down for >1 minute,
// for example) is returned synchronously after the channel is
// closed so callers can distinguish "completed with gaps" from
// "completed cleanly."
func (p *Poller) Backfill(ctx context.Context, pairs []canonical.Pair, opts BackfillOptions, out chan<- canonical.OracleUpdate) error {
	defer close(out)

	logger := p.Logger
	if logger == nil {
		logger = slog.Default()
	}

	chunk := opts.ChunkBlocks
	if chunk == 0 {
		chunk = DefaultBackfillChunkBlocks
	}
	if chunk == 0 {
		chunk = 5000 // defensive
	}

	from := opts.FromBlock
	if from == 0 {
		from = MainnetMergeBlock
	}

	to := opts.ToBlock
	if to == 0 {
		head, err := p.Client.EthBlockNumber(ctx)
		if err != nil {
			return fmt.Errorf("backfill: probe head block: %w", err)
		}
		to = head
	}
	if from > to {
		return fmt.Errorf("backfill: from %d > to %d", from, to)
	}

	logger.Info("chainlink backfill starting",
		"source", SourceName,
		"feeds", len(pairs),
		"from_block", from,
		"to_block", to,
		"chunk_blocks", chunk)

	var firstErr error
	for _, pr := range pairs {
		spec, ok := p.FeedMap[pr.String()]
		if !ok {
			continue
		}
		if err := p.backfillFeed(ctx, pr, spec, from, to, chunk, opts.Sleep, logger, out); err != nil {
			if firstErr == nil {
				firstErr = err
			}
			if errors.Is(err, context.Canceled) {
				return err
			}
			// Non-fatal — log + move to next feed.
			logger.Warn("chainlink backfill feed failed",
				"source", SourceName,
				"pair", pr.String(),
				"err", err)
		}
	}
	logger.Info("chainlink backfill complete",
		"source", SourceName,
		"feeds", len(pairs))
	return firstErr
}

// backfillFeed walks one feed's log range chunk-by-chunk. Returns
// the first non-recoverable error (provider down, ctx cancelled);
// per-chunk decode errors are logged + skipped via emitChunkLogs.
func (p *Poller) backfillFeed(
	ctx context.Context,
	pair canonical.Pair,
	spec FeedSpec,
	from, to, chunk uint64,
	sleep time.Duration,
	logger *slog.Logger,
	out chan<- canonical.OracleUpdate,
) error {
	addresses := []string{strings.ToLower(spec.Address)}
	topics := []any{AnswerUpdatedTopic0}

	totalRounds := 0
	for start := from; start <= to; start += chunk {
		end := start + chunk - 1
		if end > to {
			end = to
		}

		logs, err := p.Client.EthGetLogs(ctx, addresses, topics, start, end)
		if err != nil {
			return fmt.Errorf("eth_getLogs %d-%d: %w", start, end, err)
		}

		n, err := p.emitChunkLogs(ctx, pair, spec, logs, logger, out)
		totalRounds += n
		if err != nil {
			return err
		}

		if sleep > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(sleep):
			}
		}
	}

	logger.Info("chainlink backfill feed complete",
		"source", SourceName,
		"pair", pair.String(),
		"address", spec.Address,
		"rounds_emitted", totalRounds,
		"from_block", from,
		"to_block", to)

	return nil
}

// emitChunkLogs decodes one chunk's logs into OracleUpdates and
// fans them through `out`. Returns the count emitted + the first
// fatal error (ctx cancelled). Per-entry decode/project failures
// are logged + skipped; only context cancellation halts the chunk.
func (p *Poller) emitChunkLogs(
	ctx context.Context,
	pair canonical.Pair,
	spec FeedSpec,
	logs []LogEntry,
	logger *slog.Logger,
	out chan<- canonical.OracleUpdate,
) (int, error) {
	n := 0
	for _, entry := range logs {
		rnd, err := decodeAnswerUpdatedLog(entry)
		if err != nil {
			logger.Warn("chainlink backfill decode skip",
				"source", SourceName,
				"pair", pair.String(),
				"block", entry.BlockNumber,
				"tx", entry.TxHash,
				"err", err)
			continue
		}
		u, err := p.project(pair, spec, rnd)
		if err != nil {
			logger.Warn("chainlink backfill project skip",
				"source", SourceName,
				"pair", pair.String(),
				"round", rnd.RoundID,
				"err", err)
			continue
		}
		select {
		case <-ctx.Done():
			return n, ctx.Err()
		case out <- u:
			n++
		}
	}
	return n, nil
}
