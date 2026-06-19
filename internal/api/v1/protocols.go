package v1

import (
	"context"
	"net/http"
	"time"

	"github.com/StellarIndex/stellar-index/internal/storage/clickhouse"
	"github.com/StellarIndex/stellar-index/internal/storage/timescale"
)

// protocolDetailTTL caches the expensive /v1/protocols/{name} detail
// (lake-analytics + bespoke scans run ~15s) per-process for 60s with
// single-flight, so concurrent requests collapse to one query instead
// of pegging CPU (2026-06-19 incident). The proper fix — a CAGG so the
// scan is fast — is tracked in docs/archive/page-audit-2026-06-19/.
const protocolDetailTTL = 60 * time.Second

type protoDetailEntry struct {
	view ProtocolDetailView
	at   time.Time
}

// cachedProtocolDetail returns a TTL-cached detail view for `name`,
// computing it via build() at most once per TTL across concurrent
// callers. Per-server (the maps live on Server, lazy-init'd) so it never
// leaks across test instances. Returns ok=false only when the caller's
// context is cancelled while waiting on another caller's in-flight
// build. A build that runs to completion is cached; one that returns
// under a cancelled context (the 25s ceiling fired) is returned but NOT
// cached, so a partial result can't stick for the full TTL.
func (s *Server) cachedProtocolDetail(ctx context.Context, name string, build func(context.Context) ProtocolDetailView) (ProtocolDetailView, bool) {
	s.protoDetailMu.Lock()
	if s.protoDetailCache == nil {
		s.protoDetailCache = map[string]protoDetailEntry{}
		s.protoDetailFlight = map[string]chan struct{}{}
	}
	if e, ok := s.protoDetailCache[name]; ok && time.Since(e.at) < protocolDetailTTL {
		s.protoDetailMu.Unlock()
		return e.view, true
	}
	if ch, inflight := s.protoDetailFlight[name]; inflight {
		s.protoDetailMu.Unlock()
		select {
		case <-ch:
			s.protoDetailMu.Lock()
			e, ok := s.protoDetailCache[name]
			s.protoDetailMu.Unlock()
			return e.view, ok
		case <-ctx.Done():
			return ProtocolDetailView{}, false
		}
	}
	done := make(chan struct{})
	s.protoDetailFlight[name] = done
	s.protoDetailMu.Unlock()

	view := build(ctx)
	complete := ctx.Err() == nil

	s.protoDetailMu.Lock()
	if complete {
		s.protoDetailCache[name] = protoDetailEntry{view: view, at: time.Now()}
	}
	delete(s.protoDetailFlight, name)
	s.protoDetailMu.Unlock()
	close(done)
	return view, true
}

// protocolActivityWindowDays is the lookback for the windowed per-protocol
// analytics (the activity time-series). ~90 days ≈ 1.55M ledgers — bounded so
// the lake query prunes partitions and stays fast on the 12B-row table.
const protocolActivityWindowDays = 90

// protocolActivityWindowLedgers is protocolActivityWindowDays expressed in
// ledgers (~5s close time → 17,280/day).
const protocolActivityWindowLedgers = protocolActivityWindowDays * 17280

// ProtocolActivityReader serves per-protocol on-chain analytics from the
// certified lake (contract_events): event-type breakdown, daily activity
// series, and per-contract rollups, all scoped to a protocol's contract-id set.
// Production wiring is *clickhouse.ExplorerReader (the same lake reader the
// network explorer uses). Nil reader → the analytics fields serve empty; the
// directory + registry still work.
type ProtocolActivityReader interface {
	LakeTipLedger(ctx context.Context) (uint32, error)
	ProtocolEventBreakdown(ctx context.Context, contractIDs []string, sinceLedger uint32) ([]clickhouse.ProtocolEventTypeCount, error)
	ProtocolDailyActivity(ctx context.Context, contractIDs []string, sinceLedger uint32) ([]clickhouse.ProtocolDailyPoint, error)
	ProtocolContractActivity(ctx context.Context, contractIDs []string, sinceLedger uint32) ([]clickhouse.ProtocolContractActivity, error)
}

