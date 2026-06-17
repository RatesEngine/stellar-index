---
adr: 0028
title: Tokenized real-world assets as AssetType "rwa"
status: Accepted
date: 2026-05-22
accepted: 2026-05-27
supersedes: []
superseded_by: null
---

# ADR-0028: Tokenized real-world assets as `AssetType = "rwa"`

## Context

RedStone's Stellar push-feed deployment is **19 per-feed contracts**.
8 of them price crypto / stablecoin assets the decoder already
models. The other 11 price
**tokenized real-world assets**: tokenized treasuries and
money-market funds, tokenized-BTC variants, and an inverse equity
ETF.

Two problems block ingesting those 11 (task #53):

### 1. `feed_id` ≠ display name

The per-feed contract's `feed_id()` getter is the exact string the
relayer passes in `write_prices(updater, feed_ids, payload)` — the
string our decoder must match. Captured on-chain 2026-05-22 via
`stellar contract invoke … -- feed_id`, five of the 19 differ from
their display name:

| Display | on-chain `feed_id` |
| --- | --- |
| EUROC  | `EUROC/EUR` |
| BENJI  | `BENJI_ETHEREUM_FUNDAMENTAL` |
| iBENJI | `iBENJI_ETHEREUM_FUNDAMENTAL` |
| SolvBTC/FUNDAMENTAL     | `SolvBTC_FUNDAMENTAL` |
| SolvBTC.BBN/FUNDAMENTAL | `SolvBTC.BBN_FUNDAMENTAL` |

The pre-#53 decoder matched `canonical.IsKnownCrypto(feedID)`. Because
the EUROC feed_id is `EUROC/EUR` — not the allow-list entry `EUROC` —
**EUROC never decoded**. Live RedStone coverage was 7 feeds, not 8.

### 2. RWA assets do not fit any existing `AssetType`

`canonical.AssetType` has five variants — native, classic, soroban,
fiat (ADR-0010), crypto (ADR-0014). A tokenized US-Treasury fund like
BENJI is none of them:

- `crypto` (ADR-0014) is semantically wrong. BENJI, GILTS, CETES,
  KTB, TESOURO, USTRY are tokenized **government debt / money-market
  funds**; SPXU is an inverse **equity** ETF. Lumping them into
  `crypto` pollutes every crypto-scoped surface (the explorer's
  crypto views, crypto aggregations, the verified-currency
  catalogue). A tokenized T-bill is not a cryptocurrency.
- `classic` / `soroban` need an issuer G-address / contract
  C-address. RedStone references these by ticker alone.
- `fiat` is an ISO-4217 reference currency. An RWA is a tradable
  instrument, not a currency.

This is the same structural gap ADR-0010 solved for fiat and
ADR-0014 solved for crypto: a bare-ticker reference with no on-chain
identity. RWA is the third sibling.

## Decision

### 1. Extend `canonical.AssetType` with a sixth variant: `rwa`

```go
const (
    AssetNative  AssetType = "native"
    AssetClassic AssetType = "classic"
    AssetSoroban AssetType = "soroban"
    AssetFiat    AssetType = "fiat"
    AssetCrypto  AssetType = "crypto"
    AssetRWA     AssetType = "rwa"     // NEW
)
```

Wire form `rwa:<CODE>` (e.g. `rwa:BENJI`) — unambiguous prefix,
`ParseAsset` dispatches in O(1), identical pattern to `fiat:` and
`crypto:`. Object form `{"type": "rwa", "code": "BENJI"}`. SQL
storage: the same text column; the `rwa:` prefix distinguishes it.

Allow-listed RWA codes (the 8 RWA feeds RedStone publishes on
Stellar mainnet, 2026-05-22):

```
BENJI  iBENJI  GILTS  CETES  KTB  TESOURO  USTRY  SPXU
```

`internal/canonical/asset_rwa.go` holds the allow-list +
`NewRWAAsset` constructor — mirroring `asset_crypto.go`. Extending
the list is a one-line amendment to this ADR.

### 2. The RedStone 19-feed registry

The decoder replaces the `IsKnownCrypto` match with an explicit
registry keyed on the **exact** `feed_id`. The quote rule: a feed_id
of the form `<BASE>/<QUOTE>` is `<QUOTE>`-denominated; all others are
USD (the RedStone convention — only EUROC carries an explicit
suffix).

| feed_id | base asset | quote |
| --- | --- | --- |
| `BTC` `ETH` `USDC` `XLM` `PYUSD` | `crypto:<id>` | USD |
| `EUROB` `MXNe` | `crypto:<id>` | USD |
| `EUROC/EUR` | `crypto:EUROC` | **EUR** |
| `BENJI_ETHEREUM_FUNDAMENTAL` | `rwa:BENJI` | USD |
| `iBENJI_ETHEREUM_FUNDAMENTAL` | `rwa:iBENJI` | USD |
| `GILTS` `CETES` `KTB` `TESOURO` `USTRY` `SPXU` | `rwa:<id>` | USD |
| `SolvBTC` | `crypto:SolvBTC` | USD |
| `SolvBTC_FUNDAMENTAL` | `crypto:SolvBTC_FUNDAMENTAL` | USD |
| `SolvBTC.BBN_FUNDAMENTAL` | `crypto:SolvBTC.BBN_FUNDAMENTAL` | USD |

### 3. SolvBTC family stays `crypto`

SolvBTC is a BTC-backed crypto token — a cryptocurrency, not a
real-world asset — so the three SolvBTC feeds are `crypto`, added to
the ADR-0014 allow-list (a one-line amendment there, which 0014's
Amendments section explicitly permits).

The `_FUNDAMENTAL` feeds publish NAV rather than market price.
**Proposed:** model each feed_id as its own distinct crypto code
(`SolvBTC`, `SolvBTC_FUNDAMENTAL`, `SolvBTC.BBN_FUNDAMENTAL`) — no
information loss, no collision, the granular-coverage default.
Collapsing market+NAV into one asset would need a price-basis
discriminator the canonical model does not have. **Open for review:**
if the operator prefers a basis dimension on `OracleUpdate` instead,
that is a larger change and a separate ADR.

## Consequences

- **Positive:** all 19 RedStone feeds decode. RedStone becomes the
  engine's only on-chain RWA price source — Reflector covers none of
  these.
- **Positive:** fixes the latent EUROC bug (feed_id `EUROC/EUR`
  silently dropped since the feed launched).
- **Positive:** the per-feed quote rule lets EUROC land as a genuine
  EUR-denominated observation instead of being mislabelled USD —
  the pre-#53 decoder hardcoded USD for every feed.
- **Negative:** a sixth variant for every `switch asset.Type` ladder.
  Mitigated — the allow-list keeps the set closed and small.
- **Negative:** RWA "prices" are NAV-quoted references updated daily
  at best (24h heartbeat). They must not be mixed into market-VWAP.
  Already handled: RedStone is `ClassOracle`, `IncludeInVWAP=false`.
- **Downstream:** the verified-currency catalogue, explorer asset
  views and `/v1/assets` `asset_class` tagging gain an `rwa` value.
  Catalogue entries stay hand-curated (R-018) — this ADR does not
  auto-populate them.

## Alternatives considered

1. **Reuse `crypto` for RWA** — rejected. A tokenized T-bill sharing
   a type with BTC mis-feeds every crypto-scoped surface. The whole
   point of a typed `AssetType` is to keep these distinct.
2. **Amend ADR-0014 to cover RWA** — rejected. ADR-0014's scope is
   crypto tickers; the repo models each bare-ticker category as its
   own sibling ADR (0010 fiat → 0014 crypto → 0028 rwa). A new
   variant is a new decision, and ADRs are immutable.
3. **One generic `external_ref` variant for fiat+crypto+rwa** —
   rejected for the same reason ADR-0014 §Alternatives-1 rejected
   merging fiat and crypto: type-level clarity is the feature.

## Amendments

_Append new RWA codes here as a one-liner. Never supersede this ADR
for an addition._

- 2026-05-22 — initial allow-list of 8 codes (the RWA feeds in
  RedStone's Stellar mainnet deployment). See `canonical.IsKnownRWA`
  for the live list.

## References

- Related ADRs: ADR-0010 (fiat), ADR-0014 (crypto) — sibling
  bare-ticker variants; ADR-0003 (i128 no-truncation) — RWA prices
  still flow the U256/i128 path unchanged.
- Implementation: `internal/canonical/asset_rwa.go` (allow-list +
  constructor), `internal/canonical/asset.go` (type + String +
  ParseAsset + Validate), `internal/sources/redstone/decode.go`
  (the feed registry).
