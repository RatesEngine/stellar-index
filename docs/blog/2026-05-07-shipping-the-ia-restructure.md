---
title: Shipping the IA restructure
date: 2026-05-07
author: Rates Engine team
summary: A look at the v0.5.0-rc.22 release — IA restructure, magic-link auth, /currencies launch, sparkline coverage across every per-source page, and the backend work behind it.
---

The site got a substantial restructure in this release. The old flat
nav (Assets / Markets / DEXes / Lending / Aggregators / Oracles /
Network / Methodology / Research / SDK / Docs) collapsed into the
grouped IA we always wanted but kept deferring:

- **Currencies** — top-level for the new world-fiat surface.
- **Blockchain ▾** — Assets, Exchanges, Dexes, Lending,
  Aggregators, Oracles, Networks.
- **API Docs** — straight to docs.ratesengine.net.
- **About ▾** — Pricing, Blog, API status, Company, Careers,
  Contact.
- **Sign in / Create account** — magic-link, no passwords.

## What landed alongside the restructure

This release is bigger than just nav. The IA reshape forced a real
audit of every page — half of them were stub data or pre-launch
placeholders. We wired them up against real endpoints and where the
endpoint didn't exist, we built it.

### Backend additions

- `GET /v1/oracle/streams` — every active price stream from every
  oracle, latest observation per (source, asset, quote) over the
  trailing 7d. Backs the new "price streams" table on /oracles.
- `GET /v1/lending/pools` — Blend pools observed in the auction
  stream, with 24h / all-time auction counts + 30d unique users.
- `GET /v1/currencies` + `GET /v1/currencies/{ticker}` — world fiat
  rates via a free [currency-api](https://github.com/fawazahmed0/exchange-api)
  shim. Cached in-memory, refreshed hourly. Per-currency detail
  includes 7d history + cross-rates against every other supported
  ticker.
- `?include=stats,sparkline` opt-in flag on `GET /v1/sources` —
  attaches per-(source, hour) USD-volume buckets so listing pages
  can render mini charts without N+1 fetches.
- `?include=sparkline` opt-in on `GET /v1/coins` — same idea, batched
  per-asset 24h hourly history via a new
  `Store.GetCoinsPriceHistory24hBatch`.
- `price_history_7d` on `GET /v1/coins/{slug}` — daily samples
  alongside the existing 24h hourly series.
- XLM/USD-fallback CTE on /v1/sources, /v1/pools, and the lending
  query — Soroban DEX trades against the XLM SAC wrapper used to
  show `null` USD volume because the operator's USD-pegged Phase 1
  allow-list doesn't include XLM itself. The vol_24h CTE now derives
  from base/quote_amount × XLM/USD when the XLM SAC is on either
  side, so Phoenix / Aquarius / Comet pools show real volume.

### Auth: magic-link end-to-end

The whole auth flow rebuilt on the existing dashboardauth
infrastructure (which already had magic-link tokens + session
cookies). The /signup form used to mint API keys inline — now it
sends a magic link, the callback creates the account on first
sign-in, and API keys live under the account.

- `/signin` and `/signup` share a `SignInForm` with magic-link
  POST → "check your inbox" flow.
- `/auth/callback` on the explorer forwards to the API's
  `/v1/auth/callback` so the Set-Cookie applies and the 303
  redirect lands on `/account` logged in.
- `/account` reads the session cookie via `credentials: include`,
  shows user email + account info + tier, lists API keys with
  per-key revoke buttons.
- `DELETE /v1/account/keys/{keyID}` revokes by ID, scoped to the
  caller's identifier, with 409 if you try to revoke the credential
  you're authenticated with.
- `/v1/account/me` extended with optional `{user, account}` nested
  objects for session callers (API-key callers continue to populate
  the existing top-level fields).
- Navbar shows your email in a chip with a sign-out dropdown when
  you're authenticated; "Sign in / Create account" CTAs otherwise.

### Per-account daily usage tracking

`GET /v1/account/usage` used to return an empty list (the wire
shape was locked but the rollup writer hadn't shipped). The new
`internal/usage` package writes per-(subject, day) Redis INCRs in
a UsageTracker middleware that runs after rate-limit (so 429s
don't pollute counters). The handler reads the trailing 30 days;
/account renders a 30-day request bar chart above the keys
section.

### Sparkline coverage

Every per-source page renders the same `volume_history_24h` shape:

- `/dexes` overview table — 24h sparkline column per protocol.
- `/exchanges` table — same column for CEXes.
- `/dexes/{source}` and `/exchanges/{name}` detail pages — wider
  sparkline below the activity grid.
- `/sources/{name}` registry detail — same.
- `/assets` listing — per-row 24h sparkline + 7d toggle on the
  detail page.
- `/currencies` listing — 7d % change column + tiny sparkline.

### IA pages

- `/aggregators` — adds a "Reference price aggregators" table for
  CG/CMC/CryptoCompare alongside the on-chain Soroswap-Router /
  DeFindex cards.
- `/lending` — pools table beneath the Blend overview card.
- `/oracles` — full two-table rebuild (oracles + price streams).
- `/exchanges` — real CEX summary table replacing the placeholder
  shell, with per-exchange detail pages backed by
  `/v1/markets?source=<name>`.
- `/currencies` — sortable listing with rate, 7d %, sparkline,
  search; per-currency converter + cross-rates table; embeddable
  iframe widget at `/embed/currency/[ticker]`.

### Mobile-nav fix

The IA restructure shipped a `hidden md:flex` desktop nav without a
mobile fallback — < 768px screens saw only the logo. New hamburger
drawer mirrors the desktop dropdowns with collapsible sections.

## What's not in this release

We're honest about gaps because the alternative is shipping mock
data:

- **Order-book depth on /exchanges.** The IA spec mentions this;
  we don't ingest it. Trade-derived 24h volume + trade count are
  what the page shows today.
- **DEX TVL / liquidity on /dexes.** Same situation — we observe
  swap events, not pool reserves. The protocol overview surfaces
  active pool count + 24h volume + 24h trades; a real liquidity
  number wires in once the per-pool reserve reader ships.
- **1h / 24h % change windows on /currencies.** The free
  currency-api shim is daily-grain; finer granularity needs a paid
  forex feed.
- **Pure SEP-41/SEP-41 swap USD volume.** Our XLM/USD fallback
  prices any trade touching XLM but token↔token Soroban swaps
  contribute zero to USD volume until a per-token oracle layer
  wires in.

These are documented in [the launch-readiness backlog][backlog] and
the per-page "what's not on this page yet" footers.

## What's next

v1 ships in the coming weeks. Order-book depth, DEX TVL, and the
paid forex feed are the headline post-v1 priorities; the running
list is in the [launch-readiness backlog][backlog].

If you want to follow the per-release ledger, the [changelog feed][feed]
is also available as Atom.

[backlog]: https://github.com/RatesEngine/rates-engine/blob/main/docs/architecture/launch-readiness-backlog.md
[feed]: https://ratesengine.net/changelog.atom
