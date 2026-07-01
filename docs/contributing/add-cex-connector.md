# Checklist — add a CEX / FX connector (off-chain)

Reference: `internal/sources/external/binance/`. Framework: `internal/sources/external/framework.go`.
These are **NOT** dispatcher sources and do **NOT** go in `KnownSources` / `enabled_sources`
(they're gated by `cfg.<Venue>.Enabled` only). **Reuse the shared helpers — do not copy-paste:**
`external/scale` (`DecimalStringToScaledInt`, `Pow10`, `SyntheticTxHash`) and `external/wsclient`
(reconnect/backoff/jitter/pair-inversion). Check `/CAPABILITY-INVENTORY.md` first.

## 1 — Create `internal/sources/external/<venue>/`
- [ ] `events.go` — vendor wire types.
- [ ] `parse.go` — vendor JSON → `canonical.Trade`. **Set `Trade.Decimals` explicitly**
      (CEX = 10^8; FX pollers = 10^6). Scale via `external/scale`, do not re-implement.
- [ ] `streamer.go` (live WS/REST, implement `external.Streamer`, use `external/wsclient`
      for the reconnect loop) and/or a poller implementing `external.Poller`.
- [ ] `backfill.go` — historical OHLC, implement `external.Backfiller` (optional).
- [ ] `pairs.go` — symbol map (`DefaultPairs()`).
- [ ] `*_test.go` — inline golden frames (copy `binance/streamer_test.go`); there is no
      `test/fixtures/external/`.

## 2 — Wire it
- [ ] `internal/sources/external/registry.go` → `Registry`: `Metadata{Class:ClassExchange,
      Subclass:SubclassCEX|SubclassFX, IncludeInVWAP:true, BackfillSafe:true, …}`. Only
      `ClassExchange` contributes to VWAP.
- [ ] `internal/config/config.go` → a `<Venue>` field with `default:"false"` + a default entry.
- [ ] `cmd/stellarindex-indexer/main.go` → `buildExternal`: `if cfg.<Venue>.Enabled { … }`.
- [ ] `cmd/stellarindex-ops/main.go` → the **parallel** block + the backfill switch if `Backfiller`.

## Reuse, don't rebuild
`external.Runner` (`runner.go`) owns dust-drop/teardown/metrics; `external/wsclient` owns
reconnect/backoff. Implement only `Start`/`PollOnce` + frame-parse. **Do not hand-roll a WS
reconnect loop or a decimal-scaling helper** — that's the #1 duplication source (D3).

## Guards / done when
`external.Lookup` fail-closes an unregistered source to `IncludeInVWAP=false`. Done when
parse tests pass, the venue emits `stellarindex_external_poller_polls_total{...,outcome="success"}`
/ streamed trades, and `/v1/sources` classifies it correctly.
