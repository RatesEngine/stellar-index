---
title: Oracle manipulation — attack catalogue and defensive layers
last_verified: 2026-05-03
status: living document
---

# Oracle manipulation — attack catalogue and defensive layers

A risk-and-defense reference for the engineering team. Documents
known oracle-manipulation incidents in the broader DeFi space, the
attack patterns common to them, and which layers of the Stellar
Index architecture defend against each.

This doc is a **living** piece, not a frozen reference. It lives
in `docs/architecture/` specifically because the threat model
evolves — new incidents inform new defenses, and this doc is the
place to record both.

## Attack pattern (canonical shape)

Every oracle-manipulation exploit observed in DeFi follows the
same five-step shape:

1. **Identify a low-liquidity asset** whose price feeds a
   downstream protocol's collateral / liquidation / borrowing
   logic.
2. **Take a position** in the downstream protocol — usually a
   borrow against the to-be-manipulated asset as collateral, or a
   short-side bet that benefits from a price dislocation.
3. **Manipulate the asset's price** on a thin venue (small DEX
   pool, single-source CEX, sandwich on an AMM). Often via a flash
   loan to inflate purchasing power without permanent capital.
4. **Trigger the oracle to read the manipulated price.** The
   oracle pushes the bad value to the downstream protocol, which
   uses it for collateral valuation or liquidation pricing.
5. **Withdraw value** from the downstream protocol against the
   inflated valuation, before fair-market price re-asserts via
   arbitrage. Walk away with the spread; the protocol absorbs the
   loss.

The exploitable surface is the gap between **the oracle's price**
and **the asset's fair market price**. Every defensive layer in
this doc closes some part of that gap.

## Known incidents

A non-exhaustive catalogue of historically-significant oracle
manipulations. The "what should have stopped it" column is what
maps to our defensive layers below.

### Reflector / USTRY (Stellar, 2026)

- **Attack:** Manipulation of eTherFuse USTRY (tokenised US
  Treasury) price on a thin venue. Reflector reported the
  manipulated value; a downstream lending protocol used it as
  collateral pricing. Attacker borrowed against inflated
  collateral, withdrew, left bad debt.
- **What the oracle did wrong:** Reflector v3's TWAP and cross-pair
  computation are local — Reflector v3 has no on-chain `twap` or
  `x_*` methods, so we compute them ourselves. For a thin asset like USTRY, the
  cross-source consensus that protects liquid assets degrades to
  near-single-source reads. A manipulation on the only venue
  Reflector observed was sufficient.
- **What should have stopped it:** Multi-venue consensus. Liquidity
  floor per source per bucket. Divergence cross-check against
  alternative reference oracles. Per-asset risk tier with stricter
  thresholds for thin-liquidity assets.
- **Status in this codebase:** Reflector is `ClassOracle` in our
  `external.Registry` — its outputs are reported alongside our
  computed VWAP but **excluded from VWAP weight**. So a similar
  USTRY-shaped manipulation would not propagate into our prices.
  We'd report Reflector's diverging value as a separate source and
  trip a divergence warning.

### Mango Markets (Solana, October 2022)

- **Attack:** Avraham Eisenberg manipulated MNGO perpetual price
  on Mango Markets itself by aggressively buying MNGO-PERP, then
  borrowed against the inflated collateral on the same protocol.
  ~$117M in losses across the protocol. Eisenberg later argued in
  court the manipulation was a "highly profitable trading strategy."
- **What the oracle did wrong:** Mango's collateral pricing read
  from Mango's own market data — a circular dependency. The oracle
  was reading the price of an asset on the venue that was being
  manipulated.
- **What should have stopped it:** External oracle reference.
  Liquidity-weighted contribution that downweights low-volume
  trades.
- **Status in this codebase:** Our VWAP is computed across multiple
  external venues (CEX + DEX), not from any single protocol's
  internal data. The class-exclusion rule means our pricing for an
  asset cannot be manipulated by trading on the asset's own
  derivatives market.

### Cream Finance (Ethereum, October 2021)

