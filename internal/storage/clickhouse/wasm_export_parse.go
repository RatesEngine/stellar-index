package clickhouse

import (
	"encoding/binary"
	"errors"
	"fmt"
)

// wasm_parse.go — a tiny, dependency-free reader for the parts of the
// WebAssembly binary format we surface on the contract-wasm explorer endpoint:
// the export table and (to give each export a signature) the type + function +
// import sections. This is NOT a full validator or disassembler — it walks
// section headers and extracts exactly the four sections needed to answer
// "what functions does this contract export, and what are their wasm ABI
// types?" Everything else is length-skipped. Pure Go, no wabt, no cgo — so the
// metadata + exports layer of the endpoint always works regardless of tooling.
//
// Binary format reference: https://webassembly.github.io/spec/core/binary/

var (
	errBadMagic   = errors.New("not a wasm module (bad magic)")
	errTruncated  = errors.New("truncated wasm module")
	errBadSection = errors.New("malformed wasm section")
)

const (
	secType     = 1
	secImport   = 2
	secFunction = 3
	secExport   = 7
)

// valType maps a wasm value-type byte to its mnemonic. Covers the four numeric
// types Soroban contracts use; anything else (vector/reftypes) renders as a
// hex token so the output is still well-formed.
func valType(b byte) string {
	switch b {
	case 0x7f:
		return "i32"
	case 0x7e:
		return "i64"
	case 0x7d:
		return "f32"
	case 0x7c:
		return "f64"
	default:
		return fmt.Sprintf("0x%02x", b)
	}
}

// funcType is one entry of the type section: param + result value-type lists.
type funcType struct {
	params  []string
	results []string
}

// reader is a cursor over the wasm byte slice with LEB128 + bounds helpers.
type reader struct {
	b []byte
	i int
}

func (r *reader) eof() bool { return r.i >= len(r.b) }

func (r *reader) byte() (byte, error) {
	if r.i >= len(r.b) {
		return 0, errTruncated
	}
	v := r.b[r.i]
	r.i++
	return v, nil
}

// uvarint reads an unsigned LEB128 (used for all wasm u32 counts/lengths).
func (r *reader) uvarint() (uint64, error) {
	var x uint64
	var shift uint
	for {
		b, err := r.byte()
		if err != nil {
			return 0, err
		}
		x |= uint64(b&0x7f) << shift
		if b&0x80 == 0 {
			return x, nil
		}
		shift += 7
		if shift >= 64 {
			return 0, errBadSection
		}
	}
}

// bytes reads n raw bytes.
func (r *reader) bytes(n int) ([]byte, error) {
	if n < 0 || r.i+n > len(r.b) {
		return nil, errTruncated
	}
	out := r.b[r.i : r.i+n]
	r.i += n
	return out, nil
}

// name reads a length-prefixed UTF-8 name.
func (r *reader) name() (string, error) {
	n, err := r.uvarint()
	if err != nil {
		return "", err
	}
	raw, err := r.bytes(int(n))
	if err != nil {
		return "", err
	}
	return string(raw), nil
}

// parseWasmExports walks the module and returns its exported FUNCTIONS with
// resolved param/result types. Exports of memories/tables/globals are omitted
// (the endpoint surfaces the callable API). An empty result with nil error
// means a valid module that exports no functions.
func parseWasmExports(b []byte) ([]WasmExport, error) {
	if len(b) < 8 || binary.LittleEndian.Uint32(b[0:4]) != 0x6d736100 { // "\0asm"
		return nil, errBadMagic
	}
	r := &reader{b: b, i: 8} // skip magic(4) + version(4)

	var types []funcType
	var importedFuncs int      // imported functions occupy the low func-index space
	var funcTypeIdx []uint32   // local function index → type index
	var rawExports []rawExport // collected, resolved after all sections seen

	for !r.eof() {
		id, err := r.byte()
		if err != nil {
			return nil, err
		}
		size, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		body, err := r.bytes(int(size))
		if err != nil {
			return nil, err
		}
		sr := &reader{b: body}
		switch id {
		case secType:
			types, err = parseTypeSection(sr)
		case secImport:
			importedFuncs, err = parseImportSection(sr)
		case secFunction:
			funcTypeIdx, err = parseFunctionSection(sr)
		case secExport:
			rawExports, err = parseExportSection(sr)
		default:
			// length-skipped (body already consumed)
		}
		if err != nil {
			return nil, err
		}
	}

	return resolveExports(rawExports, types, funcTypeIdx, importedFuncs), nil
}

// rawExport is an export-section entry before signature resolution. kind 0 ==
// function; funcIdx is the (import-offset) function index.
type rawExport struct {
	name    string
	kind    byte
	funcIdx uint32
}

