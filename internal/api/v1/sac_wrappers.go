package v1

import "net/http"

// handleSACWrappers serves GET /v1/sac-wrappers.
//
// Returns the operator-configured map of Stellar-Asset-Contract
// (SAC) C-strkey contract IDs to their underlying classic-asset
// keys ("CODE-ISSUER"). Soroban DEX sources (Soroswap, Phoenix,
// Aquarius, Comet) emit base/quote as the SAC contract address
// in their swap events, not the underlying classic asset key.
// The explorer joins this map client-side in AssetLabel to
// render readable symbols instead of `CAS3J7…OWMA`.
//
// Wire shape: `{ data: { "C…": "CODE-ISSUER", … } }`. Empty
// object when the operator hasn't configured any wrappers
// (deployment without the [supply.sac_wrappers] block) — the
// explorer then degrades to showing the truncated C-strkey as
// before.
//
// No query parameters. Cacheable; the map only changes when the
// operator restarts the API process with a new config.
func (s *Server) handleSACWrappers(w http.ResponseWriter, r *http.Request) {
	out := s.sacWrappers
	if out == nil {
		out = map[string]string{}
	}
	writeJSON(w, out, Flags{})
}
