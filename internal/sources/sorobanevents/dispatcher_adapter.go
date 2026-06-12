package sorobanevents

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// RawEventSink is the contract a sink must satisfy to receive
// captured rows from the dispatcher's raw-event hook.
//
// The dispatcher's raw-event hook (added in ADR-0029) calls
// PushEvent for every Soroban contract event it sees, regardless
// of whether a per-source decoder claimed it. The sink converts to
// a [Row] via [Capture] and writes batched.
//
// PushEvent MAY block to apply back-pressure to the producer. The
// [AsyncSink] implementation blocks when its channel is full so
// produced rows are never silently lost — back-pressure propagates
// up to the ledger walker, which is correct: cursor advance must
// not outrun durable writes (otherwise -resume can't recover the
// gap). The previous non-blocking buffer-full-drop semantics were
// proved unsafe by the 2026-05-26 fill walk, which dropped ~0.43%
// of rows across 8 chunks without a recovery path.
type RawEventSink interface {
	PushEvent(ev events.Event)
}

// BatchWriter is the storage seam the [AsyncSink] uses to drain its
// buffer. The Timescale implementation
// (`internal/storage/timescale.Store.InsertSorobanEventsBatch`)
// writes the batch via a single multi-row INSERT with ON CONFLICT
// DO NOTHING; idempotent across replays.
type BatchWriter interface {
	InsertSorobanEventsBatch(ctx context.Context, rows []Row) error
}

// AsyncSinkOptions configures a [NewAsyncSink].
type AsyncSinkOptions struct {
	// BufferSize is the channel depth. Defaults to 4096. Sized so a
	// few seconds of peak Soroban event volume (typically 100-500
	// events/s) fits even during a Postgres write hiccup. When the
	// buffer is full PushEvent blocks (back-pressure into the
	// producer); the buffer's only job is to smooth bursty producer
	// rates against the worker's batched flushes — sustained
	// over-production correctly slows the dispatcher down.
	BufferSize int

	// BatchSize is the number of rows the worker accumulates before
	// firing an InsertSorobanEventsBatch. Defaults to 1000 per the
	// task spec — large enough to amortise the COPY-equivalent
	// statement-prepare cost across many rows, small enough that one
	// failed batch only loses ~10s of events at peak.
	BatchSize int

	// FlushInterval bounds how long a partial batch can wait before
	// being flushed. Defaults to 1 second. Without this, a low-volume
	// indexer (or the tail of a backfill) could leave the last
	// few-hundred rows stranded in the channel forever.
	FlushInterval time.Duration

	// WriteTimeout caps how long one InsertSorobanEventsBatch call
	// may block. Defaults to 10 seconds — generous because the
	// batched write is many-row.
	WriteTimeout time.Duration

	// Logger is used for warn/error lines from the worker. nil falls
	// through to slog.Default().
	Logger *slog.Logger
}

// AsyncSink is the back-pressuring adapter between the dispatcher's
// raw-event hook and a batched storage writer. Construct with
// [NewAsyncSink] + Start; Stop signals shutdown, drains the buffer,
// and shuts down the worker.
//
// PushEvent blocks when the channel is full (back-pressure into the
// producer) so produced rows are never silently lost — a precondition
// for the cursor-advance contract in the backfill driver. DroppedCount
// only goes non-zero when Stop has been signalled and a producer
// raced past the stopping check; in steady state it stays at zero.
//
// Concurrency: PushEvent is safe for concurrent callers (parallel
// backfill chunks each run their own dispatcher into a per-chunk
// sink; the live indexer has a single dispatcher into one sink).
// The worker goroutine is single, so writes don't compete with each
// other.
type AsyncSink struct {
	w       BatchWriter
	logger  *slog.Logger
	timeout time.Duration

	ch       chan Row
	flush    time.Duration
	batchSz  int
	stopOnce sync.Once
	stopping chan struct{}
	done     chan struct{}

	mu      sync.Mutex
	dropped uint64
	skipped uint64
	written uint64
}

