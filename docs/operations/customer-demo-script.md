---
title: Customer sign-off demo script (L6.6)
last_verified: 2026-05-03
status: operator runbook
---

# Customer sign-off demo

End-to-end demo walk-through for **L6.6** in the launch-readiness
backlog. Goal: in 30 minutes, walk a customer through every
surface they're likely to use, with concrete `curl` commands they
can re-run later. The customer should leave able to make their
first real request without further intervention.

## Pre-flight (T-30 min)

- [ ] **Demo URL pinned.** `https://api.ratesengine.net/v1`
      (post-cutover) or `https://staging.ratesengine.net/v1`
      (pre-cutover dry-run).
- [ ] **Demo API key minted.** Use a tier-`apikey` key with
      reasonable rpm; record it in the demo notes for cleanup
      after.
- [ ] **Browser tabs open** — API reference, getting-started,
      status page.
- [ ] **Terminal pre-loaded** with `BASE`, `KEY` env vars:
      ```sh
      export BASE='https://api.ratesengine.net/v1'
      export KEY='ak_demo_...'
      ```
- [ ] **Wireshark / network inspector NOT open** — keeps the
      visible noise low.

## The walk-through

Stages are 5 min each unless noted; total ≈ 25-30 min.

### Stage 1 — "Is this thing on?" (2 min)

```sh
curl -s "$BASE/healthz" | jq .
curl -s "$BASE/version" | jq .
```

Talking points:
- Health + version surfaces are cheap, unauthenticated, and
  suitable for monitoring.
- `version` exposes the CalVer tag — operators can tell at a
  glance which build is responding.

### Stage 2 — Closed-bucket pricing (5 min)

```sh
curl -sH "Authorization: Bearer $KEY" \
  "$BASE/price?base=native&quote=fiat:USD" | jq .
```

Talking points:
- **Closed-bucket** semantics: the value you see is
  byte-identical across r1/r2/r3, and it stays the same for
  every request within the same minute. Demo by re-running
  the same request → the response should be identical.
- **`flags`**: `stale`, `divergence_warning`, `frozen`, etc.
  Each has a documented meaning; consumers gating on them get
  meaningful signal.
- **`confidence`** + **`confidence_factors`**: ADR-0019.
  Multi-factor score (0..1) with the per-factor decomposition
  on the wire. Customers can read why confidence dropped.
- **`sources`**: which venues contributed to this VWAP. Click
  through the names to `/v1/sources` for class metadata.

### Stage 3 — Tip pricing + "consistency vs freshness" tradeoff (3 min)

```sh
curl -sH "Authorization: Bearer $KEY" \
  "$BASE/price/tip?base=native&quote=fiat:USD" | jq .
```

Talking points:
- Tip is **rolling-window**, not closed-bucket — different
  surface, different consistency contract (ADR-0018).
- Use tip when freshness > consistency (e.g. UI displays).
  Use `/v1/price` for trade execution + reporting.
- The two URLs are deliberately distinct — a query param can't
  flip between them. URL discipline is a feature.

### Stage 4 — Per-source observations (3 min)

```sh
curl -sH "Authorization: Bearer $KEY" \
  "$BASE/observations?base=native&quote=fiat:USD" | jq '.data[:3]'
```

Talking points:
- The raw inputs the aggregator sees. Useful for customers
  who want to apply their own aggregation policy.
- `?source=binance` filters to one venue; `?aggregate=latest`
  collapses to one row per source.

### Stage 5 — Historical data (3 min)

```sh
curl -sH "Authorization: Bearer $KEY" \
  "$BASE/history/since-inception?asset=native&quote=fiat:USD&granularity=1d" \
  | jq '.data[:3]'
```

Talking points:
- Since-inception coverage — Galexie replays from ledger 2.
- Granularities: 1m / 15m / 1h / 4h / 1d / 1w / 1mo (same
  CAGGs the aggregator's closed-bucket path uses).
- CDN caches this aggressively (s-maxage=86400) — repeated
  requests are sub-10ms p99 once the edge is warm.

### Stage 6 — SSE streaming (3 min)

```sh
curl -NH "Authorization: Bearer $KEY" \
  "$BASE/price/stream?base=native&quote=fiat:USD"
```

Let it run for 60 seconds. One `data: {...}` line per closed
bucket. Talking points:
- Last-Event-ID resumption — disconnect + reconnect with the
  last seen ID, the server replays missed buckets from the
  Hub's ring buffer.
- Heartbeat every 15s as a comment line keeps proxies happy.

### Stage 7 — Asset detail (3 min)

```sh
curl -sH "Authorization: Bearer $KEY" \
  "$BASE/assets/native" | jq .
```

Talking points:
- F2 fields: `total_supply`, `circulating_supply`, `max_supply`,
  `market_cap_usd`, `fdv_usd`, `volume_24h_usd`, `supply_basis`.
- Supply data per ADR-0011 — three algorithms (XLM /
  classic / SEP-41) all wired.
- `change_24h_pct` is currently null; deferred per L7.7.

### Stage 8 — SDK demo (4 min)

Open the Go SDK example in `pkg/client/example_test.go` (or
the getting-started page). Talking points:
- Generic `Envelope[T]` shape — type-safe at the call site.
- `pkg/client` is SemVer-pinned; v0.x policy is documented in
  `docs/architecture/semver-policy.md`.

### Stage 9 — Q&A (5 min)

Common questions to expect:
- **"What if Reflector goes down?"** → Diversity factor,
  confidence drops; operators alert on confidence < 0.5
  sustained. Multi-source means single-oracle outage is
  mitigated.
- **"How do you handle USDC depegs?"** → Stablecoin classifier
  + per-class anomaly thresholds + freeze policy (ADR-0019).
  Demo: `flags.frozen` on the response when the freeze fires.
- **"What's the SLA?"** → Show `/sla` doc + the SLA-probe
  results dashboard.
- **"How fast can we get to 99.99%?"** → Honest answer:
  measurement requires ≥ 30 days production data, reported
  90 days post-launch (L7.2).
- **"What if we want a private deployment?"** → Apache-2.0
  source + ansible roles + bringup runbook are all public;
  point at `docs/operations/archival-node-bringup.md`.

## Post-demo

- [ ] **Share the recording** if recorded.
- [ ] **Send `onboarding-email.md`** with the customer's
      production API key (different from the demo key — rotate
      the demo key out same day).
- [ ] **Open a feedback issue** capturing any surface that
      drew confusion or any feature request raised.
- [ ] **L6.6 in `launch-readiness-backlog.md`** flips ✅ when
      the customer signs off.

## Cross-references

- [`docs/getting-started.md`](../getting-started.md) — the
  written walkthrough this demo follows.
- [`deploy/comms/onboarding-email.md`](../../deploy/comms/onboarding-email.md)
  — what to send the customer after.
- [`launch-day-checklist.md`](launch-day-checklist.md) §T-3
  — schedule the demo.
- L6.6 in [`launch-readiness-backlog.md`](../architecture/launch-readiness-backlog.md).