// ProtocolBespokeReader builds the per-category bespoke analytics block from the
// served-tier projected tables (TVL/volume/AUM/flows/feeds). Production wiring
// is timescale.Store. Nil → the bespoke block is absent (the rest of the page
// still serves).
type ProtocolBespokeReader interface {
	BuildProtocolBespoke(ctx context.Context, source, category string, windowDays int) (*timescale.BespokeBlock, error)
}

// bespokeFromStore maps the timescale-side block to the wire view (the two
// shapes are intentionally identical; timescale can't import v1).
func bespokeFromStore(b *timescale.BespokeBlock) *ProtocolBespoke {
	if b == nil {
		return nil
	}
	out := &ProtocolBespoke{Category: b.Category, Notes: b.Notes}
	for _, k := range b.KPIs {
		out.KPIs = append(out.KPIs, BespokeKPI{Label: k.Label, Value: k.Value, Unit: k.Unit, Hint: k.Hint})
	}
	for _, s := range b.Series {
		pts := make([]BespokeSeriesPt, 0, len(s.Points))
		for _, p := range s.Points {
			pts = append(pts, BespokeSeriesPt{Date: p.Date, Value: p.Value})
		}
		out.Series = append(out.Series, BespokeSeries{Name: s.Name, Unit: s.Unit, Points: pts})
	}
	for _, t := range b.Tables {
		out.Tables = append(out.Tables, BespokeTable{Title: t.Title, Columns: t.Columns, Rows: t.Rows})
	}
	return out
}

// ProtocolContractsReader is the read seam for the protocol_contracts
// registry (ADR-0035 factory-anchored gating). Production wiring is
// timescale.Store.ListProtocolContracts. Nil reader → contract lists
// and counts serve empty; never an error.
type ProtocolContractsReader interface {
	ListProtocolContracts(ctx context.Context, source string) ([]timescale.ProtocolContract, error)
	// ListSourceContractsFromProjection is the fallback roster for protocols
	// the protocol_contracts registry doesn't carry yet (only blend is seeded
	// today): the distinct contract ids from the source's projected table
	// (defindex_flows / phoenix_liquidity / comet_liquidity / cctp_events /
	// rozo_events). Returns nil for sources without one — the page then keeps
	// its registry/pairs path. Lets defindex/phoenix/comet/cctp/rozo show a
	// full roster + the lake analytics scoped to it, without waiting on the
	// factory-enumeration team answer.
	ListSourceContractsFromProjection(ctx context.Context, source string) ([]string, error)
	// ProtocolContractIndex returns a contract_id → source map over every
	// registered protocol contract — the explorer's contract-attribution
	// overlay (the contracts directory + contract detail tag each contract
	// with its owning protocol). Returns an empty map (never an error) when
	// nothing is seeded.
	ProtocolContractIndex(ctx context.Context) (map[string]string, error)
}

// ProtocolStatsReader supplies the trailing-24h event count per source
// (one grouped UNION ALL over the per-protocol tables). Production
// wiring is timescale.Store.CountRecentEventsBySource. Nil reader →
// events_24h serves 0 for every protocol.
type ProtocolStatsReader interface {
	CountRecentEventsBySource(ctx context.Context) (map[string]int64, error)
}

// SoroswapPairsReader exposes the soroswap_pairs registry — Soroswap's
// equivalent of protocol_contracts (its pair set predates the unified
// registry and carries token identities the decoder needs). Production
// wiring is timescale.Store.LoadSoroswapPairRegistry. Nil reader →
// the soroswap contract list/count serves empty.
type SoroswapPairsReader interface {
	LoadSoroswapPairRegistry(ctx context.Context) ([]timescale.SoroswapPair, error)
}

// ProtocolCompletenessView is the verdict summary joined onto a
// protocol row — the headline slice of /v1/coverage's full verdict
// (same completeness_snapshots row, keyed by source name).
type ProtocolCompletenessView struct {
	// Complete is the headline ADR-0033 verdict.
	Complete bool `json:"complete"`
	// WatermarkLedger is the highest ledger the verdict covers.
	WatermarkLedger uint32 `json:"watermark_ledger"`
}

