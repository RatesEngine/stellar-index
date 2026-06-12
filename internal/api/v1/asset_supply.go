package v1

import (
	"context"
	"net/http"
	"strconv"

	"github.com/StellarAtlas/stellar-atlas/internal/canonical"
	"github.com/StellarAtlas/stellar-atlas/internal/storage/clickhouse"
)

// TokenSupplyReader is the seam GET /v1/assets/{asset_id}/supply reads through:
// the ClickHouse supply_flows lake (decode-at-ingest, ADR-0034), which carries
// the decoded mint/burn/clawback amount per event so per-token supply is a live
// SQL sum with no rollup refresh. *clickhouse.SupplyReader satisfies it. Nil
// disables the endpoint (503). Distinct from the F2 [SupplyLooker]
// (asset_supply_history / circulating-vs-max policy, ADR-0011) — this is the
// raw on-chain Σmint−Σburn−Σclawback total for EVERY token.
type TokenSupplyReader interface {
	TokenSupply(ctx context.Context, contractID string) (clickhouse.TokenSupply, error)
	NativeTotalCoins(ctx context.Context) (totalCoins int64, ledger uint32, err error)
}

// AssetSupply is the wire response for GET /v1/assets/{asset_id}/supply.
// Amounts are decimal strings in the asset's smallest unit (ADR-0003: an i128
// is never a JSON number); the client applies per-asset decimals for display.
type AssetSupply struct {
	AssetID       string  `json:"asset_id"`
	ContractID    string  `json:"contract_id,omitempty"`
	TotalSupply   string  `json:"total_supply"`
	MintTotal     *string `json:"mint_total,omitempty"`
	BurnTotal     *string `json:"burn_total,omitempty"`
	ClawbackTotal *string `json:"clawback_total,omitempty"`
	FlowCount     uint64  `json:"flow_count"`
	// Source is how TotalSupply was derived: "mint_burn_flows" (Σmint−Σburn−
	// Σclawback from supply_flows) or "ledger_total_coins" (XLM, from the ledger
	// header — XLM has no SAC mint/burn events).
	Source string `json:"source"`
}

// handleAssetSupply serves GET /v1/assets/{asset_id}/supply — a token's live
// supply from the decode-at-ingest supply_flows lake. The literal "supply"
// segment takes precedence over the {asset_id}/{network} wildcard route.
func (s *Server) handleAssetSupply(w http.ResponseWriter, r *http.Request) {
	if s.tokenSupply == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/supply-unavailable",
			"Supply unavailable", http.StatusServiceUnavailable,
			"This deployment hasn't wired the ClickHouse supply reader yet.")
		return
	}
	assetID := r.PathValue("asset_id")
	if assetID == "" {
		writeProblem(w, r, "https://api.ratesengine.net/errors/invalid-asset",
			"Invalid asset", http.StatusBadRequest, "asset_id path segment is required.")
		return
	}

	// XLM: supply is the ledger header's total_coins (XLM is not minted/burned
	// via SAC mint/burn events, so it has no supply_flows). Handle every alias.
	if assetID == "native" || assetID == "XLM" || assetID == "crypto:XLM" {
		coins, _, err := s.tokenSupply.NativeTotalCoins(r.Context())
		if err != nil {
			s.logger.Warn("supply: native total_coins", "err", err)
			writeProblem(w, r, "https://api.ratesengine.net/errors/supply-error",
				"Supply read failed", http.StatusBadGateway, "Could not read native supply.")
			return
		}
		writeJSON(w, AssetSupply{
			AssetID:     assetID,
			TotalSupply: strconv.FormatInt(coins, 10),
			Source:      "ledger_total_coins",
		}, Flags{})
		return
	}

	contractID, ok := s.resolveSupplyContractID(assetID)
	if !ok {
		writeProblem(w, r, "https://api.ratesengine.net/errors/supply-not-mapped",
			"Supply not available", http.StatusNotFound,
			"No Stellar-Asset-Contract is mapped for this classic asset; supply is keyed by contract. Soroban tokens (C…) resolve directly.")
		return
	}

	sup, err := s.tokenSupply.TokenSupply(r.Context(), contractID)
	if err != nil {
		s.logger.Warn("supply: token supply", "contract_id", contractID, "err", err)
		writeProblem(w, r, "https://api.ratesengine.net/errors/supply-error",
			"Supply read failed", http.StatusBadGateway, "Could not read token supply.")
		return
	}
	mint, burn, clawback := sup.Mint.String(), sup.Burn.String(), sup.Clawback.String()
	writeJSON(w, AssetSupply{
		AssetID:       assetID,
		ContractID:    contractID,
		TotalSupply:   sup.Total.String(),
		MintTotal:     &mint,
		BurnTotal:     &burn,
		ClawbackTotal: &clawback,
		FlowCount:     sup.FlowCount,
		Source:        "mint_burn_flows",
	}, Flags{})
}

// resolveSupplyContractID maps an asset_id to the contract_id supply_flows is
// keyed by: a Soroban C-strkey is itself; a classic asset ("CODE-ISSUER") is
// resolved to its Stellar-Asset-Contract via the operator's sac_wrappers map
// (reversed). Full SAC derivation for any classic asset is a follow-up; until
// then only configured classic assets resolve (others 404).
func (s *Server) resolveSupplyContractID(assetID string) (string, bool) {
	if canonical.IsContractID(assetID) {
		return assetID, true
	}
	for sac, assetKey := range s.sacWrappers {
		if assetKey == assetID {
			return sac, true
		}
	}
	return "", false
}
