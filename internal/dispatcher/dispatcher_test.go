package dispatcher

import (
	"errors"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/consumer"
	"github.com/RatesEngine/rates-engine/internal/events"
)

// ─── test-only decoder implementation ────────────────────────────

type fakeDecoder struct {
	name        string
	topic0      string
	decodeFn    func(events.Event) ([]consumer.Event, error)
	matchCount  int
	decodeCount int
}

func (d *fakeDecoder) Name() string { return d.name }

func (d *fakeDecoder) Matches(ev events.Event) bool {
	d.matchCount++
	return len(ev.Topic) > 0 && ev.Topic[0] == d.topic0
}

func (d *fakeDecoder) Decode(ev events.Event) ([]consumer.Event, error) {
	d.decodeCount++
	if d.decodeFn == nil {
		return nil, nil
	}
	return d.decodeFn(ev)
}

type fakeEvent struct {
	source string
	kind   string
}

func (e fakeEvent) Source() string    { return e.source }
func (e fakeEvent) EventKind() string { return e.kind }

// ─── dispatchOne: routing + error accounting ─────────────────────

func TestDispatch_routesToFirstMatch(t *testing.T) {
	dA := &fakeDecoder{
		name:   "alpha",
		topic0: "A",
		decodeFn: func(ev events.Event) ([]consumer.Event, error) {
			return []consumer.Event{fakeEvent{source: "alpha", kind: "trade"}}, nil
		},
	}
	dB := &fakeDecoder{name: "beta", topic0: "B"}
	disp := New(dA, dB)

	outs, err := disp.dispatchOne(events.Event{Topic: []string{"A"}})
	if err != nil {
		t.Fatalf("dispatchOne: %v", err)
	}
	if len(outs) != 1 {
		t.Fatalf("got %d outputs, want 1", len(outs))
	}
	if outs[0].Source() != "alpha" {
		t.Errorf("wrong source: %q", outs[0].Source())
	}
	if dA.decodeCount != 1 {
		t.Errorf("alpha.Decode called %d times, want 1", dA.decodeCount)
	}
	if dB.decodeCount != 0 {
		t.Errorf("beta.Decode called %d times, want 0 (alpha matched first)", dB.decodeCount)
	}
	// beta's Matches may or may not have been called depending on
	// iteration semantics; the first-match-wins contract is what
	// matters, and that's verified via decodeCount.
}