// ProtocolView is the wire shape of one directory row on
// GET /v1/protocols.
type ProtocolView struct {
	// Name is the canonical source name — the same identifier
	// /v1/coverage and /v1/sources use.
	Name string `json:"name"`
	// Category is one of: dex | amm | lending | yield | bridge | oracle | token.
	Category string `json:"category"`
	// Description is a one-sentence summary for the directory card.
	Description string `json:"description"`
	// GenesisLedger is the first ledger this protocol could have data at.
	GenesisLedger uint32 `json:"genesis_ledger"`
	// Factories lists the verified factory / trust-root contract IDs
	// the decoder anchors on (ADR-0035); empty for factory-less sources.
	Factories []string `json:"factories"`
	// ContractCount is the number of registered contract instances
	// (protocol_contracts rows; soroswap_pairs rows for soroswap).
	ContractCount int `json:"contract_count"`
	// Events24h is the trailing-24h decoded-event count across the
	// protocol's served tables.
	Events24h int64 `json:"events_24h"`
	// Completeness is the latest ADR-0033 verdict summary, absent when
	// no completeness snapshot exists for this source.
	Completeness *ProtocolCompletenessView `json:"completeness,omitempty"`
}

// ProtocolsView is the envelope data field of GET /v1/protocols.
type ProtocolsView struct {
	// Protocols lists every indexed protocol in registry order.
	Protocols []ProtocolView `json:"protocols"`
	// TotalProtocols is len(protocols), for symmetric pagination-free
	// consumers.
	TotalProtocols int `json:"total_protocols"`
}

// ProtocolContractView is one registered contract instance on
// GET /v1/protocols/{name} — a unified projection over
// protocol_contracts (factory-gated sources) and soroswap_pairs
// (token0/token1 populated, factory absent).
type ProtocolContractView struct {
	// ContractID is the instance's C-strkey.
	ContractID string `json:"contract_id"`
	// FactoryID is the deploying factory's C-strkey (gated sources;
	// empty for soroswap pairs, which are keyed by token identities).
	FactoryID string `json:"factory_id,omitempty"`
	// FirstLedger is the ledger the instance was first observed at
	// (0/absent when the seed source didn't carry it).
	FirstLedger uint32 `json:"first_ledger,omitempty"`
	// Token0 / Token1 are the pair's token C-strkeys (soroswap only).
	Token0 string `json:"token0,omitempty"`
	Token1 string `json:"token1,omitempty"`
	// Kind classifies the instance within the protocol: "factory" (a verified
	// trust-root in meta.Factories) or "instance" (a pool/vault/market the
	// factory deployed). Lets the page group the roster by role.
	Kind string `json:"kind,omitempty"`
	// Events is the all-time decoded contract-event count emitted by this
	// instance (from the lake). 0/absent when the activity reader is nil.
	Events int64 `json:"events,omitempty"`
	// LastSeen is the close time of this instance's most recent event
	// (RFC3339); absent when unknown / no activity reader.
	LastSeen string `json:"last_seen,omitempty"`
}

// ProtocolEventTypeView is one slice of a protocol's event-type distribution:
// a topic[0] symbol and how many times it fired (all-time, from the lake).
type ProtocolEventTypeView struct {
	EventType string `json:"event_type"`
	Count     int64  `json:"count"`
}

// ─── Bespoke per-category analytics (the Dune-surpassing block) ──────
//
// ProtocolBespoke is a generic rendering container — KPIs + named time-series
// + named top-N tables — filled with content BESPOKE to each protocol's
// category (lending shows TVL/borrows/utilization; a DEX shows swap volume +
// top pairs; a vault shows AUM + flows; a bridge shows transfer volume by
// domain; an oracle shows feeds + update cadence). The UI renders the three
// shapes generically, so adding/retuning a category's metrics is a server-side
// data change, not a new UI layout.

