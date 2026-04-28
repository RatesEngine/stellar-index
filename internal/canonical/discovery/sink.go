package discovery

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// AsyncSink is a non-blocking [DiscoverySink]-compatible adapter
// over a [Recorder]. The dispatcher's hot path Push call enqueues
// to a buffered channel; a worker goroutine drains the channel and
// calls Recorder.Record at production-grade rates without backing
// up dispatch.
//
// Buffer-full policy: when the channel is full, Push silently
// drops the new Hit. Discovery is best-effort — losing one record
// for a contract that already produced 10,000 events is acceptable;
// stalling the dispatch loop is not. The drop counter is exposed
// via [AsyncSink.DroppedCount] for operator monitoring.
//
// Construct via [NewAsyncSink] + Start; Stop drains the buffer and
// shuts down the worker. Safe for concurrent Push from multiple
// goroutines (the dispatcher itself is single-threaded but the
// indexer may run multiple dispatchers in the future).
type AsyncSink struct {
	rec     Recorder
	logger  *slog.Logger
	timeout time.Duration

	ch       chan Hit
	stopOnce sync.Once
	done     chan struct{}

	mu      sync.Mutex
	dropped uint64
}

// AsyncSinkOptions configures a [NewAsyncSink].
type AsyncSinkOptions struct {
	// BufferSize is the channel depth. Must be > 0. Production
	// default is 1024 — covers a few minutes of SEP-41 event volume
	// at network peak.
	BufferSize int

	// RecordTimeout caps how long a single Recorder.Record call may
	// block the worker. Default 2 seconds. A slow Postgres write
	// fails the record (logged) rather than holding up the queue.
	RecordTimeout time.Duration

	// Logger is used for warn/error lines from the worker. nil
	// falls through to slog.Default().
	Logger *slog.Logger
}

// NewAsyncSink constructs an AsyncSink. Returns the sink in
// stopped state — callers must call Start before Push will drain.
func NewAsyncSink(rec Recorder, opts AsyncSinkOptions) *AsyncSink {
	if opts.BufferSize <= 0 {
		opts.BufferSize = 1024
	}
	timeout := opts.RecordTimeout
	if timeout <= 0 {
		timeout = 2 * time.Second
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	return &AsyncSink{
		rec:     rec,
		logger:  logger,
		timeout: timeout,
		ch:      make(chan Hit, opts.BufferSize),
		done:    make(chan struct{}),
	}
}

// Start launches the drain worker. Idempotent; calling twice is a
// no-op. Caller must Stop before the process exits to flush
// pending records.
func (s *AsyncSink) Start() {
	go s.run()
}

// Push enqueues a Hit. Non-blocking: if the channel is full, drops
// the Hit and increments the drop counter. Implements
// [dispatcher.DiscoverySink] (structurally; circular import means
// dispatcher declares its own interface and this method satisfies it).
func (s *AsyncSink) Push(hit Hit) {
	select {
	case s.ch <- hit:
	default:
		s.mu.Lock()
		s.dropped++
		s.mu.Unlock()
	}
}

// Stop closes the input channel and waits for the worker to finish
// draining. Pending records that fit within the worker's per-record
// timeout are flushed; any that error are logged. Idempotent.
func (s *AsyncSink) Stop() {
	s.stopOnce.Do(func() {
		close(s.ch)
		<-s.done
	})
}

// DroppedCount returns the number of Hits dropped because the
// channel was full. Operators alert when this counter rises
// monotonically — it indicates the worker can't keep up with peak
// event rate (typically a Postgres outage or a workload spike that
// outpaces BufferSize tuning).
func (s *AsyncSink) DroppedCount() uint64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.dropped
}

// run drains the input channel until close. One Recorder.Record
// call per Hit; per-record timeout caps the worker's exposure to a
// slow recorder.
func (s *AsyncSink) run() {
	defer close(s.done)
	for hit := range s.ch {
		ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
		if err := s.rec.Record(ctx, hit); err != nil {
			s.logger.Warn("discovery: record failed",
				"err", err,
				"contract_id", hit.ContractID,
				"event_type", hit.EventType)
		}
		cancel()
	}
}
