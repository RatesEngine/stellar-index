# A08 — Canonical types + scval + events (READ-ONLY audit)

Scope: `internal/canonical/` (incl. `internal/canonical/discovery/`),
`internal/scval/`, `internal/events/` — all `.go` files including `*_test.go`.
(`internal/xdrjson` is out of scope — audited in A11.)

Auditor pass date: 2026-06-14. Method: full read of every in-scope file +
cross-check against the SDK (`go-stellar-sdk@v0.5.0`) `xdr` struct field types
(`Int128Parts`, `UInt128Parts`, `UInt256Parts`, `MuxedEd25519Account`,
`ClaimableBalanceId`) and `strkey` version-byte constants to verify the i128 /
address-encoding invariants. All four packages compile + their test suites pass
(`go test ./internal/canonical/... ./internal/scval/... ./internal/events/...`
= ok).

**Files read: 33 / 33** — canonical: 23 (amount, asset, asset_crypto,
asset_fiat, asset_rwa, oracle, pair, strkey, trade, doc, errors + their tests +
amount_edge_test + pair_validate_test); discovery: 6 (doc, recorder, sink,
sniffer + 2 tests); scval: 3 (scval + scval_test + must_encoders_test);
events: 2 (event + event_test). Plus 2 SDK files consulted
(`xdr/xdr_generated.go` for the 128/256-parts + ScAddress arm structs,
`strkey/main.go` for the version bytes / Encode bounds).

Headline: **this is the foundational i128-safety + identity layer and it is in
very good shape.** No Critical, no High. The i128 no-truncation invariant
(ADR-0003) is provably honoured on every path. Findings are a cluster of Low
items (one doc/behaviour mismatch, two latent-correctness niceties, two
test/coverage gaps) plus one Medium on `ParseAsset` strkey-arity leniency.

---

## Findings

| severity | file:line | dim | issue | why it matters | suggested fix | conf |
|---|---|---|---|---|---|---|
| medium | asset.go:266-277 (`ParseAsset`) | D1 | The classic-asset fallback splits on the first `-`/`:` and accepts ANY non-empty `issuer` substring without validating it is a G-strkey at parse time — `NewClassicAsset` *does* call `validateAccountID`, so a bad issuer is rejected, BUT a string like `"USDC-GARBAGE"` returns a clear strkey error whereas `"M…-…"`/`"B…"`/`"L…"` muxed/CB/LP strkeys (which contain no `-`/`:`) fall through to the final `does not match any known asset format` error. So a SEP-41 transfer destination that is a valid M/B/L strkey is NOT parseable as an Asset even though `AsAddressStrkey` happily produces those forms. | Asymmetry: `scval.AsAddressStrkey` emits 5 address kinds (G/C/M/B/L) but `ParseAsset` only round-trips G (classic issuer) + C (soroban). An M/B/L address can never be the issuer of a classic asset (correct), and they are not assets — so this is *defensible*. The risk is a future caller assuming `ParseAsset(AsAddressStrkey(x))` round-trips for all 5, which it does not. Today no such caller exists in-scope. | Either document on `ParseAsset` that only native/classic(G-issuer)/soroban(C)/fiat/crypto/rwa are accepted (M/B/L are holder addresses, never asset identifiers), or add an explicit early reject for M/B/L-prefixed input with a clearer error. Doc-only is fine. | med |
| low | discovery/sink.go:95-99 (`AsyncSink.Start`) | D4/D9 | Docstring says "Start launches the drain worker. **Idempotent; calling twice is a no-op.**" but the body is just `go s.run()` with no `sync.Once`/guard. A second `Start()` would spawn a second goroutine ranging the same channel — double-draining, and `Stop` (which `close(s.ch)` + waits on a single `done`) would only join one of them. | Not a live bug: every production call site (`cmd/stellarindex-indexer/main.go:319`, ops backfill, tests) calls `Start()` exactly once per sink. It is a doc-vs-behaviour mismatch that would bite anyone who trusts the "idempotent" claim. The sibling `sorobanevents.AsyncSink.Start` has the same shape. | Add a `sync.Once` around `go s.run()` to make the docstring true, or fix the docstring to "must be called exactly once." | low |
| low | discovery/sink.go:36-49,110-133 (`AsyncSink.seen`) | D5 | The in-process dedup set `seen map[string]struct{}` grows once per unique `(contractID, eventType)` and is never pruned for the life of the process. Every distinct SEP-41 contract ever observed accumulates a ~60-byte key forever. | On a long-running indexer the number of distinct SEP-41 contracts on pubnet is bounded (low tens of thousands), so this is a small bounded set, not a true leak — but it is unbounded *in principle* and the docstring frames the buffer as the only safety net. The roll-back-on-drop logic (correctly) keeps it from leaking on transient drops. | Acceptable as-is given the bound; if ever a concern, cap the set (LRU) or rely solely on the Recorder's own `IsKnown` fast-path. Note in the docstring that `seen` is process-lifetime-unbounded. | low |
| low | scval.go:435-445 (`MapField`) | D1 | `MapField` only matches map entries whose **key is a `Symbol`** (`Key.Type != ScValTypeScvSymbol → continue`). A Soroban `#[contracttype]` struct is always Symbol-keyed so this is correct for every current decoder, but a contract that emits a `Map<String, …>` or `Map<u32, …>` body would silently miss every field and `MustMapField` would return `ErrScValMissingKey` rather than a "wrong key type" signal. | The behaviour is documented ("looks up a map entry whose key is Symbol(key)") and matches the decode-by-name rule, so this is by-design. Flagged only because the silent skip of non-Symbol keys could mask a genuine schema change (contract switches a map key type) as a "missing field" rather than a distinct error class. | None required. Optionally, in `MustMapField`, distinguish "key present but non-Symbol" if a future contract type warrants it. | low |
| low | scval.go:128-152 (`DecodeScVecToArgs`) / scval.go:96-111 (`EncodeArgsAsScVec`) | D8 | `EncodeArgsAsScVec` ↔ `DecodeScVecToArgs` are an encode/decode pair powering the soroban_events op_args round-trip (ADR-0029 / projector-replay), but there is **no test for either** in `scval_test.go` (the suite covers Parse/ParseBytes/As*/MapField/AsTupleN/DecodeAddressOrSymbol but skips these two). | These two are load-bearing for `projector-replay` and the lake-replay rebuild paths — a regression (e.g. the nil-empty → SQL NULL contract at `EncodeArgsAsScVec:97-99` / `DecodeScVecToArgs:129-130`, or the `**sv.Vec` double-deref at :142) would only surface in an integration replay, not a unit test. Pure coverage gap, no observed bug; the code reads correct. | Add a round-trip test: `EncodeArgsAsScVec([]string{a,b}) → DecodeScVecToArgs → []string{a,b}`, plus empty-input → `(nil,nil)` both ways, plus the "bytes aren't a ScVec" error arm. | low |
| low | oracle.go:46-100 (`OracleUpdate`) + canonical/doc.go:1-2 | D9 | The prompt + `doc.go` package comment name a `canonical.Price` type ("Trade, Price, Asset, Pair") but there is **no `Price` type** — the price-bearing canonical type is `OracleUpdate` (carrying an `Amount`-typed `Price` field). `doc.go:2` literally says "Trade, Price, Asset, Pair" as if `Price` were a struct. | Minor doc drift: a reader grepping for `type Price` finds nothing and may assume it is missing. The actual design (raw integer `Price Amount` + source-declared `Decimals`, scale-on-read) is sound. | Update `doc.go` to say "Trade, OracleUpdate, Asset, Pair" (or "…the price-bearing OracleUpdate…") so the package overview matches reality. | low |

