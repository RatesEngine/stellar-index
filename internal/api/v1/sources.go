package v1

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/StellarIndex/stellar-index/internal/sources/external"
	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// SourcesStatsReader is the seam for /v1/sources?include=stats.
// timescale.Store implements via GetSourceStats.
type SourcesStatsReader interface {
	GetSourceStats(ctx context.Context) ([]timescale.SourceStats, error)
	GetSourceVolumeHistory24h(ctx context.Context) ([]timescale.SourceVolumeBucket, error)
}

// VolumeBucket is one wire-shape hour bucket on the per-source
// 24h sparkline. Hour is RFC 3339; volume_usd is numeric-stringified
// to preserve precision through the JSON boundary; trade_count is the
// number of trades in the hour (powers the trade-count line above the
// $-volume bars on the source page chart).
type VolumeBucket struct {
	Hour       time.Time `json:"hour"`
	VolumeUSD  string    `json:"volume_usd"`
	TradeCount int64     `json:"trade_count"`
}

// buildSourceVolumeHistory projects the per-(source, hour) raw buckets
// into a per-source trailing-24h series, zero-filling missing hours so
// clients render a continuous chart rather than gappy bars. Carries
// both USD volume + trade count per hour.
func buildSourceVolumeHistory(buckets []timescale.SourceVolumeBucket) map[string][]VolumeBucket {
	type hourRaw struct {
		vol   string
		count int64
	}
	rawBySource := map[string]map[time.Time]hourRaw{}
	for _, b := range buckets {
		if rawBySource[b.Source] == nil {
			rawBySource[b.Source] = map[time.Time]hourRaw{}
		}
		rawBySource[b.Source][b.Hour.UTC()] = hourRaw{vol: b.VolumeUSD, count: b.TradeCount}
	}
	out := map[string][]VolumeBucket{}
	now := time.Now().UTC().Truncate(time.Hour)
	for src, raw := range rawBySource {
		series := make([]VolumeBucket, 0, 24)
		for i := 23; i >= 0; i-- {
			hour := now.Add(time.Duration(-i) * time.Hour)
			hr := raw[hour]
			vol := hr.vol
			if vol == "" {
				vol = "0"
			}
			series = append(series, VolumeBucket{Hour: hour, VolumeUSD: vol, TradeCount: hr.count})
		}
		out[src] = series
	}
	return out
}

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
	Name string `json:"name"`
	// Class is the top-level taxonomy: exchange / aggregator /
	// oracle / authority_sanity. Drives the aggregator's class
	// filter (only `exchange` contributes to VWAP by default).
	Class string `json:"class"`
	// Subclass refines `class=exchange` into `dex` / `cex` / `fx`
	// so consumers can group venues (e.g. a UI rendering "DEX
	// liquidity" alongside "CEX prices"). Empty for non-exchange
	// classes (aggregator / oracle / authority_sanity have no
	// subclass dimension).
	Subclass          string `json:"subclass,omitempty"`
	IncludeInVWAP     bool   `json:"include_in_vwap"`
	Paid              bool   `json:"paid"`
	BackfillAvailable bool   `json:"backfill_available"`
	// BackfillSafe gates the `stellarindex-ops backfill` subcommand
	// per CLAUDE.md "Soroban DeFi contracts upgrade in place".
	// On-chain Soroban sources start `false` and only flip `true`
	// after a per-WASM-hash audit
	// (`docs/operations/wasm-audits/`). Off-chain CEX/FX sources
	// are always `true` — no on-chain WASM dependency.
	BackfillSafe  bool `json:"backfill_safe"`
	DefaultWeight int  `json:"default_weight"`
	// Stats columns — populated only when `?include=stats` is set.
	// 0 / "" when the source had no trades in 24h.
	TradeCount24h   int64  `json:"trade_count_24h,omitempty"`
	VolumeUSD24h    string `json:"volume_24h_usd,omitempty"`
	MarketsCount24h int64  `json:"markets_count_24h,omitempty"`
	// VolumeHistory24h — per-hour USD volume buckets for the
	// trailing 24h. Populated only when the request includes
	// `sparkline` (e.g. `?include=stats,sparkline`). Holes are
	// filled with zero-volume buckets so the array always has 24
	// entries, oldest → newest.
	VolumeHistory24h []VolumeBucket `json:"volume_history_24h,omitempty"`
}