func parseTypeSection(r *reader) ([]funcType, error) {
	count, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	out := make([]funcType, 0, count)
	for i := uint64(0); i < count; i++ {
		form, err := r.byte()
		if err != nil {
			return nil, err
		}
		if form != 0x60 { // func type
			return nil, errBadSection
		}
		params, err := readValTypes(r)
		if err != nil {
			return nil, err
		}
		results, err := readValTypes(r)
		if err != nil {
			return nil, err
		}
		out = append(out, funcType{params: params, results: results})
	}
	return out, nil
}

func readValTypes(r *reader) ([]string, error) {
	n, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	out := make([]string, 0, n)
	for i := uint64(0); i < n; i++ {
		b, err := r.byte()
		if err != nil {
			return nil, err
		}
		out = append(out, valType(b))
	}
	return out, nil
}

// parseImportSection returns the count of imported FUNCTIONS — these occupy
// func indices [0, importedFuncs), so an export's local func index is offset by
// this. Non-function imports (table/memory/global) are skipped but still
// length-walked so the section cursor stays aligned.
func parseImportSection(r *reader) (int, error) {
	count, err := r.uvarint()
	if err != nil {
		return 0, err
	}
	funcs := 0
	for i := uint64(0); i < count; i++ {
		if _, err := r.name(); err != nil { // module
			return 0, err
		}
		if _, err := r.name(); err != nil { // field
			return 0, err
		}
		kind, err := r.byte()
		if err != nil {
			return 0, err
		}
		isFunc, err := skipImportDesc(r, kind)
		if err != nil {
			return 0, err
		}
		if isFunc {
			funcs++
		}
	}
	return funcs, nil
}

// skipImportDesc consumes one import's descriptor for the given external-kind
// byte, length-walking non-function imports so the section cursor stays
// aligned. Returns true when the import is a function (it occupies a func
// index, shifting the local-func-index offset).
func skipImportDesc(r *reader, kind byte) (bool, error) {
	switch kind {
	case 0x00: // func: typeidx
		_, err := r.uvarint()
		return err == nil, err
	case 0x01: // table: reftype + limits
		if _, err := r.byte(); err != nil {
			return false, err
		}
		return false, skipLimits(r)
	case 0x02: // memory: limits
		return false, skipLimits(r)
	case 0x03: // global: valtype + mutability
		if _, err := r.byte(); err != nil {
			return false, err
		}
		_, err := r.byte()
		return false, err
	default:
		return false, errBadSection
	}
}

func skipLimits(r *reader) error {
	flag, err := r.byte()
	if err != nil {
		return err
	}
	if _, err := r.uvarint(); err != nil { // min
		return err
	}
	if flag&0x01 != 0 { // has max
		if _, err := r.uvarint(); err != nil {
			return err
		}
	}
	return nil
}

// parseFunctionSection returns local-function-index → type-index.
func parseFunctionSection(r *reader) ([]uint32, error) {
	count, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	out := make([]uint32, 0, count)
	for i := uint64(0); i < count; i++ {
		t, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		out = append(out, uint32(t))
	}
	return out, nil
}

func parseExportSection(r *reader) ([]rawExport, error) {
	count, err := r.uvarint()
	if err != nil {
		return nil, err
	}
	out := make([]rawExport, 0, count)
	for i := uint64(0); i < count; i++ {
		nm, err := r.name()
		if err != nil {
			return nil, err
		}
		kind, err := r.byte()
		if err != nil {
			return nil, err
		}
		idx, err := r.uvarint()
		if err != nil {
			return nil, err
		}
		out = append(out, rawExport{name: nm, kind: kind, funcIdx: uint32(idx)})
	}
	return out, nil
}

// resolveExports turns the raw function exports into typed WasmExports by
// chasing func index → (minus imports) local index → type index → signature.
// An export whose index can't be resolved (e.g. a re-exported import, or an
// out-of-range index from a malformed module) is kept with empty signatures
// rather than dropped — the NAME is the load-bearing field for the explorer.
func resolveExports(raw []rawExport, types []funcType, funcTypeIdx []uint32, importedFuncs int) []WasmExport {
	out := make([]WasmExport, 0, len(raw))
	for _, e := range raw {
		if e.kind != 0x00 { // functions only
			continue
		}
		ex := WasmExport{Name: e.name}
		local := int(e.funcIdx) - importedFuncs
		if local >= 0 && local < len(funcTypeIdx) {
			ti := int(funcTypeIdx[local])
			if ti >= 0 && ti < len(types) {
				ex.Params = types[ti].params
				ex.Results = types[ti].results
			}
		}
		out = append(out, ex)
	}
	return out
}
