package v1

import (
	"errors"
	"net/http"

	"github.com/StellarIndex/stellar-index/internal/canonical"
	"github.com/StellarIndex/stellar-index/internal/storage/clickhouse"
)

// WasmExportView is one exported contract function on the wire: its name and
// the wasm-ABI param/result value types. For a Soroban contract the names are
// the contract's public entry points (e.g. "swap", "deposit"); the types are
// the low-level wasm ABI (i64-tagged host values), not the Rust signature.
type WasmExportView struct {
	Name    string   `json:"name"`
	Params  []string `json:"params"`
	Results []string `json:"results"`
}

// ContractWasmView is the wire response for GET /v1/contracts/{id}/wasm: the
// contract's resolved wasm hash + size, its exported function table (always
// present — parsed natively in Go), and best-effort WAT disassembly +
// wasm-decompile pseudocode (empty when the wabt toolchain isn't installed on
// the server). source_note explains provenance + any degraded stages.
//
// size_bytes is small (< 2^31) so it stays a JSON number; the wasm hash is a
// hex string. There are no i128 fields here, so ADR-0003 string-encoding
// doesn't apply.
type ContractWasmView struct {
	ContractID string           `json:"contract_id"`
	WasmHash   string           `json:"wasm_hash"`
	SizeBytes  int              `json:"size_bytes"`
	Exports    []WasmExportView `json:"exports"`
	Wat        string           `json:"wat,omitempty"`
	Decompiled string           `json:"decompiled,omitempty"`
	SourceNote string           `json:"source_note"`
}

// handleContractWasm serves GET /v1/contracts/{contract_id}/wasm — the
// contract's on-chain WASM surfaced for the explorer's "see the code" view:
// metadata + exported function table (+ WAT + decompiled pseudocode when the
// wabt toolchain is present). Read on demand from the certified lake (ADR-0034);
// the wasm for a hash is immutable, so the response is cached for a day.
//
// 404 when the contract's wasm isn't resolvable from the captured
// ledger_entry_changes window (the instance or code entry wasn't captured —
// most pre-capture deploy-time entries are outside it; see
// clickhouse.ErrContractWasmUnresolved).
func (s *Server) handleContractWasm(w http.ResponseWriter, r *http.Request) {
	if s.explorer == nil {
		s.explorerUnavailable(w, r)
		return
	}
	cid := r.PathValue("contract_id")
	if !canonical.IsContractID(cid) {
		writeProblem(w, r, "https://api.stellarindex.io/errors/invalid-contract-id",
			"Invalid contract id", http.StatusBadRequest,
			"the contract id must be a valid C-strkey")
		return
	}
	info, err := s.explorer.ContractWasm(r.Context(), cid)
	if err != nil {
		if clientAborted(r, err) {
			return
		}
		if errors.Is(err, clickhouse.ErrContractIsSAC) {
			// The instance IS captured — it's a Stellar Asset Contract, which
			// runs built-in host logic and has no user WASM module. A distinct
			// 404 so clients (and the explorer) say "SAC — no WASM" instead of
			// "not captured yet" (which would be misleading: no backfill will
			// ever produce WASM for a SAC).
			writeProblem(w, r, "https://api.stellarindex.io/errors/contract-is-sac",
				"Stellar Asset Contract — no WASM", http.StatusNotFound,
				"this contract is a Stellar Asset Contract (the built-in SAC host "+
					"logic behind a classic asset), not a user-uploaded WASM "+
					"module — there is no WASM bytecode to show")
			return
		}
		if errors.Is(err, clickhouse.ErrContractWasmUnresolved) {
			// The reader couldn't find an instance (its entry predates the
			// capture window). Before reporting a generic miss, check the
			// operator's SAC-wrapper registry: a contract that wraps a
			// classic asset is a SAC with no WASM, ever — so even with the
			// instance uncaptured we can answer definitively rather than
			// implying a backfill will produce WASM.
			if s.isKnownSAC(cid) {
				writeProblem(w, r, "https://api.stellarindex.io/errors/contract-is-sac",
					"Stellar Asset Contract — no WASM", http.StatusNotFound,
					"this contract is a Stellar Asset Contract (the built-in SAC host "+
						"logic behind a classic asset), not a user-uploaded WASM "+
						"module — there is no WASM bytecode to show")
				return
			}
			writeProblem(w, r, "https://api.stellarindex.io/errors/contract-wasm-not-found",
				"Contract WASM not found", http.StatusNotFound,
				"the contract's wasm could not be assembled from the lake — its "+
					"contract-instance or contract-code entry isn't in the captured "+
					"ledger-entry window")
			return
		}
		s.logger.Error("explorer ContractWasm failed", "err", err, "contract", cid)
		writeProblem(w, r, "https://api.stellarindex.io/errors/internal",
			"Internal error", http.StatusInternalServerError, "")
		return
	}

	view := contractWasmView(info)
	// The wasm for a content-addressed hash is immutable — cache hard.
	w.Header().Set("Cache-Control", "public, max-age=86400")
	writeJSON(w, view, Flags{})
}

// contractWasmView maps the reader's ContractWasmInfo to the wire shape,
// composing source_note from the static provenance line + any tool note.
func contractWasmView(info clickhouse.ContractWasmInfo) ContractWasmView {
	exports := make([]WasmExportView, len(info.Exports))
	for i, e := range info.Exports {
		exports[i] = WasmExportView{
			Name:    e.Name,
			Params:  nonNilStrings(e.Params),
			Results: nonNilStrings(e.Results),
		}
	}
	note := "wasm resolved from the certified ClickHouse lake " +
		"(contract instance → wasm hash → contract_code bytes, ADR-0034); " +
		"exports parsed natively; wat/decompiled are best-effort via wabt"
	if info.ToolNote != "" {
		note += " — " + info.ToolNote
	}
	return ContractWasmView{
		ContractID: info.ContractID,
		WasmHash:   info.WasmHash,
		SizeBytes:  info.SizeBytes,
		Exports:    exports,
		Wat:        info.Wat,
		Decompiled: info.Decompiled,
		SourceNote: note,
	}
}

// nonNilStrings returns a non-nil slice so the JSON renders [] not null for a
// no-param/no-result function.
func nonNilStrings(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
