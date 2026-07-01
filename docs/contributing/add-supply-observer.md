# Checklist — add a supply observer

Reference: `internal/sources/sac_balances/`. Design: `docs/architecture/supply-pipeline.md`
(the three-domain split: XLM / classic / SEP-41).

## 1 — Create `internal/sources/<observer>/`
- [ ] `doc.go` — package doc (supply observers use `doc.go`, **not** `README.md`).
- [ ] `events.go` — the `Observation`/`Event` `consumer.Event` type.
- [ ] `dispatcher_adapter.go` (+`_test.go`). `consumer.go`/`decode.go` optional — simple
      observers fold decode into the adapter.

## 2 — Pick the dispatcher hook by what the source emits
- `LedgerEntryChangeDecoder` — `LedgerEntry` mutations (accounts/trustlines/claimable/LP/SAC).
- `OpDecoder` — classic operations (e.g. change_trust).
- `Decoder` — Soroban contract events (e.g. SEP-41 mint/burn).

## 3 — Wire it
- [ ] `internal/pipeline/dispatcher.go` → `RegisterSupplyEntryDecoders` (entry observers) or
      `RegisterSupplyEventDecoders` (event observers), gated on the relevant `cfg.Supply.*` field.
- [ ] `internal/pipeline/sink.go` → `HandleEvent`: the `case <observer>.Observation:` write.
      Supply observers are **not** projected (they're in the `IsProjectedEvent` default/out-of-scope set).
- [ ] Migration (→ [add-migration.md](add-migration.md)) for the per-class hypertable; the reader
      (`StorageClassicSupplyReader` / `StorageSEP41SupplyReader`) aggregates at refresh time.
- [ ] `internal/config` → the `[supply.*]` field (watched accounts / assets / SAC-wrapper map).
- [ ] `cmd/stellarindex-indexer/main.go` → wire alongside the existing supply observers.

**Guard:** add an integration test under `test/integration/` if it touches NUMERIC arithmetic.
**Done when:** the observer emits `Observation`s against a known ledger and the per-class supply
rollup reflects them; `verify.sh` green.
