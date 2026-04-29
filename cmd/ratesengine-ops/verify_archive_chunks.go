package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
	"golang.org/x/sync/errgroup"

	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
)

// chunkResult is what verifyChunk returns. Carries the running
// counters AND the boundary hashes the orchestrator needs to stitch
// adjacent chunks into one chain-validation pass.
//
// Empty (zero-value) firstSeq/lastSeq indicate the chunk processed
// zero ledgers — only happens when the chunk's range is empty
// (degenerate splits) or the underlying bucket lacks the range.
type chunkResult struct {
	Idx               int
	From              uint32
	To                uint32
	FirstSeq          uint32
	FirstPrevHash     sdkxdr.Hash // PreviousLedgerHash of first ledger seen
	LastSeq           uint32
	LastHash          sdkxdr.Hash
	Verified          int
	Mismatches        int
	CheckpointsOK     int
	CheckpointsMissed int
}

// stitchChunks validates the boundary between adjacent chunks: the
// last hash of chunk[i] must equal the first PreviousLedgerHash of
// chunk[i+1], AND chunk[i].LastSeq + 1 must equal chunk[i+1].FirstSeq
// (no gap). Returns nil when every adjacent pair stitches cleanly.
//
// Single-chunk results have no boundary to check; they pass.
//
// Empty chunks (zero ledgers processed) are skipped — the SDK's
// stream may legitimately yield zero ledgers for ranges before a
// bucket exists. Two adjacent empty chunks pass; an empty chunk
// between two non-empty chunks creates a hole that surfaces as a
// boundary mismatch on the surrounding pair.
func stitchChunks(results []chunkResult) error {
	if len(results) <= 1 {
		return nil
	}
	for i := 0; i < len(results)-1; i++ {
		left := results[i]
		right := results[i+1]
		if left.Verified == 0 || right.Verified == 0 {
			continue
		}
		if left.LastSeq+1 != right.FirstSeq {
			return fmt.Errorf("chunk[%d→%d] boundary gap: chunk[%d].LastSeq=%d, chunk[%d].FirstSeq=%d",
				left.Idx, right.Idx, left.Idx, left.LastSeq, right.Idx, right.FirstSeq)
		}
		if left.LastHash != right.FirstPrevHash {
			return fmt.Errorf("chunk[%d→%d] boundary chain break at ledger %d:\n"+
				"  chunk[%d].LastHash         = %s\n"+
				"  chunk[%d].FirstPrevHash    = %s",
				left.Idx, right.Idx, left.LastSeq,
				left.Idx, hashToHex(left.LastHash),
				right.Idx, hashToHex(right.FirstPrevHash))
		}
	}
	return nil
}