// ProtocolBespoke is the category-specific analytics block on
// GET /v1/protocols/{name}. Absent when no bespoke reader is wired or the
// category has none yet.
type ProtocolBespoke struct {
	// Category is the metric family: dex | amm | lending | vault | bridge | oracle.
	Category string `json:"category"`
	// KPIs are the headline numbers (pre-formatted) for the metric cards.
	KPIs []BespokeKPI `json:"kpis,omitempty"`
	// Series are named time-series for the charts (e.g. "USD volume", "TVL").
	Series []BespokeSeries `json:"series,omitempty"`
	// Tables are named top-N tables (e.g. "Top pairs", "Supplied by asset").
	Tables []BespokeTable `json:"tables,omitempty"`
	// Notes are caveats/provenance lines rendered under the block.
	Notes []string `json:"notes,omitempty"`
}

// BespokeKPI is one headline metric card. Value is PRE-FORMATTED (the server
// owns formatting so the number is correct + ADR-0003-safe); Unit is advisory.
type BespokeKPI struct {
	Label string `json:"label"`
	Value string `json:"value"`
	Unit  string `json:"unit,omitempty"`
	Hint  string `json:"hint,omitempty"`
}

// BespokeSeries is a named time-series for a chart.
type BespokeSeries struct {
	Name   string            `json:"name"`
	Unit   string            `json:"unit,omitempty"`
	Points []BespokeSeriesPt `json:"points"`
}

// BespokeSeriesPt is one (date, value) point. Value is a numeric STRING
// (ADR-0003: amounts can exceed 2^53).
type BespokeSeriesPt struct {
	Date  string `json:"date"`
	Value string `json:"value"`
}

// BespokeTable is a named top-N table — columns + string rows (the server
// formats every cell).
type BespokeTable struct {
	Title   string     `json:"title"`
	Columns []string   `json:"columns"`
	Rows    [][]string `json:"rows"`
}

// ProtocolActivityPointView is one day of a protocol's event-activity series.
type ProtocolActivityPointView struct {
	Date   string `json:"date"`
	Events int64  `json:"events"`
}

// ProtocolDetailView is the envelope data field of
// GET /v1/protocols/{name}: the directory row plus the contract
// registry, decoded event vocabulary and verification write-up path.
type ProtocolDetailView struct {
	ProtocolView
	// Contracts lists every registered instance; empty for sources
	// without a contract registry (oracles, sdex, bridges).
	Contracts []ProtocolContractView `json:"contracts"`
	// EventKinds lists the EventKind() discriminators the source's
	// decoder emits.
	EventKinds []string `json:"event_kinds"`
	// VerificationPage is the repo-relative path of the protocol's
	// verification write-up, absent when none exists yet.
	VerificationPage string `json:"verification_page,omitempty"`

	// ─── Lake analytics (populated when the activity reader is wired) ──

	// EventBreakdown is the event-type distribution (topic[0] symbol →
	// count) across the protocol's contracts over the trailing
	// ActivityWindowDays — "which event types fired, and how often." All
	// analytics share this window so the lake queries stay partition-pruned
	// and fast. Descending by count. Includes a synthetic "untyped" bucket
	// for events whose topic[0] isn't a denormalized Symbol in the lake
	// (predominantly AMM swap/sync events), so sum(EventBreakdown) reconciles
	// to EventsTotal.
	EventBreakdown []ProtocolEventTypeView `json:"event_breakdown,omitempty"`
	// ActivitySeries is the daily decoded-event count over the trailing
	// ActivityWindowDays — the protocol's on-chain activity chart.
	ActivitySeries []ProtocolActivityPointView `json:"activity_series,omitempty"`
	// ActivityWindowDays is the lookback all the analytics fields cover.
	ActivityWindowDays int `json:"activity_window_days,omitempty"`
	// EventsTotal is the contract-event count across every contract in the
	// protocol over ActivityWindowDays — the unfiltered total (= sum of
	// ActivitySeries = sum of EventBreakdown incl. the untyped bucket). NOT
	// the typed-breakdown sum, which excludes non-Symbol-topic'd events.
	EventsTotal int64 `json:"events_total,omitempty"`

	// Bespoke is the category-specific analytics block (TVL/volume/AUM/…) —
	// the Dune-surpassing, tailored-per-protocol content. Absent when no
	// bespoke reader is wired or the category has no bespoke metrics yet.
	Bespoke *ProtocolBespoke `json:"bespoke,omitempty"`
}

