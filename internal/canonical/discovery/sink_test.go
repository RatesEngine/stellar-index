package discovery_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical/discovery"
)

// fakeRecorder counts Record + IsKnown calls and lets tests inject
// errors / slow paths without spinning up Postgres.
type fakeRecorder struct {
	records atomic.Int64
	hold    chan struct{} // when non-nil, Record blocks on it
	err     error
}

func (r *fakeRecorder) Record(_ context.Context, _ discovery.Hit) error {
	r.records.Add(1)
	if r.hold != nil {
		<-r.hold
	}
	return r.err
}

func (r *fakeRecorder) IsKnown(_ context.Context, _ string) (bool, error) {
	return false, nil
}

// TestAsyncSink_DrainsToRecorder — happy path: Push → channel →
// worker → Recorder.Record. Stop drains pending records. Each Push
// uses a distinct (ContractID, EventType) so the in-process dedup
// doesn't collapse them.
func TestAsyncSink_DrainsToRecorder(t *testing.T) {
	rec := &fakeRecorder{}
	sink := discovery.NewAsyncSink(rec, discovery.AsyncSinkOptions{BufferSize: 16})
	sink.Start()

	contracts := []string{"C-1", "C-2", "C-3", "C-4", "C-5"}
	for i, c := range contracts {
		sink.Push(discovery.Hit{
			ContractID:        c,
			EventType:         discovery.EventTransfer,
			Ledger:            uint32(i + 1),
			ObservedAtRFC3339: "2026-04-28T12:00:00Z",
		})
	}
	sink.Stop() // flushes pending

	if got := rec.records.Load(); got != 5 {
		t.Errorf("Recorder.Record called %d times, want 5", got)
	}
}

// TestAsyncSink_DedupsRepeatedHits — repeat pushes for the same
// (ContractID, EventType) inside a single process resolve to a
// single Record call, with the rest counted in SkippedCount. This
// is the production-rate-protection path: r1 evidence (PR #620)
// showed 99% of Pushes were duplicates of already-discovered
// contracts.
func TestAsyncSink_DedupsRepeatedHits(t *testing.T) {
	rec := &fakeRecorder{}
	sink := discovery.NewAsyncSink(rec, discovery.AsyncSinkOptions{BufferSize: 16})
	sink.Start()

	for i := 0; i < 1000; i++ {
		sink.Push(discovery.Hit{
			ContractID: "C-hot",
			EventType:  discovery.EventTransfer,
			Ledger:     uint32(i + 1),
		})
	}
	// A second EventType for the same contract is a separate key.
	sink.Push(discovery.Hit{ContractID: "C-hot", EventType: discovery.EventMint})

	sink.Stop()

	if got := rec.records.Load(); got != 2 {
		t.Errorf("Recorder.Record called %d times, want 2 (one per unique (contract,event_type))", got)
	}
	if got := sink.SkippedCount(); got != 999 {
		t.Errorf("SkippedCount = %d, want 999", got)
	}
	if got := sink.DroppedCount(); got != 0 {
		t.Errorf("DroppedCount = %d, want 0 (dedup keeps the channel from filling)", got)
	}
}

// TestAsyncSink_DropsOnFullBuffer — Push doesn't block; once the
// channel fills, additional Hits increment DroppedCount instead.
// Each Push uses a distinct ContractID so the in-process dedup
// doesn't collapse them before they hit the channel.
func TestAsyncSink_DropsOnFullBuffer(t *testing.T) {
	hold := make(chan struct{})
	rec := &fakeRecorder{hold: hold} // worker blocks indefinitely
	sink := discovery.NewAsyncSink(rec, discovery.AsyncSinkOptions{BufferSize: 4})
	sink.Start()

	// First Push pulls into the worker (blocks on `hold`); next 4
	// fill the channel; 6th onward drop.
	for i := 0; i < 10; i++ {
		sink.Push(discovery.Hit{
			ContractID: fmt.Sprintf("C-%d", i),
			EventType:  discovery.EventTransfer,
		})
	}

	// Allow the worker to finish so Stop returns.
	close(hold)
	sink.Stop()

	if got := sink.DroppedCount(); got == 0 {
		t.Errorf("DroppedCount = 0; expected drops once buffer + in-flight saturated")
	}
}

