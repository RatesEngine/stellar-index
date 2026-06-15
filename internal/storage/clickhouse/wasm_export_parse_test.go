package clickhouse

import (
	_ "embed"
	"testing"
)

// contractRegisterWasm is a real Soroban contract module pulled from the r1
// lake (contract CAP6ZT7JC3ZCNELT4I7OJ6IBACRGRN2CWS5GBPCPYRLF3RLOTX33FAF6,
// wasm hash f89eb3cc…). Used as a golden fixture for the native export parser.
//
//go:embed testdata/contract_register.wasm
var contractRegisterWasm []byte

func TestParseWasmExports_RealContract(t *testing.T) {
	exports, err := parseWasmExports(contractRegisterWasm)
	if err != nil {
		t.Fatalf("parseWasmExports: %v", err)
	}

	// The module exports a memory + two globals + five FUNCTIONS. Only the
	// functions are surfaced (the explorer wants the callable API). These are
	// the contract's real Soroban entry points.
	want := map[string]bool{
		"get": true, "initialize": true, "register": true, "revoke": true, "_": true,
	}
	got := map[string]WasmExport{}
	for _, e := range exports {
		got[e.Name] = e
	}
	if len(got) != len(want) {
		names := make([]string, 0, len(got))
		for n := range got {
			names = append(names, n)
		}
		t.Fatalf("export count = %d %v, want %d %v", len(got), names, len(want), keys(want))
	}
	for n := range want {
		ex, ok := got[n]
		if !ok {
			t.Errorf("missing export %q", n)
			continue
		}
		// Every Soroban entry point resolved to a real signature (non-nil
		// param/result lists prove the type-section walk + import-offset math
		// chased the index correctly). A contract entry point returns the
		// host-value i64; "register"/"revoke" take args.
		if ex.Results == nil {
			t.Errorf("export %q: nil results — index resolution failed", n)
		}
		for _, vt := range append(append([]string{}, ex.Params...), ex.Results...) {
			switch vt {
			case "i32", "i64", "f32", "f64":
			default:
				t.Errorf("export %q: unexpected value type %q", n, vt)
			}
		}
	}

	// Spot-check a known signature: a Soroban contract function returns one
	// host value (i64).
	if reg := got["register"]; len(reg.Results) != 1 || reg.Results[0] != "i64" {
		t.Errorf("register results = %v, want [i64]", reg.Results)
	}
}

func TestParseWasmExports_BadMagic(t *testing.T) {
	if _, err := parseWasmExports([]byte("not wasm at all")); err == nil {
		t.Fatal("expected error on non-wasm input")
	}
	if _, err := parseWasmExports(nil); err == nil {
		t.Fatal("expected error on nil input")
	}
}

func TestParseWasmExports_MinimalModule(t *testing.T) {
	// A bare valid module header (magic + version) with no sections: valid,
	// zero exported functions.
	header := []byte{0x00, 0x61, 0x73, 0x6d, 0x01, 0x00, 0x00, 0x00}
	exports, err := parseWasmExports(header)
	if err != nil {
		t.Fatalf("parseWasmExports(minimal): %v", err)
	}
	if len(exports) != 0 {
		t.Fatalf("minimal module exports = %d, want 0", len(exports))
	}
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
