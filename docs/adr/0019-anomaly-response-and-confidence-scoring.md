---
adr: 0019
title: Anomaly response policy and confidence scoring — per-asset statistical baselines
status: Accepted
date: 2026-04-28
supersedes: []
superseded_by: null
---

# ADR-0019: Anomaly response policy and confidence scoring

## Context

[ADR-0017](0017-archive-completeness-invariants.md) protects us
against missing data. [ADR-0018](0018-api-consistency-surfaces.md)
protects us against confused consumption (three URLs, three
explicit consistency contracts). Neither addresses what happens
when the data we publish is **observably wrong** — a manipulated
or otherwise anomalous price feed, where the data is "fresh" and
"complete" but does not reflect fair market value.

The October 2024 / 2026 series of oracle-manipulation incidents
in the broader ecosystem (Polter Finance, the USTRY/Reflector
attack on Stellar, Mango / Cream / Inverse / Harvest before them)
all share one structural feature: a thin-liquidity asset price is
manipulated on a single venue, an oracle reports the manipulated
value, a downstream protocol consumes it for collateral pricing or
liquidations, and the attacker walks away with the spread. See
[`docs/architecture/oracle-manipulation-defense.md`](../architecture/oracle-manipulation-defense.md)
for the full case catalogue.

Our pre-existing defenses (multi-source consensus, source-class
exclusion, closed-bucket policy, TWAP availability) protect well
when an asset has multiple liquid sources. They **fail** for
assets with a single venue and no multi-source agreement to
average against. USTRY is the canonical example.

The naïve response is "set a threshold — refuse prices that move
more than X% in Y minutes." This is **wrong**:

- Different asset classes have wildly different normal volatility
  (stablecoins 0.05%, memecoins 50%+ per bucket are both routine).
  A single threshold can't cover both.
- Real market events DO produce 20%+ moves in minutes (asset
  delistings, exchange-hack news, flash crashes). A fixed-
  threshold freeze would lock up rates during legitimate moves.
- An attacker can keep manipulation just under threshold, slowly
  drifting the baseline upward. Over a week, the apparent "normal"
  volatility creeps up, and the attacker eventually moves freely.

The correct abstraction is **per-asset statistical baselines** —
score each asset's typical volatility from its own history, and
flag deviations *from that asset's own normal*, not from an
operator-picked global percentage.

## Decision

**Anomaly response is a continuous confidence score plus a freeze
policy on the closed-bucket surface, not a binary published/not-
published decision based on a fixed threshold.** Three pieces:

1. **Per-asset rolling statistical baseline** of volatility,
   updated continuously as new buckets close.
2. **Multi-factor `confidence` score** on every published price,
   combining baseline-deviation, source coverage, liquidity, and
   cross-oracle agreement (when available).
3. **Freeze policy** that fires only at the extreme corner — low
   confidence AND high statistical anomaly AND single-source —
   and applies asymmetrically per consistency surface (ADR-0018).

### Per-asset statistical baseline

For each `(base, quote)` pair, compute robust statistics over a
rolling 30-day window:

- **`return_median`** — median of bucket-to-bucket % change in VWAP
- **`return_mad`** — median absolute deviation of those returns,
  scaled by 1.4826 to be σ-equivalent for normally-distributed
  data
- **`source_count_p50`** — typical number of contributing sources
  per bucket
- **`liquidity_p50_usd`** — typical bucket liquidity in USD

**Why MAD, not σ.** σ is itself sensitive to outliers; if we
trained on a window containing the previous USTRY attack, σ would
inflate and hide the next attack. MAD is computed from medians and
is robust against outliers in the training window. Mature oracles
(Pyth, MakerDAO OSM) use this exact substitution.

A new bucket's z-score against the baseline:

```
z_score = abs(return_pct - return_median) / return_mad
```

A z-score of 5+ is "anomalous" by the asset's own standards,
regardless of absolute percentage. This automatically scales:

| Asset class | typical return_mad | 5σ trigger |
|---|---|---|
| Stablecoin (USDC, USDT, PYUSD) | ~0.05% | ~0.25% |
| Treasury token (USTRY) | ~0.05% | ~0.25% |
| Major crypto (XLM, BTC, ETH) | ~2% | ~10% |
| Governance token (AQUA, ULTRA) | ~10% | ~50% |
| Memecoin / new listing | ~50% | ~250% |

