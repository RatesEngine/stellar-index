package clickhouse

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// LiveSinkOptions configures a [NewLiveSink].
type LiveSinkOptions struct {
	// BufferSize is the channel depth (default 512). At the live ledger rate
	// (~1 every 5s) this is hours of headroom; it only matters during a CH
	// write hiccup, where a full buffer DROPS (see PushLedger) rather than
	// blocking — the live ingest hot path must never stall on ClickHouse.
	BufferSize int
	// FlushInterval bounds how long a partial batch waits before the worker
	// flushes it to ClickHouse (default 2s). This is the freshness floor: CH
	// lags the chain by at most ~FlushInterval + the indexer's own lag.
	FlushInterval time.Duration
	// WriteTimeout caps one flush (default 30s).
	WriteTimeout time.Duration
	// Logger for worker warn/error lines; nil → slog.Default().
	Logger *slog.Logger
}

// LiveSink is the real-time fan-out adapter (ADR-0034): the indexer pushes each
// ledger's structural [LedgerExtract] and a single worker goroutine batches them
// into ClickHouse on a short interval, keeping the lake within ~seconds of the
// chain (vs the ~10-min ch-live-catchup timer, which remains as the completeness
// backstop for anything this best-effort sink drops under pressure).
//
// Safety: PushLedger is NON-BLOCKING — on a full buffer it DROPS the whole
// LedgerExtract (DroppedCount++) rather than back-pressuring into the live
// ingest loop. A slow or down ClickHouse therefore can never stall Postgres
// ingest / pricing freshness. Only the lake's real-time edge degrades under CH
// pressure.
//
// Completeness: a drop (or a mid-flush write error) leaves a HOLE in the lake.
// The ch-live-catchup timer heals holes — but ONLY if it gap-scans below
// CH_max, not just extends the tip (a tip-only [CH_max+1,tip] catch-up can never
// re-fill a hole the sink already wrote past). The real-time projector
// (ADR-0034 #10) does NOT trust the lake to be hole-free: it reads
// contract_events only up to ContiguousWatermark — the highest ledger with no
// hole below it — so an unhealed drop stalls the projector at the hole rather
// than silently losing the dropped ledger's events.
type LiveSink struct {
	sink    *Sink
	logger  *slog.Logger
	timeout time.Duration
	flush   time.Duration

	ch       chan LedgerExtract
	stopOnce sync.Once
	stopping chan struct{}
	done     chan struct{}

	mu      sync.Mutex
	written uint64
	dropped uint64
	errored uint64
}

// NewLiveSink opens a CH connection (flushEvery high — the worker controls flush
// cadence via FlushInterval, not per-Add) and returns a stopped LiveSink; call
// Start before PushLedger, and Stop before exit to drain.
func NewLiveSink(ctx context.Context, addr string, opts LiveSinkOptions) (*LiveSink, error) {
	if opts.BufferSize <= 0 {
		opts.BufferSize = 512
	}
	if opts.FlushInterval <= 0 {
		opts.FlushInterval = 2 * time.Second
	}
	if opts.WriteTimeout <= 0 {
		opts.WriteTimeout = 30 * time.Second
	}
	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}
	// flushEvery large so Add buffers; the worker's ticker drives flushes at
	// FlushInterval (real-time at the low live rate; batched under burst).
	sink, err := Open(ctx, addr, 1000)
	if err != nil {
		return nil, err
	}
	return &LiveSink{
		sink:     sink,
		logger:   logger,
		timeout:  opts.WriteTimeout,
		flush:    opts.FlushInterval,
		ch:       make(chan LedgerExtract, opts.BufferSize),
		stopping: make(chan struct{}),
		done:     make(chan struct{}),
	}, nil
}

// Start launches the drain worker. Call once.
func (l *LiveSink) Start() { go l.run() }

// PushLedger enqueues a ledger's structural extract for the worker. NON-BLOCKING:
// drops (DroppedCount++) if the buffer is full or shutdown is in progress, so it
// never stalls the caller (the live ingest loop).
func (l *LiveSink) PushLedger(ext LedgerExtract) {
	select {
	case l.ch <- ext:
	default:
		l.bump(&l.dropped)
	}
}

func (l *LiveSink) run() {
	defer close(l.done)
	ticker := time.NewTicker(l.flush)
	defer ticker.Stop()
	for {
		select {
		case ext := <-l.ch:
			l.add(ext)
		case <-ticker.C:
			l.doFlush()
		case <-l.stopping:
			// Drain whatever's buffered, then a final flush, then exit.
			for {
				select {
				case ext := <-l.ch:
					l.add(ext)
				default:
					l.doFlush()
					return
				}
			}
		}
	}
}

func (l *LiveSink) add(ext LedgerExtract) {
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()
	if err := l.sink.Add(ctx, ext); err != nil {
		l.bump(&l.errored)
		l.logger.Warn("clickhouse live-sink: add failed", "ledger", ext.Ledger.LedgerSeq, "err", err)
		return
	}
	l.bump(&l.written)
}

func (l *LiveSink) doFlush() {
	ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
	defer cancel()
	if err := l.sink.Flush(ctx); err != nil {
		l.bump(&l.errored)
		l.logger.Warn("clickhouse live-sink: flush failed", "err", err)
	}
}

// Stop signals shutdown, drains the buffer + final flush, closes the CH conn.
// Idempotent.
func (l *LiveSink) Stop() {
	l.stopOnce.Do(func() {
		close(l.stopping)
		<-l.done
		ctx, cancel := context.WithTimeout(context.Background(), l.timeout)
		defer cancel()
		_ = l.sink.Close(ctx)
	})
}

func (l *LiveSink) bump(p *uint64) {
	l.mu.Lock()
	*p++
	l.mu.Unlock()
}

// WrittenCount / DroppedCount / ErroredCount expose worker counters for metrics
// + the shutdown log line.
func (l *LiveSink) WrittenCount() uint64 { l.mu.Lock(); defer l.mu.Unlock(); return l.written }
func (l *LiveSink) DroppedCount() uint64 { l.mu.Lock(); defer l.mu.Unlock(); return l.dropped }
func (l *LiveSink) ErroredCount() uint64 { l.mu.Lock(); defer l.mu.Unlock(); return l.errored }