// verifyChunk walks one chunk's ledger range and returns the
// counters + boundary hashes the orchestrator needs to stitch
// chunks. Pure walk-logic — no parent-context creation, no flag
// parsing; the caller controls those.
//
// chainCheckInternal: when true, validates ledger N's
// PreviousLedgerHash against ledger N-1's hash within this chunk.
// Cross-chunk boundaries are validated by stitchChunks instead.
//
// Errors abort the chunk's walk; the orchestrator's errgroup
// cancels sibling chunks. The verification semantics match the
// pre-parallel verifyArchiveLCMWalk one-for-one.
//
//nolint:gocognit,funlen,gocyclo // walk-loop linearity beats premature splitting
func verifyChunk(
	ctx context.Context,
	lsCfg ledgerstream.Config,
	chunk rangeChunk,
	idx int,
	chainCheckInternal, doCheckpoint bool,
	archiveRoot string,
	progressMu *sync.Mutex,
	startedAt time.Time,
	progressEvery time.Duration,
	totalVerified *int64,
) (chunkResult, error) {
	res := chunkResult{Idx: idx, From: chunk.from, To: chunk.to}

	var (
		prevSeq      uint32
		prevHash     sdkxdr.Hash
		hasPrev      bool
		lastProgress time.Time
	)

	err := ledgerstream.Stream(ctx, lsCfg, chunk.from, chunk.to,
		func(lcm sdkxdr.LedgerCloseMeta) error {
			seq := lcm.LedgerSequence()
			hash := lcm.LedgerHash()
			header, ok := extractLedgerHeader(lcm)
			if !ok {
				return fmt.Errorf("ledger %d: cannot extract LedgerHeader", seq)
			}

			// Capture boundary hashes on first observed ledger.
			if !hasPrev {
				res.FirstSeq = seq
				res.FirstPrevHash = header.PreviousLedgerHash
			}

			if chainCheckInternal && hasPrev {
				if seq != prevSeq+1 {
					res.Mismatches++
					return fmt.Errorf("chunk[%d] sequence gap: %d → %d (expected %d)",
						idx, prevSeq, seq, prevSeq+1)
				}
				if header.PreviousLedgerHash != prevHash {
					res.Mismatches++
					return fmt.Errorf("chunk[%d] chain break at ledger %d:\n"+
						"  ledger[%d].Hash              = %s\n"+
						"  ledger[%d].PreviousLedgerHash = %s",
						idx, seq, prevSeq, hashToHex(prevHash),
						seq, hashToHex(header.PreviousLedgerHash))
				}
			}

			if doCheckpoint && seq%64 == 63 {
				expected, hit, cerr := readArchivedLedgerHash(archiveRoot, seq)
				switch {
				case cerr != nil:
					res.Mismatches++
					return fmt.Errorf("ledger %d: archive read failed: %w", seq, cerr)
				case !hit:
					res.CheckpointsMissed++
				case expected != hash:
					res.Mismatches++
					return fmt.Errorf("checkpoint anchor mismatch at ledger %d:\n"+
						"  our LCM hash          = %s\n"+
						"  archive-signed hash   = %s",
						seq, hashToHex(hash), hashToHex(expected))
				default:
					res.CheckpointsOK++
				}
			}

			prevSeq = seq
			prevHash = hash
			hasPrev = true
			res.Verified++
			res.LastSeq = seq
			res.LastHash = hash

			if time.Since(lastProgress) >= progressEvery {
				progressMu.Lock()
				// Aggregate verified across all chunks for the
				// progress line — operators want one running total,
				// not N independent counters.
				agg := *totalVerified + int64(res.Verified)
				fmt.Fprintf(os.Stderr, "verify-archive: chunk[%d] ledger %d, agg %d verified, %.0f ledgers/s\n",
					idx, seq, agg, float64(agg)/time.Since(startedAt).Seconds())
				progressMu.Unlock()
				lastProgress = time.Now()
			}
			return nil
		},
	)
	return res, err
}

// runVerifyChunks orchestrates parallel chunk verification. Splits
// the range, runs `workers` chunks concurrently via errgroup,
// stitches boundary hashes after all chunks complete.
//
// First chunk error cancels siblings (errgroup semantics) — fail-fast
// matches the serial walk's behaviour where a single mismatch
// aborts the whole verification.
//
// Returns the aggregated chunkResult counters as a single
// chunkResult (Idx=-1, From/To = the input range) for the orchestrator
// to print as the final summary, plus any walk error.
func runVerifyChunks(
	ctx context.Context,
	lsCfg ledgerstream.Config,
	chunks []rangeChunk,
	doChain, doCheckpoint bool,
	archiveRoot string,
	startedAt time.Time,
	progressEvery time.Duration,
) ([]chunkResult, error) {
	if len(chunks) == 0 {
		return nil, errors.New("verify-archive: empty chunk list")
	}

	results := make([]chunkResult, len(chunks))
	var (
		progressMu    sync.Mutex
		totalVerified int64
		updateMu      sync.Mutex // guards totalVerified + results writes
	)

	g, gctx := errgroup.WithContext(ctx)
	for i, chunk := range chunks {
		i, chunk := i, chunk // capture
		g.Go(func() error {
			res, err := verifyChunk(
				gctx, lsCfg, chunk, i,
				doChain, doCheckpoint, archiveRoot,
				&progressMu, startedAt, progressEvery,
				&totalVerified,
			)
			updateMu.Lock()
			results[i] = res
			totalVerified += int64(res.Verified)
			updateMu.Unlock()
			return err
		})
	}
	walkErr := g.Wait()
	return results, walkErr
}