- **Attack:** Multi-step flash-loan attack manipulating yUSD price
  oracle by depositing yUSD into a Curve pool, then triggering
  oracle re-read. ~$130M lost.
- **What the oracle did wrong:** Single-pool price read. The
  oracle queried one Curve pool's spot price; the attacker only
  needed to manipulate that one pool.
- **What should have stopped it:** TWAP instead of spot.
  Multi-pool aggregation. Closed-bucket evaluation rather than
  block-level.
- **Status in this codebase:** Our default 1m VWAP requires
  sustained manipulation across the full bucket window — single-
  block flash-loan attacks dilute across all OTHER trades in the
  bucket. The TWAP option (also computed) makes multi-block
  manipulation similarly expensive.

### Inverse Finance (Ethereum, April 2022)

- **Attack:** Manipulated INV price on a thin SushiSwap pool, then
  borrowed against it on Inverse's lending market. ~$15M lost.
- **What the oracle did wrong:** Read SushiSwap's spot price for
  INV without considering pool depth or alternative sources.
- **What should have stopped it:** Liquidity floor per pool.
  Multi-source consensus.
- **Status in this codebase:** Multi-source aggregation is default;
  the gap is the absence of an explicit liquidity floor per source
  per bucket (a planned hardening — see "Gap analysis" below).

### Polter Finance (Fantom, November 2024)

- **Attack:** Manipulation of BOO token price oracle, used to
  borrow against inflated collateral on Polter's lending market.
  ~$12M lost.
- **What the oracle did wrong:** Single-DEX-pool read for a
  thin-liquidity asset.
- **What should have stopped it:** Same as Inverse — liquidity
  floor + multi-source consensus.

### Harvest Finance (Ethereum, October 2020)

- **Attack:** Flash-loan manipulation of stablecoin prices on
  Curve, used to drain Harvest's vaults. ~$24M lost.
- **What the oracle did wrong:** Read price from a single Curve
  pool that was the manipulation target.
- **What should have stopped it:** Multi-pool aggregation; TWAP;
  cross-reference against external stablecoin price feeds.

### bZx (Ethereum, February 2020 — multiple incidents)

- **Attack:** Flash-loan manipulation of Uniswap pair prices, then
  borrow against inflated collateral on bZx. Multiple incidents
  totalling ~$1M.
- **What the oracle did wrong:** Single-source spot price.
- **What should have stopped it:** TWAP, multi-source aggregation.

## USTRY scenario walkthrough — concrete demonstration

To make the defense layers concrete, this section walks through
exactly how the system reacts to a USTRY-shaped attack at each
phase of our rollout. ADR-0019 specifies the policy below; this
section shows what it looks like in practice.

### Pre-attack state

USTRY trading at ~$1.00 on a single venue (Aquarius) with low
volume (~$50K daily). System state:

- VWAP from `prices_1m` CAGG: $1.0023 ± 0.0008 over recent buckets
- Sources contributing: `["aquarius"]` — single source
- Liquidity per bucket: ~$2K
- Per-asset baseline (Phase 2+): `return_mad ≈ 0.05%`, established
  over 30+ days
- Current confidence (Phase 2+): ~0.20 (single-source caps it,
  even though the baseline is well-established and z-score is
  near-zero)

### Attack window (T+0 through T+5min)

| Time | Observed bucket VWAP | z-score vs baseline | Source count | Computed confidence | Freeze? |
|---|---|---|---|---|---|
| T-1m | $1.0023 | 0.3 | 1 | 0.20 | No |
| T+0 | $5.00 | 80σ | 1 | 0.04 | **Yes** |
| T+1m | $20.00 | 380σ | 1 | 0.03 | **Yes** |
| T+3m | $100.00 | 1980σ | 1 | 0.02 | **Yes** |
| T+5m | $50.00 | 980σ | 1 | 0.02 | **Yes** |

Freeze condition `confidence < 0.10 AND z_score > 5σ AND source_count
<= 1` trips at T+0 and stays tripped throughout the attack window.

### Per-surface response during the attack

**`/v1/price?asset=USTRY-G...&quote=fiat:USD`** (closed-bucket):