// handleProtocolsList serves GET /v1/protocols — the protocol
// directory backing the explorer's Protocols pillar. The static
// registry (protocols_registry.go) always serves; the dynamic joins
// (contract counts, 24h events, completeness verdicts) degrade to
// zeros/absent when their reader is nil or errors, so a deployment
// without the optional readers still gets a useful directory.
func (s *Server) handleProtocolsList(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	events := s.protocolEvents24h(ctx)
	verdicts := s.protocolVerdicts(ctx)

	view := ProtocolsView{Protocols: make([]ProtocolView, 0, len(protocolRegistry))}
	for _, meta := range protocolRegistry {
		contracts := s.protocolContracts(ctx, meta.Name)
		view.Protocols = append(view.Protocols,
			buildProtocolView(meta, len(contracts), events, verdicts))
	}
	view.TotalProtocols = len(view.Protocols)

	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, view, Flags{})
}

// handleProtocolDetail serves GET /v1/protocols/{name} — everything
// the directory row carries plus the contract registry, event-kind
// vocabulary and verification page. Unknown names 404.
func (s *Server) handleProtocolDetail(w http.ResponseWriter, r *http.Request) {
	meta, ok := protocolByName(r.PathValue("name"))
	if !ok {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/protocol-not-found",
			"Protocol not found", http.StatusNotFound,
			"unknown protocol name; GET /v1/protocols lists every known protocol")
		return
	}

	// Hard ceiling on the lake-derived analytics + bespoke scans. They
	// run ~15s warm but were observed ballooning to MINUTES under
	// concurrent load (2026-06-19 incident), pegging CPU because nothing
	// cancelled the runaway. 25s is generous for the normal path yet
	// caps the runaway; the enrich* helpers degrade gracefully when the
	// context is cancelled. (The real fix — a CAGG so these are fast —
	// is tracked in docs/archive/page-audit-2026-06-19/REMAINING.md.)
	ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
	defer cancel()
	view, ok := s.cachedProtocolDetail(ctx, meta.Name, func(c context.Context) ProtocolDetailView {
		contracts := s.protocolContracts(c, meta.Name)
		classifyContractKinds(contracts, meta.Factories)
		v := ProtocolDetailView{
			ProtocolView:     buildProtocolView(meta, len(contracts), s.protocolEvents24h(c), s.protocolVerdicts(c)),
			Contracts:        contracts,
			EventKinds:       append([]string{}, meta.EventKinds...),
			VerificationPage: meta.VerificationPage,
		}
		s.enrichProtocolAnalytics(c, meta, &v)
		s.enrichBespoke(c, meta, &v)
		return v
	})
	if !ok {
		writeProblem(w, r,
			"https://api.stellarindex.io/errors/protocol-detail-timeout",
			"Protocol detail timed out", http.StatusServiceUnavailable,
			"the protocol analytics are being recomputed; retry in a few seconds")
		return
	}

	w.Header().Set("Cache-Control", "public, max-age=60")
	writeJSON(w, view, Flags{})
}

// enrichBespoke attaches the category-specific bespoke analytics block,
// degrading to absent when the reader is nil or errors.
func (s *Server) enrichBespoke(ctx context.Context, meta ProtocolMeta, view *ProtocolDetailView) {
	if s.protocolBespoke == nil {
		return
	}
	blk, err := s.protocolBespoke.BuildProtocolBespoke(ctx, meta.Name, meta.Category, protocolActivityWindowDays)
	if err != nil {
		s.logger.Warn("protocol bespoke build failed", "source", meta.Name, "category", meta.Category, "err", err)
		return
	}
	view.Bespoke = bespokeFromStore(blk)
}

