// Package discovery auto-detects new SEP-41 Soroban tokens from the
// event stream so the engine can start tracking them without a code
// change.
//
// Background: every SEP-41 token contract on pubnet emits events
// matching a small set of topic shapes (`transfer`, `mint`, `burn`,
// `clawback` per the SEP-41 spec). When the dispatcher observes one
// of these events from a contract id we don't yet know about, this
// package records it in `discovered_assets` so operators can:
//
//   - audit which contracts have been seen recently (rate-of-arrival
//     of new tokens informs whether the network is busy or
//     suspicious activity is happening);
//   - bootstrap downstream wiring (supply tracking, decoder
//     registration, asset-detail metadata fetch) without manual
//     contract-id curation.
//
// Discovery is read-only on the event side: the [Sniffer] inspects
// an [events.Event] and reports whether it's SEP-41-shaped without
// modifying the event. The [Recorder] interface persists new
// contract ids; production wiring is a Postgres-backed adapter
// against the discovered_assets hypertable.
//
// Package surface (current):
//
//   - [Sniff] — pure function that classifies an event's SEP-41
//     event-type based on topic[0]. Returns (contract_id, type, ok)
//     where ok=false for non-SEP-41 events.
//   - [Recorder] — write-side interface for persistence.
//   - [SEP41EventType] — enum identifying which SEP-41 event was
//     observed.
//
// Wired today:
//
//   - Dispatcher integration:
//     [internal/dispatcher/dispatcher.go]'s `dispatchOne` calls
//     [discovery.Sniff] on every event flowing through, after
//     decoder dispatch.
//   - Postgres-backed Recorder:
//     [internal/storage/timescale/discovery.go] implements
//     [Recorder] against the `discovered_assets` hypertable;
//     `cmd/ratesengine-indexer/main.go` adapts the Store to the
//     interface.
//   - Ops command: `ratesengine-ops discovery` (see
//     `cmd/ratesengine-ops/discovery.go`) — list, recent-window,
//     per-source counts.
//   - Alert metric: `ratesengine_ingestion_discovery_drops` per
//     `deploy/monitoring/rules/ingestion.yml` — fires when
//     discovery writes start failing.
package discovery