---

## Verified CORRECT (provable coverage)

Recorded so the next pass doesn't re-litigate.

**D2 / D3 — the i128 no-truncation invariant (ADR-0003) — holds on every path:**
- `canonical.FromInt128Parts(hi int64, lo uint64)` composes via big.Int
  arithmetic (`h.Lsh(h,64); Add(h,l)`), NOT bit-truncation — two's-complement
  sign propagation is correct. Confirmed by the KALIEN-incident regression
  (`amount_test.go:24`, hi=2/lo=3106517825480896768 → "40000005972900000000")
  and the negative cases (hi=-1 → -1/-42). (amount.go:58-67)
- `FromUInt128Parts` / `FromUInt256Parts` likewise compose all words via big.Int
  (amount.go:71-93); u128-max + u256-max round-trips pinned
  (amount_test.go:109-112, amount_edge_test.go:87-94).
- `scval.AsAmountFromI128` does `canonical.FromInt128Parts(int64(p.Hi), uint64(p.Lo))`.
  Cross-checked the SDK: `xdr.Int128Parts{Hi Int64; Lo Uint64}` — so
  `int64(p.Hi)` and `uint64(p.Lo)` are identity-width conversions, **not**
  truncations. `AsAmountFromU128` (`UInt128Parts{Hi Uint64; Lo Uint64}`) and
  `AsAmountFromU256` (`UInt256Parts{HiHi,HiLo,LoHi,LoLo Uint64}`) are the same —
  every word is a `Uint64→uint64` identity convert. **No `int64(parts.Lo)`
  anywhere in scope** (grepped). (scval.go:309-341)