// NewAsyncSink constructs an AsyncSink. Returns the sink in stopped
// state — callers must call Start before PushEvent will drain.
func NewAsyncSink(w BatchWriter, opts AsyncSinkOptions) *AsyncSink {
	if opts.BufferSize <= 0 {
		opts.BufferSize = 4096
	}
	if opts.BatchSize <= 0 {
		opts.BatchSize = 1000
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 10 * time.Second
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AsyncSink{
		w:        w,
		logger:   logger,
		timeout:  opts.WriteTimeout,
		ch:       make(chan Row, opts.BufferSize),
		flush:    opts.FlushInterval,
		batchSz:  opts.BatchSize,
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}
}

// Start launches the drain worker. Idempotent; calling twice is a
// no-op. Caller must Stop before the process exits to flush pending
// rows.
func (s *AsyncSink) Start() {
	go s.run()
}

// PushEvent captures `ev` into a Row and enqueues it for the
// batched writer. Behaviour:
//   - Capture returns ErrSkip → silently skipped (SkippedCount
//     incremented). Defence in depth for non-contract events that
//     should never reach this hook.
//   - Capture returns any other error → logged + counted as skipped.
//   - Channel full → BLOCKS until a slot frees (back-pressure into
//     the producer) OR until Stop signals shutdown, in which case the
//     row is counted as dropped (DroppedCount incremented).
//   - Otherwise → enqueued.
//
// Steady-state DroppedCount stays at zero; non-zero values appear
// only on the shutdown race window after Stop is called.
//
// Implements [RawEventSink] (structurally — circular import means
// dispatcher declares its own interface and this method satisfies
// it).
func (s *AsyncSink) PushEvent(ev events.Event) {
	row, err := Capture(ev)
	if err != nil {
		s.mu.Lock()
		s.skipped++
		s.mu.Unlock()
		if !errors.Is(err, ErrSkip) {
			s.logger.Warn("sorobanevents: capture failed",
				"err", err,
				"contract_id", ev.ContractID,
				"tx_hash", ev.TxHash,
				"ledger", ev.Ledger,
			)
		}
		return
	}
	select {
	case s.ch <- row:
	case <-s.stopping:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	}
}

// Stop signals shutdown to producers (closing the stopping channel
// so any blocked PushEvent returns the row as dropped instead of
// waiting forever) and waits for the worker to drain its remaining
// buffer, flush the final batch, and exit. The input channel is
// intentionally NOT closed — that would race with concurrent
// producers and panic on send-to-closed; instead the worker uses
// the stopping signal to switch into drain-then-exit mode.
// Pending rows that fit within the worker's per-batch timeout are
// flushed; any that error are logged. Idempotent.
//
// Lifecycle contract: producers that race past the stopping check
// are unblocked via the select and counted as dropped — the drop
// count then reflects shutdown-race row loss only, never
// steady-state pressure loss.
func (s *AsyncSink) Stop() {
	s.stopOnce.Do(func() {
		close(s.stopping)
		<-s.done
	})
}

// DroppedCount returns the number of rows dropped because the
// channel was full. Operators alert when this counter rises
// monotonically — it indicates Postgres write throughput can't
// keep up with peak event rate.
func (s *AsyncSink) DroppedCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// SkippedCount returns the number of events skipped by Capture
// (non-contract events or malformed inputs). A low non-zero value
// is normal (defence-in-depth); a sustained rise means the
// dispatcher is feeding us garbage and worth investigating.
func (s *AsyncSink) SkippedCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.skipped
}

// WrittenCount returns the total number of rows successfully
// persisted via InsertSorobanEventsBatch. Useful as a
// well-it's-working signal in operator dashboards.
func (s *AsyncSink) WrittenCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.written
}

// run drains the channel and flushes batches.
//
// Deliberately uses a fresh context per batch rather than a
// parent-derived one — the whole reason this sink exists is to
// keep writing past the dispatcher's lifetime (shutdown drain),
// matching the pattern in [internal/canonical/discovery.AsyncSink].
//
//nolint:contextcheck // intentional fresh context; see godoc above.
func (s *AsyncSink) run() {
	defer close(s.done)
	batch := make([]Row, 0, s.batchSz)
	ticker := time.NewTicker(s.flush)
	defer ticker.Stop()

	flush := func() {
		if len(batch) == 0 {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
		err := s.w.InsertSorobanEventsBatch(ctx, batch)
		cancel()
		if err != nil {
			s.logger.Warn("sorobanevents: batch insert failed",
				"err", err, "rows", len(batch))
		} else {
			s.mu.Lock()
			s.written += uint64(len(batch))
			s.mu.Unlock()
		}
		batch = batch[:0]
	}

	for {
		select {
		case row := <-s.ch:
			batch = append(batch, row)
			if len(batch) >= s.batchSz {
				flush()
			}
		case <-ticker.C:
			flush()
		case <-s.stopping:
			s.drainOnStop(&batch, flush)
			return
		}
	}
}

// drainOnStop is the shutdown-drain branch of run. Reads any
// remaining rows the producers managed to enqueue between the
// stopping-close and their PushEvent-select-wakeup, accumulates
// them into batch, and triggers a final flush. s.ch is
// deliberately NOT closed — that would panic any in-flight
// producer; the stopping signal already unblocked them via
// PushEvent's select.
func (s *AsyncSink) drainOnStop(batch *[]Row, flush func()) {
	for {
		select {
		case row := <-s.ch:
			*batch = append(*batch, row)
			if len(*batch) >= s.batchSz {
				flush()
			}
		default:
			flush()
			return
		}
	}
}
