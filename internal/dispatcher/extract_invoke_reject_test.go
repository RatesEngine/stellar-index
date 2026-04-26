package dispatcher

import (
	"testing"

	"github.com/stellar/go-stellar-sdk/xdr"
)

// extractInvokeContractCalls must produce a nil slot for any
// InvokeHostFunction op whose host function isn't an InvokeContract.
// Soroban tx envelopes can also carry CreateContract and
// UploadContractWasm — these create / upload code rather than calling
// existing contracts and have no (contract, function, args) tuple to
// surface, so the dispatcher's downstream consumers must see nil.

func TestExtractInvokeContractCalls_uploadContractWasmReturnsNilSlot(t *testing.T) {
	wasm := []byte{0xAA, 0xBB, 0xCC}
	hf := xdr.HostFunction{
		Type: xdr.HostFunctionTypeHostFunctionTypeUploadContractWasm,
		Wasm: &wasm,
	}
	op := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: hf,
			},
		},
	}
	got := extractInvokeContractCalls([]xdr.Operation{op})
	if len(got) != 1 {
		t.Fatalf("got %d slots, want 1", len(got))
	}
	if got[0] != nil {
		t.Errorf("UploadContractWasm slot should be nil, got %+v", got[0])
	}
}

func TestExtractInvokeContractCalls_invokeAccountAddressSkipped(t *testing.T) {
	// InvokeContract with an Account address is invalid at the
	// protocol level; the helper defensively yields a nil slot
	// rather than emitting a malformed strkey.
	var pub xdr.Uint256
	for i := range pub {
		pub[i] = byte(i)
	}
	aid := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &pub,
	}
	addr := xdr.ScAddress{
		Type:      xdr.ScAddressTypeScAddressTypeAccount,
		AccountId: &aid,
	}
	ic := xdr.InvokeContractArgs{
		ContractAddress: addr,
		FunctionName:    xdr.ScSymbol("relay"),
	}
	hf := xdr.HostFunction{
		Type:           xdr.HostFunctionTypeHostFunctionTypeInvokeContract,
		InvokeContract: &ic,
	}
	op := xdr.Operation{
		Body: xdr.OperationBody{
			Type: xdr.OperationTypeInvokeHostFunction,
			InvokeHostFunctionOp: &xdr.InvokeHostFunctionOp{
				HostFunction: hf,
			},
		},
	}
	got := extractInvokeContractCalls([]xdr.Operation{op})
	if len(got) != 1 {
		t.Fatalf("got %d slots, want 1", len(got))
	}
	if got[0] != nil {
		t.Errorf("InvokeContract against Account address should yield nil slot, got %+v", got[0])
	}
}