- `Amount` is `*big.Int`-backed; JSON marshals to a **string** always
  (`MarshalJSON → json.Marshal(a.String())`, amount.go:169-171), never a JSON
  number — pinned by `TestAmount_JSONRoundTrip` across i64-max/u64-max+1/u128-max
  /negatives + a guard that the wire byte[0] is `"`. SQL `Value()` emits the
  decimal string for NUMERIC; `Scan` handles string/[]byte/int64/nil. The
  `Trade` and `OracleUpdate` JSON round-trips both assert i128/u128-scale
  precision survives (trade_test.go:141, oracle_test.go:174).

**D3 — DoS / panic resistance on parse paths reachable from input:**
- `FromString` caps input at `MaxAmountStringLen=512` digits BEFORE
  `big.Int.SetString` (amount.go:110-123), foreclosing the multi-MB-string →
  unbounded-alloc path; the cap is inherited by `UnmarshalJSON` and `Scan`.
  Both at-cap-passes and over-cap-rejects are tested (amount_test.go:193-222).
- `scval.Parse` / `ParseBytes` wrap every XDR/base64 failure in
  `ErrScValDecode` and return — no panic on truncated/garbage input
  (scval_test.go `TestParse_badBase64` / `_truncated` / `ParseBytes_truncated`).
- All `As*` accessors type-check `sv.Type` and return `ErrScValType` before
  dereferencing the union arm pointer — no nil-deref on a wrong-typed ScVal.
  Every accessor has a `_wrongType` test.
- The ONLY panics in scope are deliberate Must-style programmer-error guards,
  each correctly scoped to compile-time constants / decoder invariants:
  - `scval.MustEncodeSymbol`/`MustEncodeString` — panic on bad constant;
    `EncodeSymbol` enforces ScSymbol bounds (len 1..32, ASCII identifier)
    so it fails fast at encode (scval.go:163-240). Pinned by must_encoders_test.
  - `canonical.FanoutOpIndex` — panics on op/event index <0 or >0xFFFF; this is
    intentional (a 16-bit overflow would manufacture exactly the trades-PK
    collisions the function exists to prevent — better loud than a silent
    ON CONFLICT drop). Pinned by `TestFanoutOpIndex_OutOfRangePanics`
    (trade.go:101-109, trade_test.go:193-212). NOT reachable from untrusted
    input — opIndex/eventIndex come from the dispatcher walk, not user data.

**D1 — `AsAddressStrkey` for all 5 ScAddress variants (CAP-67 / P23):**
- Account (G), Contract (C), MuxedAccount (M), ClaimableBalance (B),
  LiquidityPool (L) all handled; `default` arm returns `ErrScValType` with the
  numeric type rather than dropping (the 2026-05-28 "unknown ScAddress type 4"
  LP-drop regression). Each delegates checksum+format to the SDK `strkey`
  package — no "first char is G so it's an account" shortcut. (scval.go:363-396)
- Muxed payload composition verified against SDK
  `MuxedEd25519Account{Id Uint64; Ed25519 Uint256}`: 40 bytes = 32-byte ed25519
  || 8-byte big-endian Id (`binary.BigEndian.PutUint64(raw[32:], uint64(m.Id))`)
  — matches SEP-23 muxed strkey. `uint64(m.Id)` is an identity convert.
- CB payload: 33 bytes = 1 type byte (`byte(cb.Type)`, V0=0) || 32-byte
  `*cb.V0` Hash, with an explicit nil-V0 guard before deref (scval.go:383-385).
  LP payload: 32-byte PoolId directly. All 5 pinned by `scval_test.go`
  (account/contract/muxed/claimableBalance/liquidityPool) and the SDK `strkey`
  version-byte constants confirmed (G=6<<3, C=2<<3, M=12<<3, B=1<<3, L=11<<3).

**D1 — ParseAsset for all asset shapes + canonical-string round-trip:**
- All 6 shapes parse + round-trip: native, classic (CODE-ISSUER + the CODE:ISSUER
  colon alias), soroban (C…), `fiat:`, `crypto:`, `rwa:`. The `XLM`/`xlm`/`NATIVE`
  case-insensitive native alias (F-0024) is handled (asset.go:247).
  `String()` always emits dash-form for classic regardless of input separator.
- Prefix dispatch order is sound: `fiat:`/`crypto:`/`rwa:` `CutPrefix` checks run
  before `IsContractID` (C-strkey) which runs before the classic `-`/`:` split.
  A classic code is alphanumeric-only (validated) so it can never contain `:` to
  collide with a prefix; a C-strkey has no `-`/`:` so it never mis-routes to
  classic. Verified empty/garbage/`USDC-`/`-ISSUER` all error
  (asset_test.go:99-131).
- `validateClassicAssetCode` enforces len 1..12 + `[a-zA-Z0-9]` only — emoji,
  space, hyphen, colon, null-byte, non-ASCII all rejected (asset_test.go:46-74).
