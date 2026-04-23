---
title: Runbook — price-divergence
last_verified: 2026-04-23
status: draft
severity: P2
---

# Runbook — `ratesengine_price_divergence_warning` / `_critical`

## At a glance

| Field | Value |
| ----- | ----- |
| Alerts | `_warning` (> 5 % for 2 min, informational), `_critical` (> 10 % for 2 min, ticket) |
| Severity | P2 (ticket at 10 %) / P3 (informational at 5 %) |
| Detected by | `deploy/monitoring/rules/divergence.yml` |
| Typical MTTR | 30 min – hours (depends on cause) |
| Impact | Our aggregated price disagrees with a trusted reference (CoinGecko / CMC / Chainlink-HTTP). If we're wrong, every API consumer gets a wrong price — downstream wallets may display misleading USD values, and Freighter's RFP explicitly calls out cross-reference sanity as a correctness guarantee. |

## Symptoms

- `abs(ratesengine_our_price - ratesengine_reference_price) /
  ratesengine_reference_price > 0.05` (or 0.10 for critical) for
  ≥ 2 min on some asset + reference-source pair.
- Dashboard *Divergence → per-asset* panel shows the spread.
- Often *doesn't* fire alone: bad decimal handling produces 100×
  or 1e6× divergence, not 5–10 %.

## Quick diagnosis (≤ 5 min)

```sh
# Which asset + reference source?
curl -s http://prometheus:9090/api/v1/query --data-urlencode \
  'query=topk(5, abs(ratesengine_our_price - ratesengine_reference_price) / ratesengine_reference_price)'

# Compare the two prices side by side
curl -s 'http://api:8080/v1/price?asset=native:XLM&quote=fiat:USD'
curl -s 'https://api.coingecko.com/api/v3/simple/price?ids=stellar&vs_currencies=usd'

# What sources are contributing to our aggregate for this asset?
psql -c "SELECT source, count(*) AS trades, max(observed_at) AS latest
         FROM trades
         WHERE (base = 'native:XLM' OR quote = 'native:XLM')
           AND observed_at > now() - interval '1 hour'
         GROUP BY source ORDER BY trades DESC;"
```

## Typical root causes (roughly in order of severity)

1. **Stale source contributing to the aggregate.** A source that
   stopped ingesting is still showing up in our VWAP because we
   haven't aged it out of the aggregation window.
   - Signal: one source's `max(observed_at)` is well behind the
     others; that source's price differs from the market by > 5 %.
   - Mitigation: cross-check with `source-stopped.md`; if the
     source is stopped, its historical trades keep contributing
     for the aggregation window — which is correct by design but
     means the divergence will persist until the window rolls
     forward. If it's chronically stopped, disable it in
     `ingestion.enabled_sources`.

2. **Wrong decimals somewhere.** ADR-0003 forbids float and
   requires `NUMERIC`; an accidental `int64(parts.Lo)` truncation
   in a decoder produces a price off by 2^N — catastrophic and
   obvious. This triggers critical divergence and should already
   have been caught by golden-file tests in `internal/sources/*/`.
   - Signal: ratio > 100× or ~0 for the whole asset on one source.
   - Mitigation: revert the decoder PR. This is a data-integrity
     incident; also audit any trades ingested with that version
     and delete them from the trades hypertable before they poison
     aggregates further.

3. **Illiquid market / genuine price discovery.** A thinly-traded
   long-tail token can genuinely move 5–10 % while reference data
   hasn't updated (reference sources don't track every Stellar
   asset).
   - Signal: low trade count in the window; no other sources to
     cross-check against; no other asset from the same issuer
     diverges.
   - Mitigation: nothing — this is a real signal, not an incident.
     Demote to informational via a label-specific silence if it's
     sustained.

4. **Reference source itself is broken.** CoinGecko outages,
   Chainlink-HTTP stale feed, CMC rate-limited us.
   - Signal: *every* asset diverges from that one reference,
     while other references agree with us.
   - Mitigation: pull that reference out of the divergence-feed
     rotation until it recovers.

5. **Arbitrage window.** Stellar DEX briefly trades at a different
   price from CEX due to cross-chain bridging delays or native
   Stellar liquidity being thin. Real divergence, not a bug.

## Mitigation

- [ ] Step 1 — eyeball the order of magnitude. > 100× divergence
      is always a decoder bug, never a market move — skip to step 3.
- [ ] Step 2 — identify the contributing source(s) and whether one
      is stale (diagnosis above).
- [ ] Step 3 — if decoder bug: revert, audit, purge corrupt rows,
      redeploy. This becomes an incident postmortem automatically.
- [ ] Step 4 — if stale-source: confirm via `source-stopped.md`
      diagnosis and mitigate there.
- [ ] Step 5 — if real market move: document in `postmortems/` as
      a known event (so we don't re-investigate the same spike).
- [ ] Verification: divergence drops under 5 % (or under 10 % for
      the critical rule) and the alert clears.

## Root cause analysis

- Which asset + reference pair + time window.
- Contributing sources + their trade counts + their latest
  observations.
- A sample of raw trade rows in the aggregation window (their
  prices — are they reasonable?).
- Cross-check: two other reference sources agree or disagree?

## Known false-positive patterns

- **CEX-halted vs DEX-open markets** (e.g., during CEX maintenance):
  our DEX data keeps moving; CoinGecko's aggregate lags. Real
  divergence that isn't our fault.
- **Freshly-listed tokens** — reference sources don't track them,
  so their "reference price" is the issuer's advertised number or
  empty. Suppress for new listings by excluding them from the
  divergence feed for the first 24 h.

## Related

- `oracle-stale.md` — when the reference *is* an on-chain oracle.
- `source-stopped.md` — a stopped source is the most common
  divergence cause we'd actually do something about.
- ADR-0003 (i128 precision) — decoder discipline that prevents
  decimal-related divergence.
- `divergence.yml` alert rules — if you retune thresholds, update
  this runbook too.

## Changelog

- 2026-04-23 — initial draft. Lays out the "order of magnitude"
  triage trick: > 100× is always a decoder bug.
