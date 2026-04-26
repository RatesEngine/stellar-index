package dispatcher

import (
	"encoding/base64"
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// contractEventToEventsEvent converts an xdr.ContractEvent into our
// events.Event wire shape. Used only by ProcessLedger when walking
// transaction meta. Pin every reject path so a malformed event
// can't sneak through and mis-attribute the contract id or topic
// bytes downstream.

func makeBasicContractEvent(t *testing.T) (xdr.ContractEvent, xdr.ContractId) {
	t.Helper()
	var cid xdr.ContractId
	for i := range cid {
		cid[i] = byte(i ^ 0x42)
	}
	sym := xdr.ScSymbol("REFLECTOR")
	topic := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	uval := xdr.Uint32(42)
	val := xdr.ScVal{Type: xdr.ScValTypeScvU32, U32: &uval}
	return xdr.ContractEvent{
		Type:       xdr.ContractEventTypeContract,
		ContractId: &cid,
		Body: xdr.ContractEventBody{
			V: 0,
			V0: &xdr.ContractEventV0{
				Topics: []xdr.ScVal{topic},
				Data:   val,
			},
		},
	}, cid
}

func TestContractEventToEventsEvent_happyPath(t *testing.T) {
	ce, cid := makeBasicContractEvent(t)

	got := contractEventToEventsEvent(ce, 52_000_000, "abcd", 3, "2026-04-23T12:00:00Z", []string{"arg-b64"})
	if got == nil {
		t.Fatal("expected non-nil events.Event")
	}
	if got.Type != "contract" {
		t.Errorf("Type = %q, want \"contract\"", got.Type)
	}
	if got.Ledger != 52_000_000 {
		t.Errorf("Ledger = %d, want 52_000_000", got.Ledger)
	}
	if got.OperationIndex != 3 {
		t.Errorf("OperationIndex = %d, want 3", got.OperationIndex)
	}
	if got.TxHash != "abcd" {
		t.Errorf("TxHash = %q, want \"abcd\"", got.TxHash)
	}
	if got.LedgerClosedAt != "2026-04-23T12:00:00Z" {
		t.Errorf("LedgerClosedAt = %q", got.LedgerClosedAt)
	}
	// ContractID should round-trip through strkey — 56-char C-strkey.
	if len(got.ContractID) != 56 || got.ContractID[0] != 'C' {
		t.Errorf("ContractID = %q, want 56-char C-strkey", got.ContractID)
	}
	if !got.InSuccessfulContractCall {
		t.Error("InSuccessfulContractCall = false, want true")
	}
	if len(got.Topic) != 1 {
		t.Errorf("got %d topics, want 1", len(got.Topic))
	}
	// Topic[0] should be the base64-encoded SCVal. Verify by
	// decoding back.
	raw, err := base64.StdEncoding.DecodeString(got.Topic[0])
	if err != nil {
		t.Fatalf("topic base64: %v", err)
	}
	var sv xdr.ScVal
	if err := sv.UnmarshalBinary(raw); err != nil {
		t.Fatalf("topic xdr decode: %v", err)
	}
	if sv.Type != xdr.ScValTypeScvSymbol || string(*sv.Sym) != "REFLECTOR" {
		t.Errorf("topic decode: got %+v", sv)
	}
	// OpArgs threaded through verbatim.
	if len(got.OpArgs) != 1 || got.OpArgs[0] != "arg-b64" {
		t.Errorf("OpArgs = %v, want [arg-b64]", got.OpArgs)
	}
	_ = cid // keep referenced
}

func TestContractEventToEventsEvent_wrongType(t *testing.T) {
	// System and diagnostic events must be rejected — only Type=Contract
	// produces a wire event.
	ce, _ := makeBasicContractEvent(t)
	ce.Type = xdr.ContractEventTypeSystem
	if got := contractEventToEventsEvent(ce, 1, "abcd", 0, "ts", nil); got != nil {
		t.Errorf("ContractEventTypeSystem should return nil, got %+v", got)
	}
}

func TestContractEventToEventsEvent_nilContractID(t *testing.T) {
	ce, _ := makeBasicContractEvent(t)
	ce.ContractId = nil
	if got := contractEventToEventsEvent(ce, 1, "abcd", 0, "ts", nil); got != nil {
		t.Errorf("nil ContractId should return nil, got %+v", got)
	}
}

func TestContractEventToEventsEvent_unknownBodyVersion(t *testing.T) {
	// Body version 1+ is a future protocol bump we haven't audited;
	// the function must reject rather than silently emit a
	// half-decoded event.
	ce, _ := makeBasicContractEvent(t)
	ce.Body.V = 99
	ce.Body.V0 = nil
	if got := contractEventToEventsEvent(ce, 1, "abcd", 0, "ts", nil); got != nil {
		t.Errorf("unknown body version should return nil, got %+v", got)
	}
}
