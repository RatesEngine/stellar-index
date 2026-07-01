# Checklist — add an on-chain source (Soroban DEX / event decoder)

Reference implementation: `internal/sources/soroswap/`. Binding rules: CLAUDE.md
invariants #6 (ingest path), #7 (one writer per domain), ADR-0035 (gating).
**Before writing any helper, check `/CAPABILITY-INVENTORY.md` — reuse `internal/scval`,
`canonical.Amount/Trade/Asset`.**

## 1 — Create the package `internal/sources/<name>/`
- [ ] `README.md` — cross-link `docs/protocols/<name>.md`; document the event topic
      shapes + quirks.
- [ ] `events.go` — event-identifier constants + `classify()` topic predicates + the
      `consumer.Event` wrappers (`TradeEvent{Trade canonical.Trade}` with
      `EventKind()`/`Source()`, plus `var _ consumer.Event = TradeEvent{}` compile-check).
      Amounts are `canonical.Amount` (i128 → never int64, ADR-0003).
- [ ] `decode.go` — pure `events.Event → canonical.Trade`; decode SCVals via
      `internal/scval`; **decode by Map field NAME, not position** (schema-evolution rule).
- [ ] **`dispatcher_adapter.go` — the production seam.** Implement `dispatcher.Decoder`:
      `Name() string`, `Matches(events.Event) bool`, `Decode(events.Event) ([]consumer.Event, error)`,
      + `NewDecoder(...)`. **`Matches()` MUST gate on contract identity** (a registered
      pool/factory set), not topic bytes (ADR-0035 — topic symbols collide across every AMM).
- [ ] `*_test.go` — table-driven unit tests + a real-mainnet-fixture test walking
      `test/fixtures/<name>/<wasm-version>/` (copy `soroswap/real_fixture_test.go`).
      Regenerate fixtures with `scripts/dev/capture-<name>-fixtures.sh`.

## 2 — Wire it (6 edits — miss one and the source silently emits nothing)
- [ ] `internal/config/validate.go` → add the name to **`KnownSources`** (map ~L31).
- [ ] `internal/pipeline/dispatcher.go` → `BuildDispatcher`: `case <name>.SourceName:`
      appending `<name>.NewDecoder(...)` to `decoders` (or `opDecoders`/`callDecoders`).
- [ ] `internal/pipeline/sink.go` → `HandleEvent`: `case <name>.TradeEvent:` → your
      `persistTrade(...)` / `Store.Insert<X>` writer.
- [ ] `internal/pipeline/sink.go` → **`IsProjectedEvent`**: add your event types to the
      projected switch **if** this is a projected Soroban source (writes via `soroban_events`).
- [ ] `internal/projector/registry.go` → `buildSource`: `case <name>.SourceName:` returning
      a `Source{Decoder:…}` — required for any projected source.
- [ ] `internal/sources/external/registry.go` → `Registry` map: a `Metadata{Class, Subclass,
      IncludeInVWAP, BackfillSafe:false, …}` entry (`BackfillSafe` stays false until a WASM audit).

For a contract-gated (factory-anchored) source, also add a `GatedMeta` entry in
`internal/pipeline/gated_registry.go` and forward `gated[source]` in the two wiring points (see `blend`).

## 3 — Storage
- [ ] Add a migration (→ [add-migration.md](add-migration.md)) + the `Store.Insert<X>`
      writer/reader in `internal/storage/timescale`.

## Guards that will catch mistakes
`TestIsProjectedEvent_TableDriven` fails if the sink/projector arms drift; `config.Validate`
rejects an `enabled_sources` name missing from `KnownSources`; `lint-imports.sh` enforces boundaries.

## Done when
Unit + fixture tests pass; `bash scripts/dev/verify.sh` is green; enabling the source and
running against a known ledger range produces rows in its table. Catch-up is
`stellarindex-ops projector-replay -source <name> -from <ledger>` — **never** a bespoke
`<name>-backfill` subcommand.
