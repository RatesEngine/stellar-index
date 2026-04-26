package dispatcher

import (
	"errors"
	"testing"
	"time"

	"github.com/RatesEngine/rates-engine/internal/consumer"
)

// fakeContractCallDecoder is the parallel of fakeOpDecoder /
// fakeDecoder for ContractCallDecoder. Lets us drive
// dispatchContractCall + RouteContractCall directly.
type fakeContractCallDecoder struct {
	name        string
	matchCID    string
	matchFn     string
	outputs     []consumer.Event
	decodeErr   error
	matchCount  int
	decodeCount int
}

func (d *fakeContractCallDecoder) Name() string { return d.name }

func (d *fakeContractCallDecoder) Matches(contractID, fn string) bool {
	d.matchCount++
	return contractID == d.matchCID && fn == d.matchFn
}

func (d *fakeContractCallDecoder) Decode(_ ContractCallContext) ([]consumer.Event, error) {
	d.decodeCount++
	if d.decodeErr != nil {
		return nil, d.decodeErr
	}
	return d.outputs, nil
}

func TestRouteContractCall_routesToFirstMatch(t *testing.T) {
	band := &fakeContractCallDecoder{
		name:     "band",
		matchCID: "CBAND",
		matchFn:  "relay",
		outputs:  []consumer.Event{fakeEvent{source: "band", kind: "update"}},
	}
	otherCC := &fakeContractCallDecoder{
		name:     "other",
		matchCID: "COTHER",
		matchFn:  "noop",
	}
	d := New()
	d.AddContractCallDecoder(band)
	d.AddContractCallDecoder(otherCC)

	out, err := d.RouteContractCall(ContractCallContext{
		Ledger:       1,
		ClosedAt:     time.Unix(1_770_000_000, 0).UTC(),
		ContractID:   "CBAND",
		FunctionName: "relay",
	})
	if err != nil {
		t.Fatalf("RouteContractCall: %v", err)
	}
	if len(out) != 1 {
		t.Fatalf("got %d events, want 1", len(out))
	}
	if band.decodeCount != 1 {
		t.Errorf("band Decode count = %d, want 1", band.decodeCount)
	}
	// Other decoder must not have decoded — first-match short-
	// circuit.
	if otherCC.decodeCount != 0 {
		t.Errorf("otherCC Decode count = %d, want 0 (first-match short-circuit)",
			otherCC.decodeCount)
	}
}

func TestRouteContractCall_unmatched_returnsNilNil(t *testing.T) {
	band := &fakeContractCallDecoder{
		name:     "band",
		matchCID: "CBAND",
		matchFn:  "relay",
	}
	d := New()
	d.AddContractCallDecoder(band)

	out, err := d.RouteContractCall(ContractCallContext{
		ContractID:   "CDIFFERENT",
		FunctionName: "relay",
	})
	if err != nil {
		t.Fatalf("RouteContractCall: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d events, want 0 (no decoder matched)", len(out))
	}
	// Stats must NOT increment unmatched-hits — that counter is
	// for events, not contract calls. ContractCallDecoder calls
	// fall through silently when nothing claims them; the
	// dispatcher's metric distinction is intentional.
	if d.Stats().UnmatchedHits != 0 {
		t.Errorf("UnmatchedHits = %d, want 0 (CC misses don't bump event-side counter)",
			d.Stats().UnmatchedHits)
	}
}

func TestRouteContractCall_decodeErrorIsCounted(t *testing.T) {
	boom := errors.New("decoder failed")
	band := &fakeContractCallDecoder{
		name:      "band",
		matchCID:  "CBAND",
		matchFn:   "relay",
		decodeErr: boom,
	}
	d := New()
	d.AddContractCallDecoder(band)

	_, err := d.RouteContractCall(ContractCallContext{
		ContractID:   "CBAND",
		FunctionName: "relay",
	})
	if !errors.Is(err, boom) {
		t.Errorf("error = %v, want %v wrapped", err, boom)
	}
	if got := d.Stats().DecodeErrors["band"]; got != 1 {
		t.Errorf("DecodeErrors[band] = %d, want 1", got)
	}
}

func TestRouteContractCall_emptyDecoderListNoMatch(t *testing.T) {
	d := New()
	out, err := d.RouteContractCall(ContractCallContext{
		ContractID:   "CBAND",
		FunctionName: "relay",
	})
	if err != nil {
		t.Fatalf("RouteContractCall: %v", err)
	}
	if len(out) != 0 {
		t.Errorf("got %d events, want 0", len(out))
	}
}
