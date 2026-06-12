package sorobanevents

import (
	"context"
	"math/big"
	"sync"
	"testing"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/events"
)

// blockableWriter holds a release channel — InsertSorobanEventsBatch
// blocks until the test calls release(). Records the row batches it
// successfully writes so assertions can verify durability.
type blockableWriter struct {
	mu      sync.Mutex
	written [][]Row
	release chan struct{}
}

func newBlockableWriter() *blockableWriter {
	return &blockableWriter{release: make(chan struct{})}
}

func (w *blockableWriter) InsertSorobanEventsBatch(ctx context.Context, rows []Row) error {
	select {
	case <-w.release:
	case <-ctx.Done():
		return ctx.Err()
	}
	w.mu.Lock()
	cp := make([]Row, len(rows))
	copy(cp, rows)
	w.written = append(w.written, cp)
	w.mu.Unlock()
	return nil
}

func (w *blockableWriter) WrittenRows() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	n := 0
	for _, b := range w.written {
		n += len(b)
	}
	return n
}

// captureableEvent builds a minimal events.Event whose Capture
// succeeds without depending on the full xdr-encoded fixture set
// used by events_test.go.
func captureableEvent(t *testing.T, ledger uint32) events.Event {
	t.Helper()
	contract := mkContractStrkey(t, byte(ledger%256))
	topic := b64SV(t, symbolSV("swap"))
	body := b64SV(t, i128SV(big.NewInt(int64(ledger))))
	return events.Event{
		Type:           "contract",
		Ledger:         ledger,
		LedgerClosedAt: "2026-05-26T12:00:00Z",
		ContractID:     contract,
		OperationIndex: 0,
		TxHash:         mkTxHashHex(byte(ledger % 256)),
		Topic:          []string{topic},
		Value:          body,
	}
}

// TestAsyncSink_PushEventBacksPressure_BufferFull_NoDrops verifies
// the post-2026-05-26 contract: when the channel is full, PushEvent
// blocks rather than dropping the row. This is the invariant the
// backfill cursor relies on (cursor advances per produced ledger;
// drops would leave un-recoverable gaps).
func TestAsyncSink_PushEventBacksPressure_BufferFull_NoDrops(t *testing.T) {
	t.Parallel()

	w := newBlockableWriter()
	const buf = 2
	const batchSz = 2
	sink := NewAsyncSink(w, AsyncSinkOptions{
		BufferSize:    buf,
		BatchSize:     batchSz,
		FlushInterval: 10 * time.Second, // disable time-based flush in test
		WriteTimeout:  time.Second,
	})
	sink.Start()

	const totalRows = 8
	pushed := make(chan struct{})
	go func() {
		defer close(pushed)
		for i := 0; i < totalRows; i++ {
			sink.PushEvent(captureableEvent(t, uint32(1_000_000+i)))
		}
	}()

	// Give the producer time to fill the buffer + batch (cap = buf +
	// batchSz at most) and block on the next send. If the old
	// non-blocking semantics were in play, all 8 pushes would return
	// near-instantly and DroppedCount would jump.
	select {
	case <-pushed:
		t.Fatalf("PushEvent returned before writer was released — back-pressure not applied")
	case <-time.After(100 * time.Millisecond):
		// expected — producer is blocked waiting for the writer.
	}
	if got := sink.DroppedCount(); got != 0 {
		t.Errorf("DroppedCount = %d before release, want 0 (back-pressure must not drop)", got)
	}

	// Release the writer; producer should now drain all rows.
	close(w.release)

	select {
	case <-pushed:
	case <-time.After(2 * time.Second):
		t.Fatalf("producer never finished after writer release")
	}

	sink.Stop()

	if got := sink.WrittenCount(); got != totalRows {
		t.Errorf("WrittenCount = %d, want %d", got, totalRows)
	}
	if got := sink.DroppedCount(); got != 0 {
		t.Errorf("DroppedCount = %d after Stop, want 0", got)
	}
	if got := w.WrittenRows(); got != totalRows {
		t.Errorf("writer received %d rows, want %d", got, totalRows)
	}
}

// TestAsyncSink_StopDrainsPendingRows_NoChannelClose verifies that
// Stop signals via the stopping channel and the worker drains
// remaining buffered rows without panicking (Stop intentionally does
// NOT close the input channel — that would race with concurrent
// producers).
func TestAsyncSink_StopDrainsPendingRows_NoChannelClose(t *testing.T) {
	t.Parallel()

	w := newBlockableWriter()
	close(w.release) // writer is always ready

	sink := NewAsyncSink(w, AsyncSinkOptions{
		BufferSize:    16,
		BatchSize:     4,
		FlushInterval: 10 * time.Second,
		WriteTimeout:  time.Second,
	})
	sink.Start()

	const total = 10
	for i := 0; i < total; i++ {
		sink.PushEvent(captureableEvent(t, uint32(2_000_000+i)))
	}
	sink.Stop()

	if got := sink.WrittenCount(); got != total {
		t.Errorf("WrittenCount = %d, want %d", got, total)
	}
	if got := w.WrittenRows(); got != total {
		t.Errorf("writer received %d rows, want %d", got, total)
	}
	if got := sink.DroppedCount(); got != 0 {
		t.Errorf("DroppedCount = %d, want 0", got)
	}
}

// TestAsyncSink_StopReleasesBlockedProducers verifies the
// shutdown-race semantics: a PushEvent already blocked on a full
// channel must return (counted as dropped) once Stop is signalled,
// rather than deadlocking the producer.
func TestAsyncSink_StopReleasesBlockedProducers(t *testing.T) {
	t.Parallel()

	w := newBlockableWriter() // writer never releases — sink stays full

	sink := NewAsyncSink(w, AsyncSinkOptions{
		BufferSize:    1,
		BatchSize:     1,
		FlushInterval: 10 * time.Second,
		WriteTimeout:  time.Second,
	})
	sink.Start()

	// Fire enough pushes that one will block — the writer never
	// finishes a batch so after the first row is consumed into a
	// batch the buffer fills and the next pushes block.
	const total = 4
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < total; i++ {
			sink.PushEvent(captureableEvent(t, uint32(3_000_000+i)))
		}
	}()

	// Producer should be blocked.
	select {
	case <-done:
		t.Fatalf("producer finished before Stop — back-pressure not applied")
	case <-time.After(100 * time.Millisecond):
	}

	// Stop must unblock the producer. We don't release the writer:
	// the worker's pending InsertSorobanEventsBatch will hit the
	// per-batch WriteTimeout (1s) and the worker will exit; Stop
	// returns once the worker is done.
	stopReturned := make(chan struct{})
	go func() {
		sink.Stop()
		close(stopReturned)
	}()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatalf("blocked producer not released by Stop")
	}
	select {
	case <-stopReturned:
	case <-time.After(3 * time.Second):
		t.Fatalf("Stop did not return")
	}

	// At least one drop should be recorded (the producer that was
	// past the stopping check at close time).
	if got := sink.DroppedCount(); got == 0 {
		t.Errorf("DroppedCount = 0, want >0 after shutdown-race")
	}
}