// classifyContractKinds tags each roster contract as "factory" (it is one of
// the protocol's verified trust-roots) or "instance" (a factory-deployed
// pool/vault/market).
func classifyContractKinds(contracts []ProtocolContractView, factories []string) {
	fset := make(map[string]struct{}, len(factories))
	for _, f := range factories {
		fset[f] = struct{}{}
	}
	for i := range contracts {
		if _, ok := fset[contracts[i].ContractID]; ok {
			contracts[i].Kind = "factory"
		} else {
			contracts[i].Kind = "instance"
		}
	}
}

// enrichProtocolAnalytics populates the lake-derived analytics on the detail
// view: the event-type breakdown, the daily activity series, and per-contract
// event counts merged onto the roster. Degrades to leaving the fields empty
// when the activity reader is nil or any query errors (same contract as the
// other optional joins — the directory + registry still serve).
func (s *Server) enrichProtocolAnalytics(ctx context.Context, meta ProtocolMeta, view *ProtocolDetailView) {
	if s.protocolActivity == nil {
		return
	}
	ids := protocolContractIDs(view.Contracts, meta.Factories)
	if len(ids) == 0 {
		return
	}
	// All three analytics are bounded to the recent window: bounding by
	// ledger_seq prunes partitions, keeping each query well under the lake
	// reader's 30s budget even for the busiest protocols (an all-time scan ran
	// ~33s for blend / would be far worse for soroswap). 0 ⇒ tip unreadable ⇒
	// skip the analytics entirely (degrade, don't serve partial/timed-out).
	since := s.protocolActivitySince(ctx)
	if since == 0 {
		return
	}
	view.ActivityWindowDays = protocolActivityWindowDays
	// Series first: it counts ALL contract events over the window, which
	// is the authoritative EventsTotal. The breakdown then reconciles to
	// that total via an "(untyped)" remainder bucket (see fillProtocolBreakdown).
	s.fillProtocolSeries(ctx, meta.Name, ids, since, view)
	s.fillProtocolBreakdown(ctx, meta.Name, ids, since, view)
	s.fillProtocolContractActivity(ctx, meta.Name, ids, since, view)
}

