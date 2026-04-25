package dispatcher

import (
	"encoding/base64"
	"strings"
	"testing"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/RatesEngine/rates-engine/internal/consumer"
)

// ─── strkey conversion helpers ─────────────────────────────────

func TestContractIDToStrkey(t *testing.T) {
	// 32-byte contract ID → 56-character C-strkey.
	var cid xdr.ContractId
	for i := range cid {
		cid[i] = byte(i)
	}
	got, err := contractIDToStrkey(cid)
	if err != nil {
		t.Fatalf("contractIDToStrkey: %v", err)
	}
	if len(got) != 56 || got[0] != 'C' {
		t.Errorf("got %q, want 56-char C-prefix strkey", got)
	}
	// Round-trip via the SDK to confirm the encoding matches what
	// the SDK expects to decode back.
	raw, err := strkey.Decode(strkey.VersionByteContract, got)
	if err != nil {
		t.Fatalf("SDK strkey.Decode failed: %v", err)
	}
	for i := range cid {
		if raw[i] != cid[i] {
			t.Errorf("byte %d: got %02x want %02x", i, raw[i], cid[i])
		}
	}
}

func TestAccountIDToStrkey_ed25519(t *testing.T) {
	var pub xdr.Uint256
	for i := range pub {
		pub[i] = byte(0xA0 + i%16)
	}
	aid := xdr.AccountId{
		Type:    xdr.PublicKeyTypePublicKeyTypeEd25519,
		Ed25519: &pub,
	}
	got, err := accountIDToStrkey(aid)
	if err != nil {
		t.Fatalf("accountIDToStrkey: %v", err)
	}
	if len(got) != 56 || got[0] != 'G' {
		t.Errorf("got %q, want 56-char G-prefix strkey", got)
	}
}

func TestAccountIDToStrkey_unsupportedType(t *testing.T) {
	// A zero-valued AccountId has Type == 0 but xdr.PublicKeyTypePublicKeyTypeEd25519
	// is also 0 in the upstream enum, so synthesise a junk type — the
	// guard checks for "anything other than Ed25519" and reports it.
	aid := xdr.AccountId{Type: xdr.PublicKeyType(99)}
	_, err := accountIDToStrkey(aid)
	if err == nil {
		t.Error("expected error on unsupported account type, got nil")
	}
	if !strings.Contains(err.Error(), "unsupported account type") {
		t.Errorf("error message %q missing the expected fragment", err.Error())
	}
}

// ─── mustParseRFC3339 ──────────────────────────────────────────

func TestMustParseRFC3339_happy(t *testing.T) {
	got := mustParseRFC3339("2026-04-25T12:00:00Z")
	want := time.Date(2026, 4, 25, 12, 0, 0, 0, time.UTC)
	if !got.Equal(want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMustParseRFC3339_panicOnGarbage(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic on malformed input, got none")
		}
	}()
	_ = mustParseRFC3339("not-a-timestamp")
}

// ─── extractInvokeContractCalls ────────────────────────────────

func TestExtractInvokeContractCalls_emptyOps(t *testing.T) {
	got := extractInvokeContractCalls(nil)
	if got != nil {
		t.Errorf("got %v, want nil for empty ops", got)
	}
}

func TestExtractInvokeContractCalls_skipsNonInvokeOps(t *testing.T) {
	// A Payment op is classic, not Soroban — slot must be nil.
	ops := []xdr.Operation{
		{Body: xdr.OperationBody{Type: xdr.OperationTypePayment}},
	}
	got := extractInvokeContractCalls(ops)
	if len(got) != 1 {
		t.Fatalf("got %d slots, want 1", len(got))
	}
	if got[0] != nil {
		t.Errorf("expected nil slot for non-Invoke op, got %+v", got[0])
	}
}

func TestExtractInvokeContractCalls_invokeContract(t *testing.T) {
	// Build an InvokeHostFunction op invoking a contract with one
	// Symbol argument. The helper should extract C-strkey contract
	// id, function name "relay", and one base64-encoded arg.
	var cid xdr.ContractId
	for i := range cid {
		cid[i] = byte(i + 1)
	}
	addr := xdr.ScAddress{
		Type:       xdr.ScAddressTypeScAddressTypeContract,
		ContractId: &cid,
	}
	sym := xdr.ScSymbol("hello")
	arg := xdr.ScVal{Type: xdr.ScValTypeScvSymbol, Sym: &sym}
	ic := xdr.InvokeContractArgs{
		ContractAddress: addr,
		FunctionName:    xdr.ScSymbol("relay"),
		Args:            []xdr.ScVal{arg},
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
	if got[0] == nil {
		t.Fatal("slot 0 nil — should have been populated")
	}
	if got[0].FunctionName != "relay" {
		t.Errorf("FunctionName = %q, want \"relay\"", got[0].FunctionName)
	}
	if got[0].ContractID[0] != 'C' || len(got[0].ContractID) != 56 {
		t.Errorf("ContractID = %q, want 56-char C-strkey", got[0].ContractID)
	}
	if len(got[0].Args) != 1 {
		t.Fatalf("got %d args, want 1", len(got[0].Args))
	}
	// Round-trip the arg through base64 + xdr to confirm we encoded
	// the original ScVal.
	raw, err := base64.StdEncoding.DecodeString(got[0].Args[0])
	if err != nil {
		t.Fatalf("arg base64 decode: %v", err)
	}
	var decoded xdr.ScVal
	if err := decoded.UnmarshalBinary(raw); err != nil {
		t.Fatalf("arg XDR decode: %v", err)
	}
	if decoded.Type != xdr.ScValTypeScvSymbol || string(*decoded.Sym) != "hello" {
		t.Errorf("decoded arg = %+v, want symbol \"hello\"", decoded)
	}
}

// ─── AddContractCallDecoder ────────────────────────────────────

// fakeCCDecoder implements ContractCallDecoder for the registration
// test. Matches signature: (contractID, functionName string) -> bool
// per the package's contract; Decode signature takes a single
// ContractCallContext.
type fakeCCDecoder struct{ name string }

func (f *fakeCCDecoder) Name() string             { return f.name }
func (f *fakeCCDecoder) Matches(_, _ string) bool { return false }
func (f *fakeCCDecoder) Decode(_ ContractCallContext) ([]consumer.Event, error) {
	return nil, nil
}

func TestAddContractCallDecoder_appendsAndPreservesOrder(t *testing.T) {
	d := New(nil)
	d.AddContractCallDecoder(&fakeCCDecoder{name: "alpha"})
	d.AddContractCallDecoder(&fakeCCDecoder{name: "beta"})
	if len(d.contractCallDecoders) != 2 {
		t.Fatalf("got %d decoders, want 2", len(d.contractCallDecoders))
	}
	if d.contractCallDecoders[0].Name() != "alpha" || d.contractCallDecoders[1].Name() != "beta" {
		t.Errorf("registration order not preserved: got [%s, %s]",
			d.contractCallDecoders[0].Name(), d.contractCallDecoders[1].Name())
	}
}