func TestDispatch_unmatchedCounted(t *testing.T) {
	disp := New(&fakeDecoder{name: "only", topic0: "A"})
	outs, err := disp.dispatchOne(events.Event{Topic: []string{"Z"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 0 {
		t.Errorf("got %d outputs, want 0", len(outs))
	}
	if got := disp.Stats().UnmatchedHits; got != 1 {
		t.Errorf("UnmatchedHits = %d, want 1", got)
	}
}

func TestDispatch_decodeErrorCountedPerSource(t *testing.T) {
	boom := errors.New("decoder explosion")
	disp := New(&fakeDecoder{
		name:     "boom",
		topic0:   "X",
		decodeFn: func(events.Event) ([]consumer.Event, error) { return nil, boom },
	})

	// Two events routed to the same decoder, both fail.
	for i := 0; i < 2; i++ {
		_, err := disp.dispatchOne(events.Event{Topic: []string{"X"}})
		if !errors.Is(err, boom) {
			t.Errorf("iter %d: error chain lost sentinel: %v", i, err)
		}
	}
	if got := disp.Stats().DecodeErrors["boom"]; got != 2 {
		t.Errorf("DecodeErrors[boom] = %d, want 2", got)
	}
	if got := disp.Stats().UnmatchedHits; got != 0 {
		t.Errorf("UnmatchedHits = %d, want 0 (decoder matched but errored)", got)
	}
}

func TestDispatch_emptyDecoderListNoMatch(t *testing.T) {
	disp := New() // no decoders
	outs, err := disp.dispatchOne(events.Event{Topic: []string{"anything"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 0 {
		t.Errorf("got %d outputs from empty dispatcher", len(outs))
	}
	if got := disp.Stats().UnmatchedHits; got != 1 {
		t.Errorf("UnmatchedHits = %d, want 1", got)
	}
}

func TestStats_snapshotsAreImmutable(t *testing.T) {
	disp := New(&fakeDecoder{name: "d", topic0: "T"})
	_, _ = disp.dispatchOne(events.Event{Topic: []string{"unmatched"}})

	s1 := disp.Stats()
	// Mutate the returned map — internal state should not change.
	s1.DecodeErrors["injected"] = 999

	s2 := disp.Stats()
	if _, ok := s2.DecodeErrors["injected"]; ok {
		t.Error("Stats() returned an aliased map — internal state mutable")
	}
}

// ─── OpDecoder dispatch ──────────────────────────────────────────

type fakeOpDecoder struct {
	name     string
	matchTyp xdr.OperationType
	outputs  []consumer.Event
	err      error
	calls    int
}

func (f *fakeOpDecoder) Name() string                  { return f.name }
func (f *fakeOpDecoder) Matches(op xdr.Operation) bool { return op.Body.Type == f.matchTyp }
func (f *fakeOpDecoder) Decode(OpContext) ([]consumer.Event, error) {
	f.calls++
	return f.outputs, f.err
}

func TestRouteOp_matchRoutesToCorrectDecoder(t *testing.T) {
	manageSell := &fakeOpDecoder{
		name:     "manage-sell",
		matchTyp: xdr.OperationTypeManageSellOffer,
		outputs:  []consumer.Event{fakeEvent{source: "manage-sell", kind: "trade"}},
	}
	payment := &fakeOpDecoder{
		name:     "payment",
		matchTyp: xdr.OperationTypePayment,
	}
	d := New()
	d.AddOpDecoder(manageSell)
	d.AddOpDecoder(payment)

	// ManageSellOffer op → routes to manage-sell.
	outs, err := d.RouteOp(OpContext{
		Op: xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeManageSellOffer}},
	})
	if err != nil {
		t.Fatalf("RouteOp: %v", err)
	}
	if len(outs) != 1 || outs[0].Source() != "manage-sell" {
		t.Errorf("wrong routing: %+v", outs)
	}
	if manageSell.calls != 1 || payment.calls != 0 {
		t.Errorf("decoder calls: manage=%d payment=%d", manageSell.calls, payment.calls)
	}

	// CreateAccount op → matches neither.
	outs, err = d.RouteOp(OpContext{
		Op: xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypeCreateAccount}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(outs) != 0 {
		t.Errorf("got %d outputs for unmatched op, want 0", len(outs))
	}
}

func TestRouteOp_decodeErrorCountedPerSource(t *testing.T) {
	boom := errors.New("op decoder explosion")
	d := New()
	d.AddOpDecoder(&fakeOpDecoder{
		name:     "boom",
		matchTyp: xdr.OperationTypePathPaymentStrictSend,
		err:      boom,
	})

	_, err := d.RouteOp(OpContext{
		Op: xdr.Operation{Body: xdr.OperationBody{Type: xdr.OperationTypePathPaymentStrictSend}},
	})
	if !errors.Is(err, boom) {
		t.Errorf("error chain lost sentinel: %v", err)
	}
	if got := d.Stats().DecodeErrors["boom"]; got != 1 {
		t.Errorf("DecodeErrors[boom] = %d, want 1", got)
	}
}

// ─── ProcessLedger happy path — empty ledger (no txs) ────────────

func TestProcessLedger_emptyLedgerYieldsNoOutputs(t *testing.T) {
	// A LedgerCloseMeta with no transactions should produce zero
	// outputs and no error. Validates that the reader construction
	// path doesn't trip on empty ledgers (common during Stellar's
	// quiet periods on testnet).
	lcm := emptyLedgerCloseMeta(t, 42)

	disp := New(&fakeDecoder{name: "unused", topic0: "zzz"})
	outs, err := disp.ProcessLedger(lcm, testPassphrase)
	if err != nil {
		t.Fatalf("ProcessLedger empty ledger: %v", err)
	}
	if len(outs) != 0 {
		t.Errorf("got %d outputs from empty ledger, want 0", len(outs))
	}
	// No events → no matches → no unmatched hits either.
	if got := disp.Stats().UnmatchedHits; got != 0 {
		t.Errorf("UnmatchedHits = %d, want 0", got)
	}
}