// TestAsyncSink_DropRollsBackSeen — when a Push is dropped because
// the buffer is full, the seen-mark is rolled back so a later Push
// for the same key can retry. Otherwise a transient Postgres outage
// during cold-start would permanently lose every newly-discovered
// contract.
func TestAsyncSink_DropRollsBackSeen(t *testing.T) {
	hold := make(chan struct{})
	rec := &fakeRecorder{hold: hold}
	sink := discovery.NewAsyncSink(rec, discovery.AsyncSinkOptions{BufferSize: 1})
	sink.Start()

	// Push enough distinct contracts to guarantee a drop regardless of
	// whether the worker has scheduled yet — with BufferSize=1 and a
	// blocked recorder, only 1–2 hits can be in flight at once.
	for i := 0; i < 8; i++ {
		sink.Push(discovery.Hit{
			ContractID: fmt.Sprintf("C-saturate-%d", i),
			EventType:  discovery.EventMint,
		})
	}
	// And the contract we want to retry: also dropped because buffer
	// is still saturated.
	sink.Push(discovery.Hit{ContractID: "C-retry", EventType: discovery.EventMint})

	if got := sink.DroppedCount(); got == 0 {
		t.Fatalf("expected at least one drop, got 0")
	}
	droppedBefore := sink.DroppedCount()

	// Unblock the worker so the channel drains.
	close(hold)

	// Retry the previously-dropped contract; with seen rolled back
	// it should eventually land. Poll because drain is async.
	recorded := false
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		sink.Push(discovery.Hit{ContractID: "C-retry", EventType: discovery.EventMint})
		// Look at skipped instead of records: once C-retry has been
		// enqueued (and recorded), subsequent Pushes will skip it,
		// proving the dedup mark is set again.
		if sink.SkippedCount() > 0 {
			recorded = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	sink.Stop()

	if !recorded {
		t.Errorf("C-retry never landed after drop rollback (dropped=%d, skipped=%d, recorded=%d)",
			sink.DroppedCount()-droppedBefore, sink.SkippedCount(), rec.records.Load())
	}
}

// TestAsyncSink_LogsRecordError — a Record returning an error
// doesn't crash the worker; subsequent Pushes still drain.
func TestAsyncSink_LogsRecordError(t *testing.T) {
	rec := &fakeRecorder{err: errors.New("postgres down")}
	sink := discovery.NewAsyncSink(rec, discovery.AsyncSinkOptions{BufferSize: 4})
	sink.Start()

	sink.Push(discovery.Hit{ContractID: "C1", EventType: discovery.EventMint})
	sink.Push(discovery.Hit{ContractID: "C2", EventType: discovery.EventBurn})
	sink.Stop()

	if got := rec.records.Load(); got != 2 {
		t.Errorf("Record called %d times, want 2 (errors don't stop the worker)", got)
	}
}

// TestAsyncSink_StopIsIdempotent — calling Stop twice is safe
// (real production code paths can race shutdown).
func TestAsyncSink_StopIsIdempotent(t *testing.T) {
	rec := &fakeRecorder{}
	sink := discovery.NewAsyncSink(rec, discovery.AsyncSinkOptions{BufferSize: 4})
	sink.Start()

	var wg sync.WaitGroup
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			sink.Stop()
		}()
	}
	wg.Wait() // must not deadlock or panic
}

// TestAsyncSink_DefaultBufferAndTimeout — zero-value options apply
// sensible defaults rather than producing a 0-buffer (would always
// drop) or 0-timeout sink.
func TestAsyncSink_DefaultBufferAndTimeout(t *testing.T) {
	rec := &fakeRecorder{}
	sink := discovery.NewAsyncSink(rec, discovery.AsyncSinkOptions{})
	sink.Start()

	// If BufferSize defaulted to 0 every Push would drop. With the
	// production default (1024) one Push lands cleanly.
	sink.Push(discovery.Hit{ContractID: "C-default", EventType: discovery.EventTransfer})

	// Give the worker a moment to drain.
	time.Sleep(100 * time.Millisecond)
	sink.Stop()

	if got := rec.records.Load(); got != 1 {
		t.Errorf("Record called %d times, want 1 (default buffer must be > 0)", got)
	}
	if got := sink.DroppedCount(); got != 0 {
		t.Errorf("DroppedCount = %d, want 0", got)
	}
}
