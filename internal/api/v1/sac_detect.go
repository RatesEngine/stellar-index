package v1

import "github.com/StellarIndex/stellar-index/internal/xdrjson"

// isKnownSAC reports whether contractID is a Stellar Asset Contract we can
// identify WITHOUT a captured instance entry. A SAC's id is the deterministic
// derivation of its classic asset (native or code:issuer), so even when the
// instance entry predates the lake's capture window we can answer "SAC, no
// WASM" definitively for the busiest asset contracts (native, USDC, AQUA, …)
// instead of implying a backfill might produce WASM. The set unions the
// operator's sac_wrappers registry with the computed SAC ids of the native
// asset and every verified-catalogue classic asset. Built once, cached.
//
// SAC derivation goes through internal/xdrjson (which owns the xdr dependency
// under ADR-0013) — the v1 API layer must not import go-stellar-sdk/xdr
// directly.
func (s *Server) isKnownSAC(contractID string) bool {
	s.knownSACsOnce.Do(func() { s.knownSACs = s.buildKnownSACs() })
	_, ok := s.knownSACs[contractID]
	return ok
}

func (s *Server) buildKnownSACs() map[string]struct{} {
	set := make(map[string]struct{}, len(s.sacWrappers))
	for cid := range s.sacWrappers {
		set[cid] = struct{}{}
	}
	if s.networkPassphrase == "" {
		return set
	}
	add := func(assetID string) {
		if cid, ok := xdrjson.SACContractID(assetID, s.networkPassphrase); ok {
			set[cid] = struct{}{}
		}
	}
	add("native")
	if s.verifiedCurrencies != nil {
		for _, vc := range s.verifiedCurrencies.Browseable() {
			if se := vc.StellarEntry(); se != nil && se.AssetID != "" {
				add(se.AssetID)
			}
		}
	}
	return set
}
