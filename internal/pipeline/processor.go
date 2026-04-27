package pipeline

import (
	"context"
	"log/slog"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/dispatcher"
)

// ProcessLedger runs the dispatcher over one LedgerCloseMeta and
// forwards every emitted event to the supplied sink channel.
//
// Two error paths:
//
//   - Dispatcher returns an error (malformed LCM, decoder panic
//     surfaced through the recover boundary): logged at WARN, then
//     this function returns nil. We absorb single-ledger failures
//     so a malformed payload doesn't tear down the whole stream;
//     the ledgerstream retry layer surfaces persistent failures via
//     its own error channel.
//   - ctx is canceled while pushing events: ctx.Err() is returned.
//     The caller (typically a streamer goroutine) treats this as
//     shutdown.
//
// Caller responsibility: cursor persistence + cursor metric. The
// long-running indexer wraps this with an UpsertCursor +
// CursorLastLedger.Set. The bounded-replay backfill skips both —
// it's a one-shot run with explicit -from/-to and doesn't share
// the indexer's cursor row.
func ProcessLedger(
	ctx context.Context,
	disp *dispatcher.Dispatcher,
	events chan<- consumer.Event,
	logger *slog.Logger,
	lcm sdkxdr.LedgerCloseMeta,
	networkPassphrase string,
) error {
	outputs, err := disp.ProcessLedger(lcm, networkPassphrase)
	if err != nil {
		logger.Warn("dispatcher rejected ledger",
			"ledger", lcm.LedgerSequence(),
			"err", err,
		)
		return nil
	}
	for _, ev := range outputs {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case events <- ev:
		}
	}
	return nil
}
