package stellarrpc

import (
	"strings"
	"testing"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// makeAccountStrkey produces a valid G-strkey from a 32-byte seed.
func makeAccountStrkey(t *testing.T, seedByte byte) string {
	t.Helper()
	var raw [32]byte
	for i := range raw {
		raw[i] = seedByte ^ byte(i)
	}
	s, err := strkey.Encode(strkey.VersionByteAccountID, raw[:])
	if err != nil {
		t.Fatalf("strkey encode account: %v", err)
	}
	return s
}

// makeContractStrkey produces a valid C-strkey from a 32-byte seed.
func makeContractStrkey(t *testing.T, seedByte byte) string {
	t.Helper()
	var raw [32]byte
	for i := range raw {
		raw[i] = seedByte ^ byte(i)
	}
	s, err := strkey.Encode(strkey.VersionByteContract, raw[:])
	if err != nil {
		t.Fatalf("strkey encode contract: %v", err)
	}
	return s
}

func TestParseSource_validG(t *testing.T) {
	g := makeAccountStrkey(t, 0x10)
	muxed, err := parseSource(g)
	if err != nil {
		t.Fatalf("parseSource: %v", err)
	}
	if muxed.Type != xdr.CryptoKeyTypeKeyTypeEd25519 {
		t.Errorf("muxed.Type = %d, want Ed25519", muxed.Type)
	}
	if muxed.Ed25519 == nil {
		t.Fatal("muxed.Ed25519 is nil")
	}
}

func TestParseSource_invalidStrkey(t *testing.T) {
	_, err := parseSource("definitely-not-a-strkey")
	if err == nil {
		t.Error("expected error on invalid strkey, got nil")
	}
}

func TestParseContractAddress_validC(t *testing.T) {
	c := makeContractStrkey(t, 0x42)
	addr, err := parseContractAddress(c)
	if err != nil {
		t.Fatalf("parseContractAddress: %v", err)
	}
	if addr.Type != xdr.ScAddressTypeScAddressTypeContract {
		t.Errorf("addr.Type = %d, want Contract", addr.Type)
	}
	if addr.ContractId == nil {
		t.Fatal("addr.ContractId is nil")
	}
}

func TestParseContractAddress_invalidStrkey(t *testing.T) {
	_, err := parseContractAddress("not-a-c-strkey")
	if err == nil {
		t.Error("expected error on invalid strkey, got nil")
	}
}

func TestInvokeContractTxEnvelope_buildsBase64Envelope(t *testing.T) {
	source := makeAccountStrkey(t, 0x01)
	contract := makeContractStrkey(t, 0x42)

	b64, err := InvokeContractTxEnvelope(source, contract, "all_pairs_length", nil)
	if err != nil {
		t.Fatalf("InvokeContractTxEnvelope: %v", err)
	}
	if b64 == "" {
		t.Fatal("expected non-empty envelope")
	}

	// Round-trip the envelope through xdr to confirm structure.
	var env xdr.TransactionEnvelope
	if err := xdr.SafeUnmarshalBase64(b64, &env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if env.Type != xdr.EnvelopeTypeEnvelopeTypeTx {
		t.Errorf("env.Type = %d, want Tx", env.Type)
	}
	if env.V1 == nil || len(env.V1.Tx.Operations) != 1 {
		t.Fatalf("expected 1 operation, got envelope %+v", env)
	}
	op := env.V1.Tx.Operations[0]
	if op.Body.Type != xdr.OperationTypeInvokeHostFunction {
		t.Errorf("op type = %d, want InvokeHostFunction", op.Body.Type)
	}
	hf := op.Body.MustInvokeHostFunctionOp().HostFunction
	if hf.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
		t.Errorf("host function type = %d, want InvokeContract", hf.Type)
	}
	ic := hf.MustInvokeContract()
	if string(ic.FunctionName) != "all_pairs_length" {
		t.Errorf("function name = %q, want \"all_pairs_length\"", string(ic.FunctionName))
	}
}

func TestInvokeContractTxEnvelope_emptySourceUsesNullAccount(t *testing.T) {
	contract := makeContractStrkey(t, 0x42)
	b64, err := InvokeContractTxEnvelope("", contract, "noop", nil)
	if err != nil {
		t.Fatalf("InvokeContractTxEnvelope: %v", err)
	}
	if b64 == "" {
		t.Fatal("expected non-empty envelope with default null source")
	}
}

func TestInvokeContractTxEnvelope_invalidContractRejected(t *testing.T) {
	source := makeAccountStrkey(t, 0x01)
	_, err := InvokeContractTxEnvelope(source, "not-a-c-strkey", "fn", nil)
	if err == nil {
		t.Error("expected error on invalid contract id, got nil")
	}
	if !strings.Contains(err.Error(), "contract") {
		t.Errorf("error %q missing the \"contract\" fragment", err.Error())
	}
}

func TestInvokeContractTxEnvelope_invalidSourceRejected(t *testing.T) {
	contract := makeContractStrkey(t, 0x42)
	_, err := InvokeContractTxEnvelope("not-a-g-strkey", contract, "fn", nil)
	if err == nil {
		t.Error("expected error on invalid source, got nil")
	}
	if !strings.Contains(err.Error(), "source") {
		t.Errorf("error %q missing the \"source\" fragment", err.Error())
	}
}
