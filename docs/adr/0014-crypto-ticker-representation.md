---
adr: 0014
title: Crypto tickers as AssetType "crypto"
status: Accepted
date: 2026-04-23
accepted: 2026-04-23
supersedes: []
superseded_by: null
---

# ADR-0014: Crypto tickers as `AssetType = "crypto"`

## Context

Reflector's CEX oracle (`CAFJZQWS…`) emits prices for off-chain
crypto assets (BTC, ETH, USDT, SOL, XRP, ADA, AVAX, DOT, LINK,
USDC, …) as `Asset::Other(Symbol)` on-chain. The Symbol is a bare
ticker like `"BTC"` — not a Stellar classic asset (no issuer), not
a Soroban contract, and not a fiat currency in the ISO-4217 sense.

Our canonical model today has four `AssetType` variants — native,
classic, soroban, fiat (ADR-0010). None of them fit a crypto
ticker:

- `classic`: requires an issuer G-address. BTC as a reference has
  no issuer.
- `soroban`: requires a C-address. Reflector doesn't provide one —
  BTC is referenced by ticker alone.
- `fiat`: semantically wrong (BTC is not fiat) and bound to an
  ISO-4217 allow-list that excludes crypto.

PR 164a's initial decoder path rejected these symbols with
`ErrUnknownFiatSymbol`, skipping 100% of CEX-oracle updates. PR 164d's
real-fixture harness `t.Skip`ed all three CEX fixtures under
`test/fixtures/reflector/v6-2026-04-23/` with a pointer at this
ADR. Time to fix.

## Decision

**Extend `canonical.AssetType` with a fifth variant: `crypto`.**

```go
const (
    AssetNative  AssetType = "native"
    AssetClassic AssetType = "classic"
    AssetSoroban AssetType = "soroban"
    AssetFiat    AssetType = "fiat"
    AssetCrypto  AssetType = "crypto"  // NEW
)

type Asset struct {
    Type         AssetType
    Code         string  // "BTC", "ETH", ... for crypto; existing use preserved
    Issuer       string  // unused for crypto/fiat
    ContractID   string  // unused for crypto/fiat
}
```

Canonical wire form for crypto:

- **String form:** `crypto:BTC`, `crypto:ETH`, etc. Unambiguous
  prefix so `ParseAsset` dispatches in O(1), same pattern as
  `fiat:USD`.
- **Object form:** `{"type": "crypto", "code": "BTC"}`.
- **SQL storage:** text column, `crypto:` prefix distinguishes it.

Allow-listed codes (observed in mainnet Reflector CEX traffic
2026-04-23, plus the largest-cap tickers not yet seen but very
likely to appear):

```
ADA ATOM AVAX BCH BNB BTC DOGE DOT ETH LINK LTC MATIC NEAR SHIB
SOL TON TRX UNI USDC USDT XLM XRP
```

Extension is a one-line amendment to this ADR (same pattern as
ADR-0010's fiat list).

## Consequences

- **Positive:** Reflector CEX feed's 10 symbols-per-event decode
  end-to-end. All three variants of `Asset::Other(Symbol)` — fiat,
  crypto, unknown — now have explicit handling in the decoder.
- **Positive:** `crypto:USDC` ≠ `USDC:GA5ZSEJY…` (Circle's Stellar
  classic asset). The canonical model keeps them distinct even
  though they share the `USDC` ticker. A price quoted against
  "USDC the global crypto asset" and a price quoted against
  "Circle's Stellar USDC classic asset" are semantically different;
  making them textually different prevents accidental mixing.
- **Negative:** Adds one more variant callers must handle in
  `switch asset.Type` ladders. Mitigated by keeping the allow-list
  small.
- **Negative:** Tickers are ambiguous without context. BTC on
  Reflector = BTC on Binance = BTC on Coinbase. We rely on oracle
  metadata (`base()`, data source) to know which backing market the
  price represents. Not a new problem — Reflector itself doesn't
  encode the venue in the event.
- **Operational impact:** Minimal. Storage is the same text
  column; pair IDs in Timescale grow a few new variants.
- **Downstream design impact:**
  - Pricing aggregation logic that cross-pairs crypto assets must
    use `crypto:BTC` as the key, not bare `BTC`. No free-form
    string matching.
  - Eventually, a **SAC bridge** might map `crypto:USDC` to
    Circle's on-chain `USDC-GA5ZSEJY…` for cross-venue arbitrage —
    but that's a registry decision, not an Asset type decision.
    See ADR-0010 §SAC bridge for prior art.

## Alternatives considered

1. **Broaden `AssetFiat` to `AssetExternalRef` (rename + relax
   allow-list)** — rejected. Fiat and crypto have different
   semantic properties (ISO-4217 vs tradable token). Renaming
   loses the type-level clarity that `fiat:USD` gives us today.
   If we ever need to represent "generic off-chain reference" we
   add a third variant; we don't conflate fiat with crypto.

2. **Use `AssetSoroban` with a synthetic contract ID** — rejected.
   Would require minting a fake C-address per ticker; breaks the
   invariant that `AssetSoroban.ContractID` is a real on-chain
   address.

3. **Encode as `classic` with empty issuer** — rejected for the
   same reason ADR-0010 rejected it for fiat: the empty-issuer
   string round-trips through JSON as `"BTC-"`, which `ParseAsset`
   rejects. Round-trip breakage is a cardinal sin of serialization
   design.

4. **Leave it and skip crypto symbols permanently** — rejected.
   Reflector's CEX oracle is a deliberate integration target
   (RFP coverage); skipping it defeats the purpose.

## Amendments

_Append new crypto codes here as a one-liner. Never supersede this
ADR for an addition._

- 2026-04-23 — initial allow-list of 22 codes (observed in CEX
  oracle traffic + top-cap global cryptos). See
  `canonical.IsKnownCrypto` for the live list.

## References

- Related ADRs:
  - ADR-0010 (fiat representation) — same pattern; crypto is the
    sibling variant.
  - ADR-0003 (i128 no-truncation) — crypto amounts still flow
    through the i128 path; no change.
- Discovery doc: [docs/discovery/oracles/reflector.md](../discovery/oracles/reflector.md)
  §CEX feed — lists observed asset tickers.
- Implementation: `internal/canonical/asset_crypto.go` (allow-list
  + constructor), `internal/canonical/asset.go` (type + String +
  ParseAsset + Validate).
