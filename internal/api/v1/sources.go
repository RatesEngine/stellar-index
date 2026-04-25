package v1

import (
	"net/http"
	"sort"

	"github.com/RatesEngine/rates-engine/internal/sources/external"
)

// Source is the wire shape for /v1/sources entries.
//
// Mirrors external.Metadata 1:1 today. Field-by-field on the wire
// rather than embedding so the JSON contract stays decoupled from
// the internal struct — adding an internal-only field on
// external.Metadata won't change the API output.
//
// Class string values: `exchange` / `aggregator` / `oracle` /
// `authority_sanity`. See internal/sources/external/framework.go
// for the policy semantics behind each class.
type Source struct {
	Name              string `json:"name"`
	Class             string `json:"class"`
	IncludeInVWAP     bool   `json:"include_in_vwap"`
	Paid              bool   `json:"paid"`
	BackfillAvailable bool   `json:"backfill_available"`
	DefaultWeight     int    `json:"default_weight"`
}

// handleSources serves GET /v1/sources.
//
// Returns the static external.Registry projected onto the wire
// shape, sorted by name for deterministic responses + cache-
// friendliness behind a CDN. No query parameters; the whole
// catalogue is small enough (~25 entries today) that pagination
// would be over-engineering.
//
// This endpoint is the operator-facing rendering of the same
// metadata the aggregator's class filter consults internally —
// /v1/sources tells API consumers "every venue we know about,
// labelled with whether it contributes to VWAP." A source listed
// with `include_in_vwap=false` is intentional policy
// (aggregator/oracle/authority-sanity classes), not a missing
// connector.
func (s *Server) handleSources(w http.ResponseWriter, _ *http.Request) {
	out := make([]Source, 0, len(external.Registry))
	for name, md := range external.Registry {
		out = append(out, Source{
			Name:              name,
			Class:             string(md.Class),
			IncludeInVWAP:     md.IncludeInVWAP,
			Paid:              md.Paid,
			BackfillAvailable: md.BackfillAvailable,
			DefaultWeight:     md.DefaultWeight,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, out, Flags{})
}
