---
title: Runbook — anomaly-freeze-engaged
last_verified: 2026-04-28
status: draft
severity: P2
---

# Runbook — `ratesengine_anomaly_freeze_engaged`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_anomaly_freeze_engaged` (P2) / `_max_extensions` (P1) |
| Severity | P2 (initial); P1 after 4 extensions |
| Detected by | Prometheus rule in `deploy/monitoring/rules/anomaly.yml` |
| Typical MTTR | 5–30 min for confirmed manipulation; up to 2 h for ambiguous market events |
| Impact | The asset's `/v1/price` returns last-known-good (LKG); customers consuming it see frozen value with `flags.frozen: true`. `/v1/price/tip` and `/v1/observations` continue serving live data. Lending protocols using `/v1/price` for collateral are protected; UI consumers see the manipulation transparently. |

## What freeze means

A freeze fires when **all three** conditions hold for an asset's
1m bucket per [ADR-0019](../../adr/0019-anomaly-response-and-confidence-scoring.md):

1. `confidence < 0.10` — multi-factor confidence is catastrophic
2. `z_score > 5σ` from the asset's own rolling baseline
3. `source_count <= 1` — no multi-source consensus to cross-check

This is the extreme corner: anomalous price movement on a single
source where we have no peer data to validate against. Catches
USTRY-shape attacks; designed NOT to fire on real market events
(those have multi-source coverage).

## Symptoms

- `ratesengine_anomaly_freeze_engaged{asset="..."}` gauge = 1
- `ratesengine_anomaly_z_score{asset="..."}` histogram shows a
  recent spike (5σ+)
- `ratesengine_anomaly_confidence{asset="..."}` gauge dropped
  sharply below 0.10
- Affected asset's `/v1/price` response carries
  `flags.frozen: true` and `flags.divergence_warning: true`
- The pre-freeze price is unchanged in the response; `observed_at`
  reflects the LAST-KNOWN-GOOD bucket timestamp, not now

## Quick diagnosis (≤ 5 min)

The single most important question: **is this a real market event
or manipulation?** Real market events typically affect multiple
sources simultaneously; manipulation typically hits only one venue.

```sh
# 1) Which asset is frozen and what's its on-the-wire state?
ssh r1 'curl -s "http://localhost:9090/api/v1/query?query=ratesengine_anomaly_freeze_engaged" | jq'
ssh r1 'ratesengine-ops list-cursors -config /etc/ratesengine.toml | grep <asset>'

# 2) What does our raw observation show right now?
curl -s "https://api.ratesengine.net/v1/observations?asset=<asset>&quote=fiat:USD" | jq

# 3) What do EXTERNAL references show? (if multiple agree, it's likely real)
curl -s "https://api.coingecko.com/api/v3/simple/price?ids=<coingecko-id>&vs_currencies=usd" | jq
curl -s "https://pro-api.coinmarketcap.com/v2/cryptocurrency/quotes/latest?symbol=<symbol>" -H "X-CMC_PRO_API_KEY: ..." | jq
# Reflector / Band / Redstone if applicable to the asset

# 4) What does the affected venue's spot order book look like?
# (Specific to the venue — Aquarius, Soroswap, etc.)
ratesengine-ops verify-decoders -config /etc/ratesengine.toml \
  -from <recent-ledger> -to <head-ledger> | grep <asset>
```

Decision tree:

| Multiple external refs agree | Single venue's order book reasonable | Probable cause | Action |
|---|---|---|---|
| Yes (CoinGecko, CMC, Reflector all show similar move) | n/a | **Real market event** (delisting, news, flash crash). Not manipulation. | Override freeze (manual unfreeze). Annotate the incident. |
| No (only our venue shows the move) | Order book paper-thin / wash-traded | **Manipulation** | Confirm freeze (do nothing). Wait for arbitrage to correct or extension limit. |
| No external refs cover the asset (truly thin) | Unclear | **Cannot tell — defer to caution** | Confirm freeze. Escalate to on-call lead for human judgment. |
| Conflicting | n/a | Investigation needed | Extend freeze, dig in. |

## Mitigation (≤ 15 min)

- [ ] **Step 1 — Cross-reference the asset against external sources.** Use the diagnosis commands above. Capture results for the postmortem.