### Multi-factor confidence score

Combine into a single `confidence ∈ [0, 1]` value on every published
price. Each factor returns a value in [0, 1]; combine via weighted
geometric mean (so any one factor near zero pulls the whole score
down — the dominating-factor behaviour we want):

```
confidence = (
  z_score_factor(z_score)            ^ w_z       *
  source_count_factor(n_sources)     ^ w_src     *
  diversity_factor(class_count)      ^ w_div     *
  liquidity_factor(bucket_volume)    ^ w_liq     *
  cross_oracle_factor(divergence_pct) ^ w_xoracle *
  baseline_quality_factor(days_history) ^ w_qual
)
```

Factor shapes:

- `z_score_factor`: 1.0 at z=0, decays smoothly to ~0 at z=10. Sigmoid.
- `source_count_factor`: 1/(1+exp(-(n-3))) — caps confidence at ~0.3
  for single-source assets; reaches near-1.0 at n≥6.
- `diversity_factor`: 0.5 for one class, 1.0 for ≥2 classes (CEX + DEX,
  for example).
- `liquidity_factor`: log-saturating, near-0 below $1K bucket
  volume, near-1.0 above $100K.
- `cross_oracle_factor`: 1.0 when within 1% of cross-oracle median;
  decays with divergence. Returns 0.7 (neutral) when no cross-oracle
  data is available.
- `baseline_quality_factor`: 0.5 with no baseline data, ramps to 1.0
  over the first 30 days of an asset's history.

Weights `w_*` are operator-tunable in `[anomaly.weights]` config but
default to all 1.0 (equal influence, geometric mean).

The wire response carries the score plus its decomposition (so
customers and on-call operators can see WHY confidence dropped):

```json
{
  "data": {
    "price": "1.00",
    "confidence": 0.92,
    "confidence_factors": {
      "z_score": 0.3,
      "source_count": 6,
      "source_diversity": 2,
      "liquidity_usd": 250000,
      "cross_oracle_divergence_pct": 0.4,
      "baseline_age_days": 187
    }
  }
}
```

### Freeze policy

Freeze fires only when **all three** of the following hold:

```
freeze_condition = (
  confidence < 0.10
  AND z_score > 5.0
  AND source_count <= 1
)
```

Three signals must agree. Catches USTRY-shape attacks; does NOT
fire on legitimate market events (those have multi-source
coverage, so `source_count > 1`).

When freeze fires:
- The closed-bucket surface (`/v1/price`) returns the
  last-known-good price with `flags.frozen: true`,
  `flags.divergence_warning: true`, and the original
  `observed_at` from when the LKG bucket was fresh.
- The tip surface (`/v1/price/tip`) ignores freeze — returns the
  observed value with `confidence: <low>`. Tip's contract is
  "what's happening right now" and a manipulation IS happening.
- The observations surface (`/v1/observations`) ignores freeze —
  returns raw per-source data unchanged.

**Freeze duration:**
- Initial: 30 minutes
- Re-evaluation at expiry: if freeze condition still holds, extend
  by 30 min, up to 4 extensions (2 hours total)
- After 4 extensions: escalate to operator review (P1 alert);
  freeze stays active until manual unfreeze
- Operator override always available: force unfreeze, force
  extend, manually set price

**Auto-unfreeze trigger:** confidence rises above 0.30 AND z_score
falls below 3.0 for two consecutive buckets.

### Per-surface policy summary

| Surface | Confidence in response | Freeze honoured | Anomaly visibility |
|---|---|---|---|
| `/v1/price` (closed-bucket) | ✅ in `data.confidence` | ✅ | LKG with flags |
| `/v1/price/tip` (live) | ✅ in `data.confidence` | ❌ (live data is the contract) | Low confidence + flags |
| `/v1/observations` (raw) | Per-source ages instead | ❌ (raw data is the contract) | Raw values |

### Phased rollout

The full statistical machinery is meaningful work. We ship in
three phases:

**Phase 1 — operator-set per-asset-class thresholds (transitional).**
While the baseline machinery is built, use a small TOML config:

```toml
[anomaly_detection.thresholds]
stablecoin = { warn_pct = 1.0,  freeze_pct = 3.0 }
treasury   = { warn_pct = 1.0,  freeze_pct = 3.0 }
crypto     = { warn_pct = 20.0, freeze_pct = 50.0 }
governance = { warn_pct = 50.0, freeze_pct = 100.0 }
default    = { warn_pct = 30.0, freeze_pct = 75.0 }
```

Operator classifies each asset; thresholds apply per class.
Confidence in this phase is binary (warn/freeze/clear) rather than
continuous. Crude but ships fast and protects against extreme
single-source attacks. **MUST ship before the API is used for
oracle anchoring at scale.**

**Phase 2 — statistical baselines.** The full per-asset MAD-based
baseline. `volatility_baseline_1m` CAGG. Continuous confidence
score. Replaces Phase 1's per-class thresholds with per-asset
learned thresholds. Removes operator burden of classification (the
asset's own data classifies it).

**Phase 3 — cross-oracle integration.** When `internal/divergence/`
ships, `cross_oracle_factor` becomes a real input rather than the
0.7 default. Confidence score fully reflects external consensus.

Each phase is incrementally better. ADR-0019 commits to all
three; the work-list pins the sequencing.

### Multi-window safeguard against frog-boiling

A single 30-day rolling baseline can be slowly drifted by a
sustained low-grade manipulation. To prevent that:

- Compute `return_mad` at three time scales (1d, 7d, 30d)
- Anomaly fires on the **smallest** z-score across the three
  (i.e. if any window flags the bucket as anomalous, it's anomalous)
- A slow drift may pass the 1d and 7d windows but eventually trips
  the 30d window once the drift is large enough vs the original
  baseline

This stays robust to legitimate regime changes (asset matures,
gains liquidity) — those happen across all three windows
proportionally, so no single window reports them as anomalous.

### Bootstrap (warmup) policy for new assets

Newly-listed assets have no baseline. Three options were
considered (Option A: peer-class default; Option B: lock low
confidence; Option C: hybrid). **Decision: Option C (hybrid).**

For an asset with < 30 days of history:
- Use the average baseline of similar-class assets for the math
  (so z-scores are computable)
- Cap `confidence` at 0.5 regardless of other factors
- Set `confidence_factors.baseline_age_days` to actual days of
  history; customers can gate on this directly
- Auto-classify based on issuer-domain metadata when available;
  fallback to operator-set classification

After 30 days, transition to learned per-asset baseline
automatically.

## Consequences

- **Positive — no operator picks "the right percentage."** The
  baseline is what the asset's own history says is normal. The
  threshold is what 5σ from MAD computes to. Self-tuning.

- **Positive — single algorithm covers all asset classes.**
  Stablecoins to memecoins, all use the same formulas with
  different baselines. No per-class table to maintain (after
  Phase 1).

- **Positive — confidence is graded, not binary.** Sophisticated
  consumers (lending protocols) gate on confidence; UI consumers
  display anyway. Both get appropriate behaviour from the same
  wire.

- **Positive — the per-surface policy from ADR-0018 generalises.**
  Strict surface (closed-bucket) honours freeze; lax surfaces
  (tip, observations) don't. No new policy axis introduced.

- **Negative — meaningful engineering investment.** Phase 2
  alone is ~1.5 weeks of work; Phase 3 depends on
  `internal/divergence/` which is its own multi-day project.
  Phase 1 stop-gap ships in <1 day but is admittedly crude.

- **Negative — confidence requires customer education.** A
  customer that ignores `data.confidence` could still consume a
  bad value and lose money. We document loudly, but ultimately
  the customer's contract is responsible. The freeze policy on
  `/v1/price` is the safety net for customers who don't read the
  wire.

- **Negative — no defense against well-resourced multi-source
  manipulation.** If an attacker can manipulate prices across N
  CEXes simultaneously, multi-source consensus fails too. This is
  outside the threat model — defending against it requires
  cross-oracle reference, which is Phase 3.

- **Operational impact — new alert + runbook.** Freeze events
  fire a P2 (or P1 after escalation) alert; an operator must
  review and confirm or override. A new runbook
  `anomaly-freeze-engaged.md` walks through the review process.

- **Downstream design impact — `internal/divergence/` becomes
  load-bearing.** Phase 3 of this ADR depends on it. Cross-oracle
  agreement is the strongest single defense; without it, our
  confidence score is intrinsic-only (we can disagree with the
  world and not know it).

- **Downstream design impact — chained-asset confidence is a
  product.** When pricing AQUA/COP via `AQUA → USDC → USD → COP`,
  the chained confidence is the geometric mean of each leg's
  confidence. A high-confidence DEX leg × low-confidence forex
  leg = appropriately-modest chained confidence. Algorithm
  identical, recursively applied.

## Alternatives considered

1. **Fixed-percentage threshold per asset (operator-set
   permanently).** Rejected: doesn't adapt to regime changes;
   different operators pick different numbers; doesn't handle
   warm-up gracefully.

2. **Hard reject (return 503) on anomaly, no last-known-good
   served.** Rejected: forces every downstream consumer to handle
   the no-price case explicitly. UI consumers see "no data"
   instead of a sensible value. Customers who don't handle it
   silently break. Freezing-with-LKG is a softer failure mode.

3. **Confidence interval (Pyth-style range pricing).** Considered:
   instead of a single price + confidence, return a range like
   `[$0.998, $1.002]` widening with uncertainty. Rejected as the
   default wire shape because most consumers can't handle range
   pricing — would need a separate compatibility surface. The
   `confidence` scalar gives 80% of the value with no wire-shape
   change. **MAY revisit** in a future ADR if customers request it.

4. **Always publish, never freeze; let the customer decide via
   confidence.** Rejected: the freeze policy on `/v1/price` is the
   safety net for customers who don't read confidence. Without it
   we'd silently feed manipulated data to lending protocols whose
   integration was written before our confidence field existed.

5. **z-score using σ instead of MAD.** Rejected: σ inflates after
   the FIRST manipulation, hiding subsequent ones. MAD is robust
   by construction. Same reason mature oracles (Pyth, MakerDAO
   OSM) use MAD.

6. **Single rolling-window baseline only (no multi-window).**
   Rejected: vulnerable to frog-boiling — sustained low-grade
   manipulation slowly drifts the baseline. Multi-window catches
   slow drifts at the longer scale.

7. **Skip Phase 1, ship Phase 2 directly.** Rejected: Phase 2 is
   ~1.5 weeks of work; we want the freeze safety net before the
   API enters production oracle-anchoring use. Phase 1 ships in
   <1 day; the cost of two-step rollout is small vs the protection
   it provides during the gap.

## References

- [ADR-0018](0018-api-consistency-surfaces.md) — the three-surface
  model. This ADR specifies the per-surface freeze application.
- [ADR-0017](0017-archive-completeness-invariants.md) — archive
  completeness; underlies the data integrity this ADR builds on.
- [ADR-0010](0010-off-chain-fiat-representation.md) — source
  classification (`exchange` / `aggregator` / `oracle` /
  `authority_sanity`); the foundation of class-based exclusion
  inputs to confidence scoring.
- [`docs/architecture/oracle-manipulation-defense.md`](../architecture/oracle-manipulation-defense.md) —
  attack catalogue and defensive layers; this ADR specifies the
  policy for layers 4 (outlier detection) and 9 (anomaly response).
- [`docs/operations/runbooks/anomaly-freeze-engaged.md`](../operations/runbooks/anomaly-freeze-engaged.md)
  (TBD) — operator runbook for freeze-engaged events.
- [`migrations/0002_create_price_aggregates.up.sql`](../../migrations/0002_create_price_aggregates.up.sql) —
  the existing CAGG infrastructure that
  `volatility_baseline_1m` parallels.
- Pyth Network confidence interval methodology
  (https://docs.pyth.network/price-feeds/best-practices) — design
  inspiration for confidence scoring.
- MakerDAO Oracle Security Module documentation — design
  inspiration for the OSM-style 1-hour delay (we don't use OSM
  delay but the failure-mode reasoning is similar).