- Strkey validation (`IsAccountID`/`IsContractID`/`IsMuxedAccount`/
  `IsClaimableBalance`/`IsLiquidityPool`) delegates to `strkey.Decode` →
  CRC-16 + version-byte checked, NOT regex. False-positive risk closed:
  CRC-mismatch and wrong-prefix both rejected (strkey_test.go:31-66). `IsAnyHolder`
  unions all 5 for the SEP-41 transfer from/to boundary.

**D1 — Trade / OracleUpdate / Pair identity + validation:**
- `Trade.Validate`: non-empty source, 64-char *lowercase* hex tx_hash
  (uppercase/mixed-case rejected to avoid Postgres PK dual-rows), non-zero
  timestamp, valid pair, base/quote both strictly positive. `Ledger=0` is
  intentionally allowed (off-chain venues) — documented + tested.
  `validTxHash` is a hand-rolled lowercase-hex check, correct (trade.go:188-199).
- `Trade.ID` / `OracleUpdate.ID` formats match the documented hypertable PKs;
  `Equal` compares storage-identity only (Maker/Taker/Confidence/Observer
  excluded) — pinned.
- `FanoutOpIndex` packs opIndex<<16 | eventIndex, no collisions across the
  tested grid, boundary 0xFFFF/0xFFFF → 0xFFFFFFFF clean (trade_test.go:165-189).
- `OracleUpdate.Validate` rejects zero/neg price, decimals>38 (NUMERIC limit),
  NaN/out-of-range confidence (NaN explicitly, since every NaN comparison is
  false), and validates Observer (G) + ContractID (C) only when non-empty.
  `PriceFloat` correct incl. the decimals=0 short-circuit.
- `Pair.Validate` rejects same-asset (ErrPairMismatch) + propagates base/quote
  errors with the right fragment; `Flip`/`Equal`/`EqualEitherWay` directional
  semantics correct (`XLM/USD != USD/XLM`); `ParsePair` uses `LastIndex("/")`
  which is safe because no asset form contains `/`. All three Validate branches
  directly pinned (pair_validate_test.go).

**D2 — XLM dual-form + crypto/classic distinction:**
- `crypto:XLM` (off-chain ticker) and `native` (Stellar XLM) are deliberately
  distinct canonical forms and compare unequal — consistent with the MEMORY
  "XLM dual-form" note (that note is about `native`↔`crypto:XLM` at the *read*
  layer; canonical correctly keeps them separate identities). `crypto:USDC` vs
  classic `USDC-G…` distinctness pinned (asset_crypto_test.go:61-68).
- Allow-list variants (fiat/crypto/rwa) are case-sensitive, non-overlapping
  (SolvBTC∈crypto∉rwa, BENJI∈rwa∉crypto), and each rejects issuer/contract_id
  fields via `Asset.Validate`. Stablecoin-as-fiat is correctly left to the
  aggregator (decoders keep EURC/USDC/MXNe etc. as `crypto:`), matching CLAUDE.md.

**D1 — events.Event transport type + OpArgs:**
- `Event` is the single transport-neutral Soroban event shape; `EventIndex` is
  `json:"-"` (dispatcher-only, never marshalled → RPC fixture replays stay
  byte-identical) and is the field that makes the soroban_events PK unique per
  multi-event op (the ADR-0033 Phoenix-8→1 fix). `OpArgs` is `omitempty`
  (Redstone feed-id zip path). Both round-trips + the omitempty/present split
  pinned (event_test.go).
- `EventClosedAt` fail-closes on empty/`bad-format` LedgerClosedAt (returns
  error, never zero-time) and uses `time.RFC3339` — matches the dispatcher's
  producer format (`dispatcher.go:522` `.Format(time.RFC3339)`); Go's RFC3339
  parser also tolerates fractional seconds from RPC fixtures, so no precision
  mismatch. Pinned (event_test.go:14-58).

**D4 — discovery AsyncSink concurrency (aside from the Low above):**
- `Push` is non-blocking, lock-guarded on `seen`/`dropped`/`skipped`; drop
  rolls back the seen-mark so a transient outage doesn't permanently lose a
  contract (pinned `TestAsyncSink_DropRollsBackSeen`). `Stop` is `sync.Once`-
  guarded + idempotent (pinned). `run` worker survives per-record errors
  (pinned). The race-detector-friendly tests pass.

---

## Cross-cutting note

This package set is "package zero" (every other package depends on it). The
audit found no defect that propagates a wrong value downstream — the i128
invariant, strkey validation, and identity/PK formats are all sound and
well-tested. The Medium (`ParseAsset` M/B/L leniency) is a documentation/
round-trip-symmetry concern, not a data-correctness bug. The Lows are polish:
one false "idempotent" docstring, one process-lifetime-bounded map, two
test-coverage gaps (`EncodeArgsAsScVec`/`DecodeScVecToArgs` round-trip), and one
stale `doc.go` reference to a non-existent `Price` type.