- [ ] **Step 2 — Decide: confirm or override.**

  **If confirming the freeze (manipulation suspected):**
  ```sh
  # No action needed — the freeze auto-extends every 30 min up to
  # 2 hours total. After 4 extensions, alert escalates to P1 and
  # requires manual review.
  # Annotate the incident channel:
  echo "<asset> freeze confirmed at $(date -Iseconds); cross-refs: <observations>"
  ```

  **If overriding the freeze (legitimate market event):**
  ```sh
  # Manual unfreeze. CAREFUL — this immediately publishes the
  # current observed price as authoritative.
  ratesengine-ops anomaly unfreeze -asset <asset-id> \
    -reason "real market event: <details>" \
    -approver <your-handle>
  ```

  Operator override is logged + audited. The override reason is
  stamped onto the next bucket's response as
  `flags.operator_override: true` for visibility.

- [ ] **Step 3 — If confirming, monitor the recovery path.** Auto-unfreeze fires when `confidence > 0.30 AND z_score < 3.0` for two consecutive buckets. This usually happens within 30 min as arbitrage corrects.

- [ ] **Step 4 — If escalated to P1 (4 extensions reached, 2h elapsed):** the freeze stays active until manual unfreeze. Follow the [SEV playbook](../sev-playbook.md) §4 for SEV-2 incident management. Possible outcomes:
  - Manipulation has stopped; manually unfreeze
  - Manipulation persists or asset has structurally changed (e.g. delisted everywhere); manually set price via operator override OR delist asset from API surface

- [ ] **Verification:** `ratesengine_anomaly_freeze_engaged{asset=<x>}` returns to 0 after auto-unfreeze or manual override.

## Root cause analysis

For the postmortem, capture:

- The full bucket-by-bucket sequence of `confidence`, `z_score`,
  `source_count`, `liquidity_usd` from a 2-hour window centered on
  the freeze event
- The raw trades that contributed to the anomalous bucket(s);
  specifically the trade-level price + volume + tx-hash
- External-reference values (CoinGecko / CMC / Reflector / Band)
  at the same time window
- The decision: was this real market event or manipulation? What
  led to that conclusion?
- If manipulation: was the attacker's tx-hash visible? Did funds
  flow to a known address? (For the LE postmortem if relevant.)
- Did the auto-unfreeze fire correctly, or was manual intervention
  needed? Why?
- Did the ADR-0019 thresholds work? Should `confidence_threshold`,
  `z_score_threshold`, or `source_count_threshold` be tuned?
- Update [oracle-manipulation-defense.md](../../architecture/oracle-manipulation-defense.md)
  "Known incidents" section with the new entry.

## Known false-positive patterns

- **Asset just listed (warmup window).** New assets have
  `confidence` capped at 0.5 by ADR-0019's bootstrap policy.
  Combined with low source count, can drop below 0.10 on small
  moves. Suppress freeze alerts during the first 30 days after
  asset addition; mark as "warming up" via
  `confidence_factors.baseline_age_days < 30`.

- **Source feed restarting.** A CEX connector restart can briefly
  drop a multi-source asset to single-source while reconnecting.
  If a normal-sized price move happens during that ~30 s window,
  freeze can theoretically fire. Mitigation: minimum-duration
  guard — require freeze condition to hold for ≥ 2 consecutive
  buckets before engaging. (Not yet implemented; track as a
  follow-up.)

- **Stablecoin briefly off-peg.** USDC / USDT can momentarily
  trade at 0.998–1.002 during high-volume hours. If our depeg
  threshold is too tight, freeze could fire on legitimate par
  variation. Mitigation: per-class default thresholds (Phase 1)
  with stablecoin set explicitly to 1% warn / 3% freeze.

- **Asset class mis-classification.** A stablecoin classified as
  `crypto` by mistake gets a 20% threshold instead of 1% → no
  freeze on small depegs. Operator should periodically audit asset
  classifications.

## Related

- [ADR-0019](../../adr/0019-anomaly-response-and-confidence-scoring.md) —
  the policy this runbook serves.
- [ADR-0018](../../adr/0018-api-consistency-surfaces.md) —
  per-surface application of the freeze policy.
- [oracle-manipulation-defense.md](../../architecture/oracle-manipulation-defense.md) —
  attack catalogue + defensive layers; this runbook is the
  operational arm of Layer 9.
- [aggregator-outlier-storm.md](aggregator-outlier-storm.md) —
  related but distinct: outlier-storm is about a single source
  diverging from peers within a multi-source asset; freeze is
  about a single-source asset moving anomalously by its own
  baseline.
- [price-divergence.md](price-divergence.md) — related: cross-
  reference divergence (when our prices diverge from external
  oracles). Often co-fires with anomaly-freeze.
- Postmortems tagged `anomaly-freeze` — `docs/operations/postmortems/`.

## Changelog

- 2026-04-28 — initial draft alongside ADR-0019.
