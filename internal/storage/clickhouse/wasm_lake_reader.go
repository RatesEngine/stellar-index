package clickhouse

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/stellar/go-stellar-sdk/strkey"
	"github.com/stellar/go-stellar-sdk/xdr"
)

// ErrContractWasmUnresolved is returned by ContractWasm when the contract's
// wasm could not be assembled from the lake — either the contract's
// contract_data INSTANCE entry isn't captured (so we can't learn its wasm
// hash) or the referenced contract_code entry isn't captured (so we have the
// hash but not the bytes). It's a clean "not found" (404), NOT an error: the
// lake's ledger_entry_changes capture is live-only (historical contract_code /
// instance entries created at deploy-time, years ago, are mostly outside the
// captured window — see extract.go's G12-03 note). Callers map this to 404.
var ErrContractWasmUnresolved = errors.New("clickhouse: contract wasm not resolvable from lake")

// ErrContractIsSAC is returned by ContractWasm when the contract's instance
// IS captured but its executable is a Stellar Asset Contract (the built-in
// SAC host logic), not a user-uploaded WASM module. SACs — the asset
// contracts behind `native`, USDC, and every classic asset, which are among
// the busiest contracts on the network — have no WASM to show, ever; a
// backfill will never produce one. Distinct from ErrContractWasmUnresolved
// so the API/UI can say "this is a SAC, no WASM" instead of "not captured
// yet" (audit 2026-06-19 item 13). Callers map this to a 404 with a SAC note.
var ErrContractIsSAC = errors.New("clickhouse: contract is a stellar asset contract (no wasm)")

// WasmExport is one exported function of a Soroban contract — its name and
// the i32/i64/f32/f64 param + result value types parsed from the wasm type
// section. For a Soroban contract the exported function names are the
// contract's public entry points (e.g. "register", "swap", "deposit"); the
// param/result types are the low-level wasm ABI (i64-tagged host values), not
// the Rust-level signature, but the NAMES are the contract's real API surface.
type WasmExport struct {
	Name    string   // exported symbol
	Params  []string // wasm value types: "i32"|"i64"|"f32"|"f64"
	Results []string // wasm value types
}

// ContractWasmInfo is the assembled per-contract wasm view: the resolved hash,
// the byte size, the natively-parsed export table, and (best-effort) the WAT
// disassembly + wasm-decompile pseudocode. Wat/Decompiled are empty when the
// wabt tooling (wasm2wat / wasm-decompile) isn't on PATH — the metadata +
// exports are always populated (pure-Go, no tool dependency).
type ContractWasmInfo struct {
	ContractID string
	WasmHash   string // hex sha256 of the wasm module
	SizeBytes  int
	Exports    []WasmExport
	Wat        string // WAT disassembly; empty if wasm2wat unavailable
	Decompiled string // wasm-decompile pseudocode; empty if unavailable
	ToolNote   string // human note on which optional stages ran / why they didn't
}

// ContractWasm resolves a contract id to its on-chain wasm and returns the
// assembled metadata view. Resolution is a two-hop walk over the certified
// lake's ledger_entry_changes (ADR-0034 substrate):
//
//  1. contract id → wasm hash: find the contract's contract_data INSTANCE
//     entry (ScvLedgerKeyContractInstance) and read its
//     executable.wasm_hash.
//  2. wasm hash → bytes: find the contract_code entry with that hash and read
//     ContractCodeEntry.code (the raw wasm module).
//
// The export table is parsed natively (pure Go, no tooling). WAT + decompile
// are filled best-effort by buildWasmDisassembly (wabt binaries if present).
//
// Returns ErrContractWasmUnresolved (a clean 404) when either hop misses in
// the captured window — historical deploy-time entries are largely outside the
// live ledger_entry_changes capture (extract.go G12-03 note).
func (r *ExplorerReader) ContractWasm(ctx context.Context, contractID string) (ContractWasmInfo, error) {
	dec, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		return ContractWasmInfo{}, fmt.Errorf("clickhouse: bad contract id %q: %w", contractID, err)
	}
	var cidHash xdr.Hash
	copy(cidHash[:], dec)

	wasmHash, ok, err := r.contractWasmHash(ctx, cidHash)
	if err != nil {
		return ContractWasmInfo{}, err
	}
	if !ok {
		return ContractWasmInfo{}, ErrContractWasmUnresolved
	}

	code, ok, err := r.wasmCodeByHash(ctx, wasmHash)
	if err != nil {
		return ContractWasmInfo{}, err
	}
	if !ok {
		return ContractWasmInfo{}, ErrContractWasmUnresolved
	}

	exports, perr := parseWasmExports(code)
	info := ContractWasmInfo{
		ContractID: contractID,
		WasmHash:   hex.EncodeToString(wasmHash[:]),
		SizeBytes:  len(code),
		Exports:    exports,
	}
	if perr != nil {
		// A parse miss is non-fatal: still serve the resolved hash + size.
		info.ToolNote = "export parse: " + perr.Error() + "; "
	}
	buildWasmDisassembly(ctx, &info, code)
	return info, nil
}