```json
{
  "data": {
    "asset_id": "USTRY-G...",
    "quote": "fiat:USD",
    "price": "1.0023",
    "price_type": "vwap",
    "confidence": 0.20,
    "observed_at": "2026-04-28T08:30:00.000Z",
    "sources": ["aquarius"]
  },
  "flags": {
    "stale": true,
    "frozen": true,
    "single_source": true,
    "divergence_warning": true
  }
}
```

`observed_at` reflects the LAST GOOD bucket (pre-attack). `price`
is the pre-attack VWAP. Lending protocols consuming `/v1/price`
see no apparent change throughout the attack — exactly the
defense we want. The flags loudly signal that something is wrong;
operators get a P2 alert.

**`/v1/price/tip?asset=USTRY-G...&quote=fiat:USD`** (live):

```json
{
  "data": {
    "asset_id": "USTRY-G...",
    "quote": "fiat:USD",
    "price": "50.0000",
    "price_type": "vwap",
    "window_seconds": 5,
    "confidence": 0.02,
    "confidence_factors": {
      "z_score": 980,
      "source_count": 1,
      "source_diversity": 1,
      "liquidity_usd": 5000,
      "cross_oracle_divergence_pct": 0.0,
      "baseline_age_days": 187
    },
    "observed_at": "2026-04-28T08:35:42.351Z",
    "sources": ["aquarius"]
  },
  "flags": {
    "realtime": true,
    "single_source": true,
    "divergence_warning": true
  }
}
```

Tip surface shows the manipulated value transparently —
"what's happening right now" includes the manipulation. Confidence
is 0.02 (catastrophic), `single_source` flag fires, operators see
the drop in confidence immediately. UI consumers can render
"$50.00 (very low confidence — possibly manipulated)" as a price
+ explicit warning.

**`/v1/observations?asset=USTRY-G...&quote=fiat:USD`** (raw):

```json
{
  "data": [
    {
      "source": "aquarius",
      "price": "50.0000",
      "observed_at": "2026-04-28T08:35:42.351Z"
    }
  ],
  "flags": { "realtime": true, "single_source": true }
}
```

Raw surface shows what we observed, with no aggregation, no
confidence, no freeze. Customer computes their own response.

### Post-attack recovery

After the attacker exits the position and arbitrage corrects the
on-chain price back to ~$1.00:

- Closed-bucket freeze evaluates at expiry (every 30 min during
  freeze): confidence still low (still single-source), but z-score
  drops below 3.0
- Two consecutive buckets at z-score < 3.0 → auto-unfreeze
- `/v1/price` resumes normal serving with `flags.frozen: false`
  and `confidence: 0.20`
- A postmortem is filed; the Reflector / USTRY incident is added
  to the "Known incidents" list above for future reference

### Engineering observability during the event

Operators see (via Prometheus + alertmanager):
- `stellarindex_anomaly_freeze_engaged{asset="USTRY-G..."}` gauge: 1
- `stellarindex_anomaly_z_score{asset="USTRY-G..."}` histogram: spikes
- `stellarindex_anomaly_confidence{asset="USTRY-G..."}` gauge: drops
- P2 alert "anomaly freeze engaged on USTRY-G..." fires within 1
  bucket of trip
