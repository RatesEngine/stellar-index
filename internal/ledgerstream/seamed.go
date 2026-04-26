package ledgerstream

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// StreamArchiveThenLive reads ledgers in [from, ∞) by chaining two
// Stream calls: a bounded read from `archive` covering [from, seam-1],
// then an unbounded read from `live` starting at seam.
//
// Behaviour:
//   - seam == 0 → archive bucket is unused; the call degrades to a
//     plain unbounded Stream(live, from, 0, ...). This matches the
//     pre-2026-04-26 indexer behaviour.
//   - from >= seam → all wanted data lives in the live bucket; same
//     degradation as above.
//   - from < seam → bounded archive read then unbounded live read.
//
// Restart safety: each Stream call writes the cursor inside the
// callback after every successful batch. A crash mid-archive-phase
// resumes from cursor+1 (which is still < seam) on next start, so
// the same archive→live progression replays. A crash after the
// archive phase exits but before live starts is benign — the cursor
// at that moment is seam-1; restart computes from = seam, takes the
// live-only branch, and proceeds.
//
// logger is used to mark the phase boundaries in the journal; if
// nil, the function emits no log lines.
func StreamArchiveThenLive(
	ctx context.Context,
	archive, live Config,
	from, seam uint32,
	logger *slog.Logger,
	callback func(xdr.LedgerCloseMeta) error,
) error {
	if seam == 0 || from >= seam {
		if logger != nil {
			logger.Info("ledgerstream: live-only", "from", from, "seam", seam)
		}
		return Stream(ctx, live, from, 0 /*unbounded*/, callback)
	}

	if logger != nil {
		logger.Info("ledgerstream: archive phase", "from", from, "to", seam-1)
	}
	if err := Stream(ctx, archive, from, seam-1, callback); err != nil {
		return fmt.Errorf("archive phase [%d,%d]: %w", from, seam-1, err)
	}

	if logger != nil {
		logger.Info("ledgerstream: archive phase complete; handing off to live", "seam", seam)
	}
	return Stream(ctx, live, seam, 0 /*unbounded*/, callback)
}