// contractWasmHash finds the contract's contract_data INSTANCE entry and reads
// its executable wasm hash. ok=false when no instance entry for this contract
// is in the captured window.
//
// The query is pinned to the contract's INSTANCE ledger key, computed
// deterministically (instanceKeyXDR): the key for the
// ScvLedgerKeyContractInstance entry of a given contract is a single, fixed
// base64 LedgerKey, so matching on key_xdr turns what would be a full decode of
// every contract_data row (millions — and slow to exhaust on a miss) into a
// precise equality predicate. Rows are ordered newest-first so the current
// executable wins under in-place contract upgrades; the per-contract result is
// cached hard (the wasm for a hash is immutable).
func (r *ExplorerReader) contractWasmHash(ctx context.Context, cid xdr.Hash) (xdr.Hash, bool, error) {
	keys, err := instanceKeyXDR(cid)
	if err != nil {
		return xdr.Hash{}, false, err
	}
	const q = `SELECT entry_xdr FROM stellar.ledger_entry_changes
		WHERE entry_type = 'contract_data' AND key_xdr IN (?) AND entry_xdr != ''
		ORDER BY ledger_seq DESC, ingested_at DESC`
	rows, err := r.conn.Query(ctx, q, keys)
	if err != nil {
		return xdr.Hash{}, false, fmt.Errorf("clickhouse: contract_data scan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var b64 string
		if err := rows.Scan(&b64); err != nil {
			return xdr.Hash{}, false, fmt.Errorf("clickhouse: scan contract_data: %w", err)
		}
		var entry xdr.LedgerEntry
		if xdr.SafeUnmarshalBase64(b64, &entry) != nil {
			continue
		}
		cd, ok := entry.Data.GetContractData()
		if !ok || cd.Key.Type != xdr.ScValTypeScvLedgerKeyContractInstance {
			continue
		}
		inst, ok := cd.Val.GetInstance()
		if !ok {
			continue
		}
		switch inst.Executable.Type {
		case xdr.ContractExecutableTypeContractExecutableWasm:
			if inst.Executable.WasmHash != nil {
				return *inst.Executable.WasmHash, true, rows.Err()
			}
		case xdr.ContractExecutableTypeContractExecutableStellarAsset:
			// Instance IS captured, but it's a SAC — no WASM to resolve,
			// ever. Newest-first ordering means this is the current
			// executable, so report it distinctly rather than falling
			// through to the generic "unresolved" 404.
			return xdr.Hash{}, false, ErrContractIsSAC
		}
	}
	return xdr.Hash{}, false, rows.Err()
}

// ContractCodeVersion is one entry in a contract's code-upgrade timeline: the
// ledger at which the contract's instance began pointing at WasmHash.
type ContractCodeVersion struct {
	Ledger    uint32
	CloseTime time.Time
	WasmHash  string
}

// ContractCodeHistory returns a contract's WASM-hash timeline — the contract's
// "change over time" (ADR-0038 Phase C): every distinct executable the
// contract instance has pointed at, in chronological order, so an in-place
// `update_contract` upgrade surfaces as a new version. Reads the instance
// contract_data entry's executable across all captured changes and collapses
// consecutive identical hashes. Coverage = the captured entry-change window
// (same substrate as the "see the code" view); fills back with the Phase-C
// backfill. Empty (not an error) when the contract's instance isn't captured.
func (r *ExplorerReader) ContractCodeHistory(ctx context.Context, contractID string) ([]ContractCodeVersion, error) {
	dec, err := strkey.Decode(strkey.VersionByteContract, contractID)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: bad contract id %q: %w", contractID, err)
	}
	var cidHash xdr.Hash
	copy(cidHash[:], dec)
	keys, err := instanceKeyXDR(cidHash)
	if err != nil {
		return nil, err
	}

	const q = `SELECT ledger_seq, close_time, entry_xdr FROM stellar.ledger_entry_changes
		WHERE entry_type = 'contract_data' AND key_xdr IN (?) AND entry_xdr != ''
		ORDER BY ledger_seq ASC, change_index ASC, ingested_at ASC`
	rows, err := r.conn.Query(ctx, q, keys)
	if err != nil {
		return nil, fmt.Errorf("clickhouse: contract code history: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var out []ContractCodeVersion
	var lastHash string
	for rows.Next() {
		var (
			seq       uint32
			closeTime time.Time
			b64       string
		)
		if err := rows.Scan(&seq, &closeTime, &b64); err != nil {
			return nil, fmt.Errorf("clickhouse: scan code history: %w", err)
		}
		var entry xdr.LedgerEntry
		if xdr.SafeUnmarshalBase64(b64, &entry) != nil {
			continue
		}
		cd, ok := entry.Data.GetContractData()
		if !ok || cd.Key.Type != xdr.ScValTypeScvLedgerKeyContractInstance {
			continue
		}
		inst, ok := cd.Val.GetInstance()
		if !ok || inst.Executable.Type != xdr.ContractExecutableTypeContractExecutableWasm ||
			inst.Executable.WasmHash == nil {
			continue
		}
		h := hex.EncodeToString(inst.Executable.WasmHash[:])
		if h == lastHash {
			continue // unchanged executable — not an upgrade
		}
		lastHash = h
		out = append(out, ContractCodeVersion{Ledger: seq, CloseTime: closeTime, WasmHash: h})
	}
	return out, rows.Err()
}

// instanceKeyXDR returns the base64 LedgerKey(s) for a contract's
// ScvLedgerKeyContractInstance contract_data entry — one per durability
// (persistent + temporary), since the lake stores the key XDR verbatim and the
// durability is part of it. An instance entry is always persistent in practice,
// but querying both keeps the match exact without that assumption.
func instanceKeyXDR(cid xdr.Hash) ([]string, error) {
	contractID := xdr.ContractId(cid)
	durabilities := []xdr.ContractDataDurability{
		xdr.ContractDataDurabilityPersistent,
		xdr.ContractDataDurabilityTemporary,
	}
	out := make([]string, 0, len(durabilities))
	for _, d := range durabilities {
		key := xdr.LedgerKey{
			Type: xdr.LedgerEntryTypeContractData,
			ContractData: &xdr.LedgerKeyContractData{
				Contract:   xdr.ScAddress{Type: xdr.ScAddressTypeScAddressTypeContract, ContractId: &contractID},
				Key:        xdr.ScVal{Type: xdr.ScValTypeScvLedgerKeyContractInstance},
				Durability: d,
			},
		}
		b64, err := xdr.MarshalBase64(key)
		if err != nil {
			return nil, fmt.Errorf("clickhouse: marshal instance key: %w", err)
		}
		out = append(out, b64)
	}
	return out, nil
}

// wasmCodeByHash returns the raw wasm bytes for a code hash from the
// contract_code entries, or ok=false when that hash isn't captured.
func (r *ExplorerReader) wasmCodeByHash(ctx context.Context, hash xdr.Hash) ([]byte, bool, error) {
	const q = `SELECT entry_xdr FROM stellar.ledger_entry_changes
		WHERE entry_type = 'contract_code' AND entry_xdr != ''`
	rows, err := r.conn.Query(ctx, q)
	if err != nil {
		return nil, false, fmt.Errorf("clickhouse: contract_code scan: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var b64 string
		if err := rows.Scan(&b64); err != nil {
			return nil, false, fmt.Errorf("clickhouse: scan contract_code: %w", err)
		}
		var entry xdr.LedgerEntry
		if xdr.SafeUnmarshalBase64(b64, &entry) != nil {
			continue
		}
		cc, ok := entry.Data.GetContractCode()
		if !ok || cc.Hash != hash {
			continue
		}
		return []byte(cc.Code), true, rows.Err()
	}
	return nil, false, rows.Err()
}