- Runbook `anomaly-freeze-engaged.md` walks through:
  - "Is this a real market event or manipulation?"
  - Confirm freeze (do nothing) vs override (manual unfreeze)
  - Cross-reference checks (CoinGecko, CMC, Reflector — all should
    show similar manipulation if it's network-wide; only Aquarius
    here means it's venue-specific)
  - File postmortem + add to incident catalogue

### Phase 1 vs Phase 2 vs Phase 3 — what changes for USTRY

The walkthrough above assumes Phase 2 is shipped (full statistical
baseline). The system would also defend USTRY in Phase 1 (with
slightly cruder mechanics) and provides additional protection in
Phase 3:

| Phase | What detects USTRY attack | What protects |
|---|---|---|
| Phase 1 (per-class thresholds) | USTRY classified as `treasury` (warn 1%, freeze 3%); 100x movement blows past freeze threshold | Same freeze policy; binary trip rather than continuous confidence |
| Phase 2 (statistical baseline) | z-score 1000σ from MAD-derived baseline; `confidence_factors` exposed on wire | Continuous confidence + decomposition factors visible to consumers |
| Phase 3 (cross-oracle integration) | `cross_oracle_factor` brings external oracle disagreement into the confidence | Strongest protection; would catch even a coordinated multi-venue manipulation if peer oracles disagreed |

So even at Phase 1 (the stop-gap), USTRY is protected on the
closed-bucket surface. Phase 2 makes the same protection
self-tuning. Phase 3 hardens against the next class of attacks
(multi-venue coordinated).

## Defensive layers (mapped to attack steps)

How each layer closes part of the gap between manipulated venue
price and fair-market price:

### Layer 1 — Multi-source consensus (default, shipped)

Every asset's VWAP is computed across **all known venues that
contribute trades**. Per `internal/sources/external/registry.go`,
only `ClassExchange` (CEX + DEX with verified trade-level data)
contributes weight. Aggregator outputs (CoinGecko / CMC), oracles
(Reflector / Band / Redstone), and authority-sanity sources (ECB /
Polygon FX) are reported alongside but excluded from VWAP weight.

**Defends against:** Steps 3–4. A single-venue manipulation gets
diluted against all other contributing venues.

**Limitation:** For an asset with only one contributing venue,
this defense degrades to "trust that one venue."

### Layer 2 — Source-class exclusion (default, shipped)

We deliberately do NOT consume Reflector / Band / Redstone /
CoinGecko / CMC outputs as VWAP inputs. Their prices appear in
the response's `sources` array (alongside our computed value) and
in our divergence-monitoring outputs, but they cannot move our
VWAP.

**Defends against:** Reflector-shape attacks specifically. If
Reflector is compromised tomorrow, our VWAP for the affected
asset doesn't change — we compute from raw exchange data and
report the divergence.

**Why this matters:** This is the layer that would have isolated
us from the USTRY / Reflector incident. Our pricing cannot be
manipulated by manipulating Reflector.

### Layer 3 — Liquidity floor per source per bucket (planned, NOT shipped)

What's missing: a per-source per-bucket minimum-USD-volume
threshold. A pool with $500 of TVL contributing one trade
shouldn't be voting on the asset's VWAP. Today our trade-volume
weighting partially addresses this (small-volume trades get small
weight) but doesn't reject thin-pool sources outright.

**Defends against:** Step 3. Thin-pool manipulation can't slip
contributions through if the pool's depth is below the floor.

**Concrete proposal:** Add `aggregate.min_pool_tvl_usd` config
default ~$10K and `aggregate.min_per_bucket_volume_usd` ~$1K.
Sources/pools below the floor are excluded from VWAP for that
bucket but still recorded in raw trades for audit.

### Layer 4 — Outlier detection (alert-only, partial)

The `aggregator-outlier-storm` alert fires when a single source's
contributions diverge from the inter-source median by N sigma.
Today this is monitor-only — operators see the alert but the
filter is calibrated for noise rejection, not adversarial
detection.

**Defends against:** Step 3, after-the-fact. Manipulation is
detected within minutes; doesn't prevent the bad bucket from
landing.

**Hardening:** Make the storm AUTOMATICALLY exclude the offending
source from the bucket when σ-threshold is exceeded for >
threshold count, with manual review afterwards. Today's runbook
suggests this manually; should be automatic for high-confidence
outlier-storm detections.

### Layer 5 — Cross-reference divergence monitoring (planned, NOT shipped)

`internal/divergence/` (planned package per CLAUDE.md) will:

- Cross-check our computed VWAP against CoinGecko / CMC /
  Chainlink-HTTP / Reflector / Band / Redstone outputs
- Set `flags.divergence_warning: true` on every response when
  divergence > threshold
- Trip an alert; runbook walks operators through "is this a real
  market event or a manipulation in progress?"

**Defends against:** Steps 4–5, by giving downstream consumers a
wire-level signal that we disagree with the broader oracle
consensus.

**Why this matters:** Even if our internal layers all somehow
fail to detect a manipulation (e.g. coordinated multi-venue
manipulation across our entire source set), the divergence layer
catches the case where our computation diverges from everyone
else's. That's the last line of defense.

### Layer 6 — Closed-bucket policy (default, shipped)

Per ADR-0015, the API only serves closed buckets. A bucket is
"closed" when its window-end timestamp has passed plus the CAGG
refresh delay (~30s for the 1m bucket).

**Defends against:** Step 3. Single-block manipulation is averaged
across all OTHER trades in the bucket, dramatically diluting its
effect. To move the bucket's VWAP meaningfully, an attacker must
sustain manipulation across the entire window — which is far more
expensive than a single-block flash-loan attack.

### Layer 7 — TWAP availability (default, shipped — alongside VWAP)

The CAGG schema (`migrations/0002_create_price_aggregates.up.sql`)
computes both VWAP and TWAP at every granularity. Customers who
need additional time-resilience (e.g. for liquidation pricing)
can request TWAP at a longer window.

**Defends against:** Step 3 with even greater dilution. A 1-minute
manipulation barely affects a 1h TWAP.

### Layer 9 — Per-asset confidence + freeze policy (per ADR-0019)

The defenses above protect well when an asset has multiple liquid
sources. They fail for thin, single-source assets like USTRY where
multi-source consensus simply doesn't exist. ADR-0019 specifies an
additional layer that protects single-source assets:

- **Per-asset rolling statistical baseline** — for each
  `(base, quote)` pair, compute `return_mad` (median absolute
  deviation, robust σ-equivalent) over a rolling 30-day window.
  z-scores against this baseline detect anomalies relative to the
  asset's *own normal volatility*, regardless of absolute
  percentage.
- **Multi-factor confidence score** — combine
  z-score, source count, source diversity, liquidity, cross-oracle
  agreement, and baseline data quality into a single
  `data.confidence ∈ [0, 1]` value on every published price.
- **Freeze policy on closed-bucket surface only** — when
  `confidence < 0.10 AND z_score > 5σ AND source_count <= 1`,
  `/v1/price` returns last-known-good with `flags.frozen: true`.
  `/v1/price/tip` and `/v1/observations` ignore freeze
  (their consistency contracts permit anomalous data).

**Defends against:** Steps 3–5 for thin-asset / single-source
attacks where multi-source consensus (Layers 1, 2) provides no
protection. The USTRY scenario (worked example above) is the
canonical case.

**Phased rollout** per ADR-0019:
- Phase 1: per-asset-class default thresholds (operator config), binary warn/freeze.
- Phase 2: full per-asset statistical baseline + continuous confidence.
- Phase 3: cross-oracle factor wired in once `internal/divergence/` ships.

### Layer 8 — Decoder + WASM-version audit gating (default, shipped)

Per `docs/architecture/contract-schema-evolution.md`, the
`BackfillSafe` flag in `internal/sources/external/registry.go`
gates which Soroban contract WASM versions we trust for backfill.
A new WASM upgrade triggers the per-WASM-hash audit procedure
(`docs/operations/wasm-audits/`) before we'll replay against it.

**Defends against:** Step 3 via a different vector — malicious WASM
upgrade. An attacker who deploys a backdoored WASM upgrade for a
known DEX contract gets caught at audit time, not after exploit.

## Gap analysis

Defenses architecturally specified, with current shipped status.
ADR-0019 supersedes the earlier "per-asset risk tier" gap with a
properly-scoped statistical approach (Phases 1 + 2 are live;
Phase 3 cross-oracle factor is the remaining piece).

| Defense | ADR | Phase | Status | Priority |
|---|---|---|---|---|
| **Per-asset confidence + freeze policy (Phase 1)** | ADR-0019 | Phase 1 transitional | **Shipped** — `internal/aggregate/anomaly/` (per-class thresholds + freeze action) | **High** — minimum stop-gap before production oracle anchoring |
| **Per-asset confidence + freeze policy (Phase 2 statistical baselines)** | ADR-0019 | Phase 2 | **Shipped** — `internal/aggregate/baseline/` (Median/MAD/ZScore + multi-window refresh) + `internal/aggregate/confidence/` (six-factor weighted-geomean score) | **High** — replaces operator thresholds with per-asset learned thresholds; the proper protection against USTRY-shape attacks |
| **`internal/divergence/` cross-reference** | ADR-0019 | Phase 3 | **Wired** — divergence worker writes `cachekeys.Divergence(asset)`; orchestrator reads it via `lookupDivergencePct` and feeds `confidence.CrossOracleFactor`. Production-quality operational coverage tracked as L7.3 (post-launch). | **High** — last line of defense; the post-launch L7.3 work tunes divergence-source coverage, not the wiring itself |
| **Liquidity floor per source per bucket** | (planned) | — | Trade-volume weighted, no absolute floor | **Medium** — partially covered by ADR-0019's `liquidity_factor` in confidence; an explicit hard floor is complementary |
| **Auto-exclude in outlier-storm** | (planned) | — | Alert-only | **Medium** — detect-and-react vs detect-and-prevent |
| **Stablecoin depeg auto-gating** | (planned) | — | Manual policy via aggregator class system | **Low** — depeg detection works; auto-gating during severe depegs would prevent stablecoin-as-collateral exploits |

## Adversarial-testing exercises (recommended, not yet scheduled)

Concrete tests that would exercise these defenses against
realistic manipulation attempts:

1. **Thin-pool simulation.** Inject a fabricated trade into a
   small DEX pool (via captive-core replay against synthetic
   ledgers) representing a 50% price spike. Confirm:
   - Outlier-storm alert fires within 1 bucket
   - VWAP barely moves (other sources dominate)
   - `flags.divergence_warning` flips on the affected pair (the
     divergence service writes to `div:<asset>` Redis keys; the
     `/v1/price` handler surfaces the flag)

2. **Single-source compromise.** Configure a "malicious binance"
   stub that returns price ×2 on all trades. Confirm:
   - Outlier-storm fires
   - VWAP weight on the bad source decays as σ-filter excludes it
   - Operator runbook walks through identification + disabling

3. **Multi-source coordinated attack.** Inject divergent prices
   into N sources simultaneously. Confirm:
   - Divergence monitoring fires
   - Operators notice within minutes
   - System fails-safe: clearly-flagged response > silently-wrong response

4. **Reflector / Band / external-oracle compromise.** Point our
   external-oracle source's output at a manipulated value.
   Confirm: our VWAP doesn't change. Our wire response shows the
   divergence as a source-level note.

These exercises would make valuable additions to a chaos-testing
suite once `internal/divergence/` ships (item #24 in the work
list).

## References

- [ADR-0010](../adr/0010-off-chain-fiat-representation.md) — source
  classification (`exchange` / `aggregator` / `oracle` /
  `authority_sanity`); the foundation of class-based exclusion.
- [ADR-0015](../adr/0015-last-closed-bucket-rate-serving.md) —
  closed-bucket policy; raises the cost of single-block manipulation.
- [ADR-0018](../adr/0018-api-consistency-surfaces.md) — three
  consistency surfaces; `flags.divergence_warning` is wired across
  all three.
- [ADR-0019](../adr/0019-anomaly-response-and-confidence-scoring.md) —
  per-asset confidence + freeze policy; protects single-source
  thin assets that multi-source consensus can't.
- [`docs/architecture/aggregation-plan.md`](aggregation-plan.md) —
  the policy chain underlying VWAP computation.
- [`docs/operations/runbooks/aggregator-outlier-storm.md`](../operations/runbooks/aggregator-outlier-storm.md) —
  the runbook that fires on adversarial outlier patterns.
- [`docs/operations/runbooks/price-divergence.md`](../operations/runbooks/price-divergence.md) —
  the runbook that fires on cross-reference divergence.

## Maintenance

When a new oracle-manipulation incident becomes public:

1. Add an entry under "Known incidents" with the same
   attack / oracle-error / what-should-have-stopped-it / status
   columns
2. If the incident reveals a defense gap not in our list,
   add it to "Gap analysis"
3. Update the `last_verified` date in frontmatter
4. Cross-reference from any newly-affected runbook
