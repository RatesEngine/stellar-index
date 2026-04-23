// Package consumer defines the stable interface every source
// indexer implements — SDEX, Soroswap, Aquarius, Phoenix, Comet,
// Blend, Reflector, Redstone, Band, CEX connectors, FX feeds,
// and reference aggregators.
//
// # Contract
//
// A Source has two orchestration modes:
//
//   - [Source.StreamLive]     — subscribes to the live source and
//     emits canonical trades / prices as they happen.
//   - [Source.BackfillRange]  — walks a bounded historical range
//     and emits every matching canonical trade / price for replay.
//
// Both emit on the same output channel type. Consumers treat
// them uniformly; orchestration in cmd/ratesengine-indexer
// decides which mode to run per source.
//
// # Invariants
//
//   - Every emitted event is a fully-formed value from
//     internal/canonical — never a partial / unvalidated struct.
//   - Amount fields are *big.Int via canonical.Amount. See
//     ADR-0003.
//   - Sources honour ctx.Done() promptly. No unbounded blocking.
//   - Sources are safe to Stop + re-create; orchestrator
//     restarts on unrecoverable error and feeds the resumption
//     cursor back in.
//
// # Adding a new source
//
// Short form (see existing sources in internal/sources/ for
// templates):
//
//  1. Create internal/sources/<name>/ with doc.go + events.go +
//     decode.go + consumer.go + source_test.go. Follow the
//     five-file convention of soroswap / aquarius / phoenix /
//     reflector.
//  2. Implement [Source] on a `*Source` struct. Add compile-time
//     assertions at the bottom of consumer.go:
//         var _ consumer.Source = (*Source)(nil)
//         var _ consumer.Event  = TradeEvent{}
//  3. Register the source name in cmd/ratesengine-indexer/main.go's
//     buildSources() switch.
//  4. Add golden-file fixtures under test/fixtures/<name>/ when
//     the real SCVal decoder wiring is ready.
package consumer