// protocolContractIDs is the dedup'd analytics scope: every registered instance
// + the verified factories themselves (factories emit events too, e.g.
// new_pair / deploy).
func protocolContractIDs(contracts []ProtocolContractView, factories []string) []string {
	ids := make([]string, 0, len(contracts)+len(factories))
	seen := make(map[string]struct{}, cap(ids))
	add := func(id string) {
		if id == "" {
			return
		}
		if _, dup := seen[id]; dup {
			return
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	for _, c := range contracts {
		add(c.ContractID)
	}
	for _, f := range factories {
		add(f)
	}
	return ids
}

// fillProtocolBreakdown populates EventBreakdown (degrades on error).
//
// The breakdown groups by topic[0]'s denormalized symbol (topic_0_sym),
// which the lake only populates when topic[0] is a plain Symbol SCVal.
// Many Soroban DEX events carry a non-Symbol topic[0] — Soroswap's
// swap/sync events are the dominant case (190k+ over a 90d window with an
// empty topic_0_sym) — so the typed breakdown alone under-counts the true
// event total by a wide margin, and is empty entirely for protocols whose
// every event is non-Symbol-topic'd (the phoenix "empty breakdown but the
// chart has data" case). To keep the breakdown reconciled with EventsTotal
// (which fillProtocolSeries sets from the unfiltered count), append a
// synthetic "untyped" bucket carrying the remainder. EventsTotal must
// already be set (series is filled first in enrichProtocolAnalytics).
func (s *Server) fillProtocolBreakdown(ctx context.Context, name string, ids []string, since uint32, view *ProtocolDetailView) {
	breakdown, err := s.protocolActivity.ProtocolEventBreakdown(ctx, ids, since)
	if err != nil {
		s.logger.Warn("protocol event breakdown failed", "source", name, "err", err)
		return
	}
	view.EventBreakdown = make([]ProtocolEventTypeView, 0, len(breakdown)+1)
	var typedSum int64
	for _, b := range breakdown {
		view.EventBreakdown = append(view.EventBreakdown, ProtocolEventTypeView{EventType: b.EventType, Count: int64(b.Count)})
		typedSum += int64(b.Count)
	}
	// Reconcile to the authoritative total: the gap is events whose
	// topic[0] symbol the lake didn't denormalize (predominantly AMM
	// swap/sync events). Surfacing it as a labeled bucket keeps
	// sum(EventBreakdown) == EventsTotal == sum(ActivitySeries).
	if untyped := view.EventsTotal - typedSum; untyped > 0 {
		view.EventBreakdown = append(view.EventBreakdown, ProtocolEventTypeView{EventType: "untyped", Count: untyped})
	}
}

// fillProtocolSeries populates the daily ActivitySeries + EventsTotal
// (degrades on error). EventsTotal is the unfiltered contract-event count
// over the window (the sum of the daily points), which is the
// authoritative total the breakdown reconciles against — NOT the typed
// breakdown sum, which excludes non-Symbol-topic'd events.
func (s *Server) fillProtocolSeries(ctx context.Context, name string, ids []string, since uint32, view *ProtocolDetailView) {
	series, err := s.protocolActivity.ProtocolDailyActivity(ctx, ids, since)
	if err != nil {
		s.logger.Warn("protocol daily activity failed", "source", name, "err", err)
		return
	}
	view.ActivitySeries = make([]ProtocolActivityPointView, 0, len(series))
	var total int64
	for _, p := range series {
		view.ActivitySeries = append(view.ActivitySeries, ProtocolActivityPointView{Date: p.Date, Events: int64(p.Events)})
		total += int64(p.Events)
	}
	view.EventsTotal = total
}

// fillProtocolContractActivity merges per-contract event counts + last-seen onto
// the roster (degrades on error).
func (s *Server) fillProtocolContractActivity(ctx context.Context, name string, ids []string, since uint32, view *ProtocolDetailView) {
	act, err := s.protocolActivity.ProtocolContractActivity(ctx, ids, since)
	if err != nil {
		s.logger.Warn("protocol contract activity failed", "source", name, "err", err)
		return
	}
	byID := make(map[string]clickhouse.ProtocolContractActivity, len(act))
	for _, a := range act {
		byID[a.ContractID] = a
	}
	for i := range view.Contracts {
		a, ok := byID[view.Contracts[i].ContractID]
		if !ok {
			continue
		}
		view.Contracts[i].Events = int64(a.Events)
		if !a.LastSeen.IsZero() {
			view.Contracts[i].LastSeen = a.LastSeen.UTC().Format(time.RFC3339)
		}
	}
}

// protocolActivitySince returns the ledger cutoff for the windowed activity
// series (tip − window). 0 when the tip can't be read (caller skips the series).
func (s *Server) protocolActivitySince(ctx context.Context) uint32 {
	tip, err := s.protocolActivity.LakeTipLedger(ctx)
	if err != nil {
		s.logger.Warn("protocol activity tip read failed", "err", err)
		return 0
	}
	if tip <= protocolActivityWindowLedgers {
		return 1 // whole chain is inside the window
	}
	return tip - protocolActivityWindowLedgers
}

// buildProtocolView projects one registry entry + the dynamic joins
// into the directory wire shape.
func buildProtocolView(meta ProtocolMeta, contractCount int, events map[string]int64, verdicts map[string]timescale.CompletenessSnapshot) ProtocolView {
	v := ProtocolView{
		Name:          meta.Name,
		Category:      meta.Category,
		Description:   meta.Description,
		GenesisLedger: meta.GenesisLedger,
		Factories:     append([]string{}, meta.Factories...),
		ContractCount: contractCount,
		Events24h:     events[meta.Name],
	}
	if sn, ok := verdicts[meta.Name]; ok {
		v.Completeness = &ProtocolCompletenessView{
			Complete:        sn.Complete,
			WatermarkLedger: sn.Watermark,
		}
	}
	return v
}

// protocolEvents24h reads the per-source trailing-24h event counts,
// degrading to an empty map (every protocol reads 0) when the reader
// is nil or errors.
func (s *Server) protocolEvents24h(ctx context.Context) map[string]int64 {
	if s.protocolStats == nil {
		return nil
	}
	counts, err := s.protocolStats.CountRecentEventsBySource(ctx)
	if err != nil {
		s.logger.Warn("protocols events_24h read failed", "err", err)
		return nil
	}
	return counts
}

// protocolVerdicts reads the latest completeness verdict per source,
// degrading to an empty map (verdict summaries absent) when the reader
// is nil or errors.
func (s *Server) protocolVerdicts(ctx context.Context) map[string]timescale.CompletenessSnapshot {
	if s.completenessReader == nil {
		return nil
	}
	snaps, err := s.completenessReader.ListCompletenessSnapshots(ctx)
	if err != nil {
		s.logger.Warn("protocols completeness read failed", "err", err)
		return nil
	}
	out := make(map[string]timescale.CompletenessSnapshot, len(snaps))
	for _, sn := range snaps {
		out[sn.Source] = sn
	}
	return out
}

// protocolContracts returns name's registered instances in the unified
// wire shape: soroswap_pairs for soroswap, protocol_contracts for the
// factory-gated sources, empty for everything else (and on nil reader
// or read error — same degradation contract as the other joins).
func (s *Server) protocolContracts(ctx context.Context, name string) []ProtocolContractView {
	if name == "soroswap" {
		return s.soroswapContracts(ctx)
	}
	if s.protocolContractsReader == nil {
		return []ProtocolContractView{}
	}
	rows, err := s.protocolContractsReader.ListProtocolContracts(ctx, name)
	if err != nil {
		s.logger.Warn("protocols contract registry read failed", "source", name, "err", err)
		return []ProtocolContractView{}
	}
	if len(rows) == 0 {
		// The protocol_contracts registry is empty for this source (only blend
		// is seeded today). Fall back to the contracts the decoder has actually
		// captured into the projected table, so defindex/phoenix/comet/cctp/rozo
		// get a real roster + the lake analytics scoped to it.
		return s.protocolContractsFromProjection(ctx, name)
	}
	out := make([]ProtocolContractView, 0, len(rows))
	for _, row := range rows {
		out = append(out, ProtocolContractView{
			ContractID:  row.ContractID,
			FactoryID:   row.FactoryID,
			FirstLedger: row.FirstLedger,
		})
	}
	return out
}

// protocolContractsFromProjection is the registry-empty fallback: the distinct
// contracts from name's projected table (nil/empty when the source has no
// per-contract table — aquarius/sdex/oracles, which legitimately have no
// contract roster).
func (s *Server) protocolContractsFromProjection(ctx context.Context, name string) []ProtocolContractView {
	ids, err := s.protocolContractsReader.ListSourceContractsFromProjection(ctx, name)
	if err != nil {
		s.logger.Warn("protocols projection roster read failed", "source", name, "err", err)
		return []ProtocolContractView{}
	}
	out := make([]ProtocolContractView, 0, len(ids))
	for _, id := range ids {
		out = append(out, ProtocolContractView{ContractID: id})
	}
	return out
}

// soroswapContracts projects the soroswap_pairs registry into the
// unified contract shape (pair strkey as the instance, token pair
// attached, no factory column — the pairs table predates ADR-0035).
func (s *Server) soroswapContracts(ctx context.Context) []ProtocolContractView {
	if s.soroswapPairs == nil {
		return []ProtocolContractView{}
	}
	pairs, err := s.soroswapPairs.LoadSoroswapPairRegistry(ctx)
	if err != nil {
		s.logger.Warn("protocols soroswap pair registry read failed", "err", err)
		return []ProtocolContractView{}
	}
	out := make([]ProtocolContractView, 0, len(pairs))
	for _, p := range pairs {
		out = append(out, ProtocolContractView{
			ContractID: p.PairStrkey,
			Token0:     p.Token0Strkey,
			Token1:     p.Token1Strkey,
		})
	}
	return out
}