// validSourceClasses is the allow-list of values accepted for the
// `class` query parameter. Mirrors external.Class — we deliberately
// duplicate the strings here so the API surface stays stable if the
// internal package ever renames a constant.
var validSourceClasses = map[string]bool{
	string(external.ClassExchange):        true,
	string(external.ClassAggregator):      true,
	string(external.ClassOracle):          true,
	string(external.ClassAuthoritySanity): true,
	string(external.ClassLending):         true,
	string(external.ClassRouter):          true,
}

// handleSources serves GET /v1/sources.
//
// Returns the static external.Registry projected onto the wire
// shape, sorted by name for deterministic responses + cache-
// friendliness behind a CDN. The whole catalogue is small enough
// (~25 entries today) that pagination would be over-engineering.
//
// Query parameters:
//   - class (optional): filter by source class
//     (`exchange` / `aggregator` / `oracle` / `authority_sanity`).
//     Unknown value → 400.
//
// This endpoint is the operator-facing rendering of the same
// metadata the aggregator's class filter consults internally —
// /v1/sources tells API consumers "every venue we know about,
// labelled with whether it contributes to VWAP." A source listed
// with `include_in_vwap=false` is intentional policy
// (aggregator/oracle/authority-sanity classes), not a missing
// connector.
func (s *Server) handleSources(w http.ResponseWriter, r *http.Request) { //nolint:gocognit,gocyclo // include parsing + per-source stat-fill + sparkline backfill are linear; splitting would scatter the request lifecycle
	classFilter := r.URL.Query().Get("class")
	if classFilter != "" && !validSourceClasses[classFilter] {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/invalid-class",
			"Invalid class", http.StatusBadRequest,
			"class must be one of: exchange, aggregator, oracle, authority_sanity")
		return
	}

	// `include=stats` opt-in joins per-source 24h trade counts.
	// Absent the param the response stays the static-registry
	// projection it always was; opt-in lets callers that want the
	// extra column pay the (cheap) DB hit, while everyone else
	// keeps the all-static fast path. `include=stats,sparkline`
	// additionally joins the per-hour 24h volume series.
	includeFlags := strings.Split(r.URL.Query().Get("include"), ",")
	includeStats, includeSparkline := false, false
	for _, f := range includeFlags {
		switch strings.TrimSpace(f) {
		case "stats":
			includeStats = true
		case "sparkline":
			includeSparkline = true
			includeStats = true // sparkline implies stats
		}
	}
	type stats struct {
		trades  int64
		volume  string
		markets int64
	}
	statsBySource := map[string]stats{}
	historyBySource := map[string][]VolumeBucket{}
	if includeStats && s.sourcesStats != nil {
		// 8s ceiling on the stats fan-out — same pattern as
		// /v1/markets (#1099) and /v1/pools (#1082). Soft-fail
		// already serves the registry without stats on error,
		// so a deadline just degrades gracefully rather than
		// hanging the whole sources listing on a cold cache.
		statsCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		got, err := s.sourcesStats.GetSourceStats(statsCtx)
		cancel()
		if err != nil {
			s.logger.Warn("source stats", "err", err)
			// Soft-fail: serve the registry without stats.
		} else {
			for _, ss := range got {
				vol := ""
				if ss.VolumeUSD24h.Valid {
					vol = ss.VolumeUSD24h.String
				}
				statsBySource[ss.Source] = stats{
					trades:  ss.TradeCount24h,
					volume:  vol,
					markets: ss.MarketsCount24h,
				}
			}
		}
	}
	if includeSparkline && s.sourcesStats != nil {
		// Same 8s ceiling as the stats fan-out above.
		sparkCtx, cancel := context.WithTimeout(r.Context(), 8*time.Second)
		buckets, err := s.sourcesStats.GetSourceVolumeHistory24h(sparkCtx)
		cancel()
		if err != nil {
			s.logger.Warn("source volume history", "err", err)
		} else {
			historyBySource = buildSourceVolumeHistory(buckets)
		}
	}

	out := make([]Source, 0, len(external.Registry))
	for name, md := range external.Registry {
		if classFilter != "" && string(md.Class) != classFilter {
			continue
		}
		st := statsBySource[name]
		out = append(out, Source{
			Name:              name,
			Class:             string(md.Class),
			Subclass:          string(md.Subclass),
			IncludeInVWAP:     md.IncludeInVWAP,
			Paid:              md.Paid,
			BackfillAvailable: md.BackfillAvailable,
			BackfillSafe:      md.BackfillSafe,
			DefaultWeight:     md.DefaultWeight,
			TradeCount24h:     st.trades,
			VolumeUSD24h:      st.volume,
			MarketsCount24h:   st.markets,
			VolumeHistory24h:  historyBySource[name],
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	writeJSON(w, out, Flags{})
}
