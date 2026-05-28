---
title: TWAP / OHLC methodology + freshness semantics
last_verified: 2026-05-28
status: ratified
---

# TWAP / OHLC methodology

This doc explains how `/v1/twap` and `/v1/ohlc` compute their
output and — importantly — how they behave when the requested
window has no trade data. Customers comparing our wire shape to
`/v1/price` may notice a deliberate asymmetry: `/v1/price` will
serve a stale-marked LKG (last known good) value during cache-
cold windows while `/v1/twap` and `/v1/ohlc` return HTTP 404
`errors/no-trades`. That asymmetry is intentional; this page
documents why.

## Wire shape

| Endpoint | Returns | Window-shape | LKG fallback |
| --- | --- | --- | --- |
| `/v1/price` | one current-price scalar | tip-anchored ("price as of N seconds ago") | yes — flags.stale + LKG age |
| `/v1/twap` | one TWAP scalar over `[from, to)` | client-specified | **no** |
| `/v1/ohlc` (single-bar) | one OHLCV row over `[from, to)` | client-specified | **no** |
| `/v1/ohlc?interval=…` (series) | N OHLCV rows | client-specified | **no** |

## Why `/v1/price` has LKG but `/v1/twap`/`/v1/ohlc` don't

A TWAP or OHLC over a window with zero trades is not "the TWAP
with old data" — it's **undefined**. Specifically:

- **TWAP** weights each trade's price by the duration until the
  next trade. A window with no trades has no weighting to compute
  against. The only sane TWAP value over `[t, t+w)` when zero
  trades exist is "no value."
- **OHLC** is open / high / low / close of trades in `[t, t+w)`.
  Zero trades means no open, no high, no low, no close. There is
  no "stale OHLC" — only the OHLC of a *different* window, which
  answers a different question.

In contrast, `/v1/price` is a **tip** endpoint: "what is the
price now?" Answering "the most recent price we have, even if
it's 30 minutes old, with `flags.stale: true`" is honest about
the staleness while still being useful. The customer asked
"what's the price?", not "what's the price of [t, t+30s)?"

If a TWAP/OHLC handler invented an answer by stretching the
window backwards to find data, it would silently change the
semantics of the response. A customer requesting a 24h TWAP
ending now would unknowingly receive a TWAP of yesterday's
24h ending yesterday — a different number representing a
different time window. Returning 404 is the correct contract:
"there are no trades in the window you asked about; if you
want a longer lookback, ask for one."

## What customers should do under "no trades in window"

1. **Widen the window.** The closed-bucket clamp (ADR-0015)
   means `to` defaults to the previous 30-s boundary; explicit
   `to` is respected verbatim. Widening `from` backwards by an
   hour, a day, or a week is a fast retry pattern.
2. **Fall back to `/v1/price`.** For a "give me the latest price
   even if it's old" semantic, the tip endpoint is the right
   surface — it carries `flags.stale` to indicate staleness and
   the LKG age in the envelope's `as_of`.
3. **Use `/v1/history`.** For "show me every trade in this
   window" semantics, the history surface returns raw trades
   without aggregation, which is what TWAP/OHLC compute against.

## Stablecoin-proxy fallback IS present

The TWAP/OHLC endpoints DO carry the X/USD → X/USDC|EURC|… proxy
fallback that `/v1/price` has. If you request `?base=native&quote=fiat:USD`
and no native/USD trades exist in the window, the handler retries
against `[trades].usd_pegged_classic_assets` operator-declared
pegs (Circle USDC, EURC, etc.) in priority order; the first
non-empty result wins and the response carries
`flags.triangulated: true`. This is in
`internal/api/v1/ohlc.go::ohlcTradesWithStablecoinFallback` and
the equivalent in `vwap.go`/`twap.go` paths.

This proxy fallback is *not* an LKG fallback — it answers the
same question ("what was the price over this window?") against
a near-equivalent pair, not against a different time window.

## Cascade-window behaviour

Under the F-0039-style Redis MISCONF cascade (audit-2026-05-26),
the `/v1/price` LKG path remained available because LKG values
live in Postgres (the cache layer being down doesn't lose them).
`/v1/twap` and `/v1/ohlc` correctly returned 404 for windows
where the cascade had blocked the underlying trade ingest — that
is the right answer for "what's the TWAP of a window where we
didn't observe any trades."

Operators investigating the asymmetry should:

1. Confirm the cascade-affected window is a real ingest gap, not
   a cache-fronted view of stale data.
2. Use `/v1/price` for "is anything live for this pair right now"
   diagnostics.
3. Use `/v1/diagnostics/cursors` for the authoritative per-source
   ingest state.

The audit's framing — "SSE+streaming clients on /twap or /ohlc
see hard 404s during cache-cold storms while /price stays
nominally up" — was true but the right user-facing behaviour:
streaming TWAP clients SHOULD see 404 when no trades land in
the streaming window, because the next valid TWAP doesn't exist
yet. Quietly emitting yesterday's TWAP into a today-anchored
stream would be a correctness regression, not a robustness win.

## Cross-reference

- `internal/api/v1/twap.go::handleTWAP` — TWAP path.
- `internal/api/v1/ohlc.go::handleOHLC` — OHLC path.
- `internal/api/v1/ohlc.go::ohlcTradesWithStablecoinFallback` — proxy fallback.
- `internal/api/v1/price.go::handlePrice` — LKG-bearing tip path.
- ADR-0015 — last-closed-bucket contract.
- F-0074 (audit-2026-05-26) — original audit finding that closed
  against this doc.

## Changelog

- 2026-05-28 — initial draft (F-0074 closure).
