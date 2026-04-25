// Package consumer defines the [Event] sum-type interface that
// every source's emitted value implements, plus a legacy
// [Source] + [Orchestrator] pair from the pre-dispatcher
// architecture.
//
// # What's load-bearing today
//
// [Event] is the type the indexer's event sink type-switches on
// to attribute each row to its source. Concrete shapes — e.g.
// `soroswap.TradeEvent`, `reflector.UpdateEvent`,
// `external.TradeEvent` — are defined in the source packages
// and all satisfy this interface.
//
// Every value emitted by a decoder, whether dispatched through
// the [internal/dispatcher] hot path or produced by an
// [internal/sources/external] connector goroutine, lands on a
// `chan consumer.Event` and gets sunk by
// `cmd/ratesengine-indexer`.
//
// # What's legacy
//
// [Source] and the orchestrator in this package come from the
// pre-2026-04-23 architecture, when each venue ran its own
// goroutine speaking stellar-rpc and exposed
// [Source.StreamLive] + [Source.BackfillRange] methods. That
// path was retired when r1 dropped stellar-rpc — see
// CLAUDE.md's binding rule "Ingest goes via Galexie →
// dispatcher → decoder. Never stellar-rpc."
//
// The interface and orchestrator are still in tree because:
//
//   - The `external/` connectors (CEX / FX / aggregator pollers)
//     genuinely need a per-venue goroutine model; they're not
//     dispatched from a ledger-meta walk. They use a sibling
//     framework in `internal/sources/external/runner.go`,
//     not this orchestrator, but the shape rhymes.
//   - The legacy code path is the simplest reference for
//     anyone designing a future per-source goroutine that
//     needs cursor + restart + backoff semantics.
//
// New on-chain sources should NOT implement [Source] —
// they register a [dispatcher.Decoder] / [dispatcher.OpDecoder]
// / [dispatcher.ContractCallDecoder] instead.
//
// # Invariants for Event values (current)
//
//   - Every emitted event wraps a fully-formed value from
//     [internal/canonical] — never a partial / unvalidated
//     struct.
//   - Amount fields are *big.Int via canonical.Amount. See
//     ADR-0003.
//
// # Adding a new on-chain source
//
//  1. Create internal/sources/<name>/ with events.go +
//     decode.go + consumer.go + dispatcher_adapter.go +
//     tests. Follow the existing sources as templates.
//
//  2. Define a TradeEvent / UpdateEvent in `events.go`:
//
//     type TradeEvent struct{ Trade canonical.Trade }
//     func (TradeEvent) EventKind() string { return "<name>.trade" }
//     func (e TradeEvent) Source() string  { return e.Trade.Source }
//     var _ consumer.Event = TradeEvent{}
//
//  3. Wire `dispatcher_adapter.go` to register the right
//     seam (Decoder / OpDecoder / ContractCallDecoder).
//
//  4. Add the source name + builder to
//     cmd/ratesengine-indexer/main.go's `buildDispatcher()`.
//
//  5. Add real-fixture tests under test/fixtures/<name>/.
package consumer
