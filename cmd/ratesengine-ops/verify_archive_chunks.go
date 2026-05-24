package main

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"
	"golang.org/x/sync/errgroup"

	"github.com/RatesEngine/rates-engine/internal/ledgerstream"
	"github.com/RatesEngine/rates-engine/internal/obs"
)

// startVerifyArchiveMetrics spins up a tiny http.Server on addr
// exposing /metrics from the obs Registry. Returns a stop function
// that gracefully shuts down the server (≤ 5 s for in-flight scrapes
// to drain). Used when -metrics-listen is supplied.
//
// Errors when the bind itself fails (port already in use, perms);
// scrape failures during the run don't propagate to the caller —
// the server just logs and continues, so a flaky scraper can't
// stall verification.
func startVerifyArchiveMetrics(addr string) (func(), error) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", obs.Handler())

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	lc := net.ListenConfig{}
	listenCtx, cancelListen := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancelListen()
	ln, err := lc.Listen(listenCtx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("listen %s: %w", addr, err)
	}
	go func() {
		_ = srv.Serve(ln)
	}()

	return func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}, nil
}

// checkResumeFromHash compares the first chunk's FirstPrevHash
// against an operator-supplied hex hash. Used to prove cross-run
// continuity: when a previous verification halted partway, the
// operator records its last verified ledger's hash and passes it on
// the resume run via -resume-from-hash; this check enforces the
// boundary explicitly rather than relying on the implicit-overlap
// proof from re-reading the seam ledger.
//
// Errors:
//   - hex parse failures (operator typo, wrong length) — surfaced
//     with a clear message that names the expected format.
//   - hash mismatch — names both hashes + the seam ledger so the
//     operator can audit which side is wrong (likely indexer or
//     archive corruption between runs).
func checkResumeFromHash(expectedHex string, firstPrevHash sdkxdr.Hash, firstSeq uint32) error {
	expectedBytes, err := hex.DecodeString(expectedHex)
	if err != nil {
		return fmt.Errorf("resume-from-hash: parse hex %q: %w", expectedHex, err)
	}
	if len(expectedBytes) != len(firstPrevHash) {
		return fmt.Errorf("resume-from-hash: hex length %d, want %d (32-byte SHA-256)", len(expectedBytes), len(firstPrevHash))
	}
	var expected sdkxdr.Hash
	copy(expected[:], expectedBytes)
	if expected != firstPrevHash {
		return fmt.Errorf("resume-from-hash boundary mismatch at ledger %d:\n"+
			"  -resume-from-hash         = %s\n"+
			"  observed FirstPrevHash    = %s",
			firstSeq, expectedHex, hashToHex(firstPrevHash))
	}
	return nil
}

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
	chunkLabel := strconv.Itoa(idx)

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
					obs.VerifyArchiveMismatchesTotal.WithLabelValues(chunkLabel, "sequence").Inc()
					return fmt.Errorf("chunk[%d] sequence gap: %d → %d (expected %d)",
						idx, prevSeq, seq, prevSeq+1)
				}
				if header.PreviousLedgerHash != prevHash {
					res.Mismatches++
					obs.VerifyArchiveMismatchesTotal.WithLabelValues(chunkLabel, "chain").Inc()
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
					obs.VerifyArchiveMismatchesTotal.WithLabelValues(chunkLabel, "checkpoint").Inc()
					return fmt.Errorf("ledger %d: archive read failed: %w", seq, cerr)
				case !hit:
					res.CheckpointsMissed++
					obs.VerifyArchiveCheckpointsTotal.WithLabelValues(chunkLabel, "missed").Inc()
				case expected != hash:
					res.Mismatches++
					obs.VerifyArchiveMismatchesTotal.WithLabelValues(chunkLabel, "checkpoint").Inc()
					return fmt.Errorf("checkpoint anchor mismatch at ledger %d:\n"+
						"  our LCM hash          = %s\n"+
						"  archive-signed hash   = %s",
						seq, hashToHex(hash), hashToHex(expected))
				default:
					res.CheckpointsOK++
					obs.VerifyArchiveCheckpointsTotal.WithLabelValues(chunkLabel, "matched").Inc()
				}
			}

			prevSeq = seq
			prevHash = hash
			hasPrev = true
			res.Verified++
			res.LastSeq = seq
			res.LastHash = hash
			obs.VerifyArchiveLedgersVerified.WithLabelValues(chunkLabel).Inc()
			obs.VerifyArchiveCurrentLedger.WithLabelValues(chunkLabel).Set(float64(seq))

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

// chunkOrchestratorOpts carries resume-aware hooks for
// [runVerifyChunks]. The zero value is the legacy single-pass shape:
// chunks are numbered 0..len-1 in the progress log + result array,
// and no per-chunk completion is reported anywhere.
type chunkOrchestratorOpts struct {
	// ChunkIdxs maps each position in `chunks` to the chunk's
	// ORIGINAL idx in the parent run's full pre-resume chunk list.
	// nil ⇒ identity (0, 1, …, len(chunks)-1). When resume has
	// filtered already-Done chunks out, this preserves the original
	// numbering so the progress log + state-file row stay
	// consistent across restarts ("chunk[5]" always means the same
	// 5M-ledger slice).
	ChunkIdxs []int

	// OnChunkDone is invoked synchronously after each chunk's
	// walker returns without error, with the chunk's original idx
	// and its terminal chunkResult. nil ⇒ no-op. The caller
	// (verifyArchiveLCMWalk) writes a state-file update marking
	// the chunk Done so a subsequent SIGTERMed run can skip it.
	OnChunkDone func(originalIdx int, res chunkResult)
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
	opts chunkOrchestratorOpts,
) ([]chunkResult, error) {
	if len(chunks) == 0 {
		return nil, errors.New("verify-archive: empty chunk list")
	}

	// Resolve per-position → original-idx mapping. Default is
	// identity (no resume filtering).
	idxs := opts.ChunkIdxs
	if idxs == nil {
		idxs = make([]int, len(chunks))
		for i := range chunks {
			idxs[i] = i
		}
	}
	if len(idxs) != len(chunks) {
		return nil, fmt.Errorf("verify-archive: ChunkIdxs len %d != chunks len %d", len(idxs), len(chunks))
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
		originalIdx := idxs[i]
		g.Go(func() error {
			res, err := verifyChunk(
				gctx, lsCfg, chunk, originalIdx,
				doChain, doCheckpoint, archiveRoot,
				&progressMu, startedAt, progressEvery,
				&totalVerified,
			)
			updateMu.Lock()
			results[i] = res
			totalVerified += int64(res.Verified)
			updateMu.Unlock()
			// Mark Done in the state file only on clean completion
			// — an errored chunk's partial-progress isn't durable
			// (in-flight-chunk resume is a future revision; see
			// ChunkProgress doc in verify_archive_state.go).
			if err == nil && opts.OnChunkDone != nil {
				opts.OnChunkDone(originalIdx, res)
			}
			return err
		})
	}
	walkErr := g.Wait()
	return results, walkErr
}
