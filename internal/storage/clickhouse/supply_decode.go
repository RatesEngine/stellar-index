package clickhouse

import (
	"math/big"

	"github.com/stellar/go-stellar-sdk/xdr"

	"github.com/StellarAtlas/stellar-atlas/internal/scval"
)

// SupplyFlowTopic0Syms are the topic[0] symbols whose events change a token's
// supply under CAP-67 (classic SAC) / SEP-41: a mint adds, a burn/clawback
// removes. Total supply(contract) = Σmint − Σburn − Σclawback over all history
// (baseline 0 at the asset/contract's genesis).
var SupplyFlowTopic0Syms = []string{"mint", "burn", "clawback"}

// IsSupplyFlowSym reports whether a topic[0] symbol is a supply-affecting event.
func IsSupplyFlowSym(sym string) bool {
	switch sym {
	case "mint", "burn", "clawback":
		return true
	default:
		return false
	}
}

// DecodeSupplyAmount extracts the (positive) magnitude from a mint/burn/clawback
// event's data body: either a bare i128 (the common shape) or the SEP-41/CAP-67
// map variant {amount, to_muxed_id, …} that carries the amount in an `amount`
// field when a muxed destination is present (CLAUDE.md SEP-41 note). Returns
// ok=false with a short reason for an undecodable body so callers skip-and-
// continue. The amount is a *big.Int (ADR-0003: i128 never truncated). Shared
// by the ingest extractor (decode-at-ingest → supply_flows) and the ch-supply*
// CH-reading tools so both decode identically.
func DecodeSupplyAmount(sv xdr.ScVal) (*big.Int, string, bool) {
	if amt, err := scval.AsAmountFromI128(sv); err == nil {
		return amt.BigInt(), "", true
	}
	if sv.Type == xdr.ScValTypeScvMap {
		entries, merr := scval.AsMap(sv)
		if merr != nil {
			return nil, "map-parse-error", false
		}
		amtVal, ok := scval.MapField(entries, "amount")
		if !ok {
			return nil, "map-no-amount", false
		}
		if amt, aerr := scval.AsAmountFromI128(amtVal); aerr == nil {
			return amt.BigInt(), "", true
		}
		return nil, "map-amount-not-i128", false
	}
	return nil, sv.Type.String(), false
}

// DecodeSupplyAmountXDR parses a base64 scval data body then decodes the amount
// — the string-input form used by the CH-reading supply tools (the ingest
// extractor already holds the parsed xdr.ScVal and calls DecodeSupplyAmount).
func DecodeSupplyAmountXDR(dataXDR string) (*big.Int, string, bool) {
	sv, err := scval.Parse(dataXDR)
	if err != nil {
		return nil, "parse-error", false
	}
	return DecodeSupplyAmount(sv)
}
