package pipeline

import (
	"context"
	"fmt"
	"log/slog"

	sdkxdr "github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/consumer"
	"github.com/StellarAtlas/stellar-atlas/internal/dispatcher"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
)

// ProcessLedger runs the dispatcher over one LedgerCloseMeta and
// forwards every emitted event to the supplied sink channel.
//
// Error paths:
//
//   - Dispatcher returns an error (malformed LCM, reader build
//     failure, decoder panic surfaced through the recover boundary):
//     logged at WARN, then returned to the caller unchanged so the
//     caller can refuse cursor advancement for that ledger.
//   - ctx is canceled while pushing events: ctx.Err() is returned.
//     The caller (typically a streamer goroutine) treats this as
//     shutdown.
//
// Caller responsibility: cursor persistence + cursor metric. Those
// must happen only after ProcessLedger returns nil for the ledger.
func ProcessLedger(
	ctx context.Context,
	disp *dispatcher.Dispatcher,
	events chan<- consumer.Event,
	logger *slog.Logger,
	lcm sdkxdr.LedgerCloseMeta,
	networkPassphrase string,
) (err error) {
	before := disp.Stats()
	defer func() {
		emitDispatcherMetricDeltas(before, disp.Stats())
		if r := recover(); r != nil {
			err = fmt.Errorf("dispatcher panic for ledger %d: %v", lcm.LedgerSequence(), r)
			logger.Warn("dispatcher panicked",
				"ledger", lcm.LedgerSequence(),
				"panic", fmt.Sprintf("%v", r),
			)
		}
	}()
	outputs, err := disp.ProcessLedger(lcm, networkPassphrase)
	if err != nil {
		logger.Warn("dispatcher rejected ledger",
			"ledger", lcm.LedgerSequence(),
			"err", err,
		)
		return err
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

func emitDispatcherMetricDeltas(before, after dispatcher.Stats) {
	for source, n := range after.EventsSeen {
		delta := n - before.EventsSeen[source]
		if delta <= 0 {
			continue
		}
		obs.SourceMatchedEventsTotal.WithLabelValues(source).Add(float64(delta))
	}
	for source, n := range after.DecodeErrors {
		delta := n - before.DecodeErrors[source]
		if delta <= 0 {
			continue
		}
		obs.SourceDecodeErrorsTotal.WithLabelValues(source).Add(float64(delta))
	}
	for source, n := range after.OrphanEvents {
		delta := n - before.OrphanEvents[source]
		if delta <= 0 {
			continue
		}
		obs.SourceOrphanEventsTotal.WithLabelValues(source).Add(float64(delta))
	}
}
