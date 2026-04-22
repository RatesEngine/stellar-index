---
adr: 0010
title: Off-chain fiat currencies as AssetType "fiat"
status: Accepted
date: 2026-04-22
supersedes: []
superseded_by: null
---

# ADR-0010: Off-chain fiat currencies as `AssetType = "fiat"`

## Context

The aggregation layer needs to price Stellar assets against
off-chain fiat currencies (USD, EUR, GBP, JPY, CNY, BRL per the
RFPs). These are NOT Stellar assets — there's no `code+issuer`
(Circle's USDC is a classic Stellar asset, but "USD as a
reference currency" is not).

Oracles speak in fiat: Reflector's CEX contract emits "XLM/USD at
12.42" where USD is the abstract concept, not any specific issuer's
stablecoin. Similarly FX feeds (OANDA etc.) publish cross-pair
rates between fiat currencies.

Our canonical type `Asset` was designed for Stellar assets only —
three shapes: `native` / `classic (code+issuer)` / `soroban
(contract)`. When oracle code needed to express "USD", it fell
back to a **sentinel hack**:

```go
Asset{Type: AssetClassic, Code: "USD"}  // empty issuer = fiat sentinel
```

This shipped in the first `canonical.OracleUpdate` commit with a
`TODO(#0)` marker flagging the debt. [`canonical.Asset.Validate`]
specifically tolerates this form for a small allow-list; no other
code path does.

Consequences of leaving the hack:

- `Asset.String()` → `"USD-"` (empty issuer renders as trailing
  dash), which [`ParseAsset`] rejects as malformed. Round-trip
  JSON breaks for any response that includes a fiat-quoted asset.
- `validateFiatSentinel` duplicates the allow-list in two places.
- New developers see a "classic asset with no issuer" and assume
  it's a bug.

Time to fix it before more code touches the Asset type.

## Decision

**Extend [`canonical.AssetType`] with a fourth variant: `fiat`.**

```go
const (
    AssetNative  AssetType = "native"
    AssetClassic AssetType = "classic"
    AssetSoroban AssetType = "soroban"
    AssetFiat    AssetType = "fiat"   // NEW
)

type Asset struct {
    Type         AssetType
    Code         string     // "USD", "EUR", … for fiat; existing use for classic
    Issuer       string     // unused for fiat
    ContractID   string     // unused for fiat
}
```

Canonical wire form for fiat:

- **String form:** `fiat:USD`, `fiat:EUR`, etc. Unambiguous prefix
  means `ParseAsset` can dispatch in O(1).
- **Object form:** `{"type": "fiat", "code": "USD"}`.
- **SQL storage:** same text column as other assets; the `fiat:`
  prefix distinguishes it. Indexes on `(base_asset, quote_asset,
  ts)` work identically.

Allow-listed codes (ISO-4217 three-letter plus a small extension
set for the RFPs' "AQUA/BRL" case):

```
AUD BRL CAD CHF CNY EUR GBP HKD INR JPY KRW MXN NGN NZD RUB SGD TRY USD ZAR
```

New fiat codes require a one-line ADR amendment (added to this
document's amendments section; never a superseding ADR).
`canonical.IsKnownFiat(code)` exposes the allow-list to callers.

Validation:

```go
func (a Asset) Validate() error {
    switch a.Type {
    case AssetFiat:
        if a.Issuer != "" || a.ContractID != "" {
            return errors.New("fiat asset must not carry issuer/contract")
        }
        if !IsKnownFiat(a.Code) {
            return fmt.Errorf("%w: unknown fiat code %q", ErrInvalidAsset, a.Code)
        }
        return nil
    // ... existing cases
    }
}
```

## Consequences

**Positive**

- `Asset.String()` + `ParseAsset()` round-trip cleanly for every
  legitimate asset shape, including fiat. JSON round-trips don't
  break for oracle responses.
- API response for a fiat-quoted price is now self-describing:
  `{"quote": "fiat:USD"}` — a consumer parsing the string knows
  "this is a reference currency, not a Stellar token."
- Single allow-list in one function, one place. The old sentinel
  hack's duplicated allow-list comes out.
- API docs can describe the four `AssetType` values uniformly.
- No issuer-address trickery to signal "this isn't really a
  Stellar asset" — the type tag does it.

**Negative**

- Four variants instead of three. Every type-switch on
  `AssetType` needs a new case — we grep for `AssetSoroban` today
  to find them. CI check (TODO(#0)) to assert switch-coverage
  exhaustiveness would be tidy; Go 1.21+ has `analyzers/
  exhaustive` that can enforce it.
- Wire-contract change for clients who ingested the old sentinel
  form. Mitigation: we never shipped the old form — it was
  internal-only. Any external API response that would've emitted
  "USD-" is pre-launch; fix before v1 ships.
- Migration of the migration: if any trades/oracle_updates rows
  get written with the old `USD-` text before this lands, they
  need a UPDATE SET base_asset = 'fiat:USD' WHERE base_asset =
  'USD-'. Tracked in migrations/0004 (TBD).

**Operational impact**

- `internal/canonical/asset.go` — one new case in
  `Validate()`, `String()`, `ParseAsset()`, `MarshalJSON`/
  `UnmarshalJSON`, `Scan`/`Value`.
- Tests extend the existing table-driven cases by a fiat-fixture
  block.
- `internal/sources/cex/*` and `internal/sources/fx/*` (future)
  emit `AssetFiat` directly instead of the sentinel hack.
- `docs/reference/api-design.md §3` asset-identifier grammar
  updated to include the `fiat:CODE` form.

**Downstream design impact**

- The aggregator's triangulation code
  (`internal/aggregate/triangulate.go`, future) has to handle
  fiat ↔ fiat rates as well as fiat ↔ Stellar-asset rates. Clean
  separation between the two via the `AssetType` tag.
- Triangulation through a "synthetic USD anchor" (proposal §
  Cross-Pair Derivation) is now just `AssetType = fiat, Code =
  USD` — a real value, not a string match.

## Alternatives considered

1. **Keep the empty-issuer classic sentinel.** Rejected — the
   round-trip breakage for JSON is a real bug that'll get
   reported once we ship a `/v1/price?quote=USD` response
   publicly.

2. **Invent a well-known synthetic issuer G-address.** Something
   like `GAAAAAAAFIAT000000...` registered as the "off-chain"
   issuer. Rejected: (a) it'd be a valid-looking Stellar address
   that isn't actually on the network, risking confusion; (b)
   any code reading the issuer would have to string-match
   against this magic value, not type-dispatch — regressing on
   the exact thing this ADR fixes.

3. **Separate `FiatCurrency` type in a new package.** Rejected
   as over-engineering. Every `Pair` + `Trade` + `OracleUpdate`
   + API envelope now needs a union type (Go doesn't have sum
   types natively, so we'd implement one via interfaces or
   generic wrappers) for Base/Quote. Six files of scaffolding
   to buy nothing.

4. **Expand `AssetClassic` to allow empty issuer, code-match the
   fiat set.** Rejected as a special-case of the current sentinel
   hack with the same problems: breaks `ParseAsset` round-trip,
   forces two allow-lists in sync.

5. **Represent fiat as a Stellar contract asset.** The Chainlink
   approach — there's a "virtual" contract address that fiat
   prices go through. Rejected: that's them abusing their
   smart-contract platform's type system to model an
   abstraction. We don't have that constraint; our canonical
   type is defined by us.

## Migration from the sentinel (pre-v1 cleanup)

Because this is still pre-v1 and no data is written yet with the
sentinel shape, we do NOT need a data migration. Code migration
is:

1. Amend `canonical.AssetType` with the `AssetFiat` constant.
2. Update `Asset.String()`, `ParseAsset`, `Validate`,
   `MarshalJSON`, `UnmarshalJSON`, `Scan`, `Value` to handle the
   new variant.
3. Delete `isFiatSentinel` from `internal/canonical/oracle.go`
   and remove the allow-list acceptance in
   `OracleUpdate.Validate`. Replace with `a.Quote.Validate()` —
   the fiat variant now validates cleanly through the main path.
4. Add `canonical.IsKnownFiat(code)` + the allow-list in
   `asset_fiat.go` (new file; keeps the ISO list navigable).
5. Update tests: `oracle_test.go` fiat-sentinel test becomes a
   simple fiat-asset test.
6. Write a grep-friendly TODO comment on each `switch` over
   `AssetType` that didn't previously handle fiat — Go's
   exhaustive linter (if/when we enable it) will flag them
   automatically.

The implementation PR lands as `fix(canonical): AssetFiat variant
per ADR-0010` in the session that ratifies this ADR.

## Amendments

_Append new fiat codes here as a one-liner. Never supersede this
ADR for an addition._

- 2026-04-22 — initial allow-list of 19 codes.

## References

- Related ADRs:
  - ADR-0003 (i128 no-truncation) — amounts at fiat oracle prices
    are still `canonical.Amount`; `AssetType = fiat` doesn't
    change amount handling.
- Related docs:
  - [api-design.md §3](../reference/api-design.md#3-asset-identifier-grammar)
    — grammar extended with `fiat:CODE`.
  - [canonical/oracle.go](../../internal/canonical/oracle.go) —
    the TODO(#0) this ADR closes.
- External:
  - ISO 4217 — the currency-code reference.
  - Chainlink feeds — a reference for "virtual asset" naming
    conventions in oracle systems.
