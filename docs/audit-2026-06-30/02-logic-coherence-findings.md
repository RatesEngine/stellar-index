---
title: Audit 2 — Logic / product-coherence findings register (LC-###)
status: findings drafted + key premises verified; remediation roadmap below
---

# Logic / product-coherence findings — register

Impact scale: **P0** breaks the Stellar-first mental model / actively misleads ·
**P1** notable friction or redundancy · **P2** polish. "Gated" = touches the
`pkg/client`/OpenAPI wire and waits for the reserved Unit-D pre-v1 break; "Now" =
non-breaking, shippable immediately. Full evidence + target designs in
[PLAN-2](PLAN-2-logic-coherence-audit.md); the flagship has a full impl sketch in
[fiat-split-implementation.md](fiat-split-implementation.md). ✔ = premise
re-verified against code this pass.

## P0 — breaks the mental model

| ID | Finding | Surface | Now/Gated | Effort |
|----|---------|---------|-----------|--------|
| **LC-001** ✔ | **19 fiat currencies listed as browseable Stellar assets, ranked above Stellar tokens by M2.** The flagship incoherence. `Browseable()` excludes only `reference_only`, not fiat; `/v1/assets/verified` + `asset_class=all` lead with CNY/JPY/USD by money supply. | `/v1/assets*`, `/assets`, `VerifiedCurrenciesStrip` | Now | M |
| **LC-002** ✔ | **`reference_only` coins (BTC/ETH/…) reachable as Stellar asset detail pages** (`/assets/btc` → `GlobalAssetView`). A non-Stellar coin page on a Stellar explorer. | `/v1/assets/{slug}`, `/assets/[slug]` | Now | S |
| **LC-020** ✔ | **Sidebar `/account/*` links never show active state + 404 off-Cloudflare.** Sidebar points at `/account/*` but pages are `/dashboard/*`; works only via CF 301; `isActive()` never matches the served URL; broken in `next dev`. | `Sidebar.tsx`, `_redirects` | Now | S |

## P1 — notable friction / redundancy

| ID | Finding | Now/Gated | Effort |
|----|---------|-----------|--------|
| **LC-003** | Verified-currency catalogue does double duty (Stellar trust registry + 15 non-Stellar reference coins). Extract the reference map → catalogue becomes a pure Stellar registry. | Now | M |
| **LC-010** | Cluster B: `/dexes` `/exchanges` `/sources` are 3 filtered views of one endpoint, each entity with **two** detail pages (`/dexes/[s]`+`/sources/[n]`). Biggest structural IA debt. | Now | L |
| **LC-011** ✔ | Cluster A: `/operation` is a detail page while its `/tx` `/ledger` `/contract` siblings are alias-redirects — inconsistent pattern; dead legacy `*View` impls. | Now | M |
| **LC-012** | Cluster C: protocol category stubs (`/amm`,`/yield`,`/bridges`,`/sdex`,`/liquidity-pools`) — same protocol via 3-4 doors; de-dup nav + name the canonical data home. | Now | M |
| **LC-021/022/023** | Nav: four labels for the DEX concept; fiat board has no nav home; `/markets` footer-only; Soroswap Router oddly top-level; `/docs` vs external doc destinations. | Now | M |
| **LC-030** | "coins" vs "currencies" vs "assets" three-way split — product says assets, internals say `CoinsReader`/`CoinRow`/`CurrenciesReader`. Converge on "asset". | Now (code) | M |
| **LC-040** | Dual-shape `/v1/assets/{slug}` (GlobalAssetView vs AssetDetail; explorer shape-sniffs `asset_id` + fetches both). Split routes or add a `kind` discriminator. | **Gated** | M |
| **LC-031** | Residual cross-chain wording in the wire (`AssetDetail.Class` = "cross-chain"; vestigial `NetworkEntry.Contract/ExternalLink`); **Unit D unshipped** → `pkg/client`/OpenAPI still carry multi-network shapes. | **Gated** | M |
| **LC-043** | ~50 legacy `/currencies/*` 301s near CF's 100-rule cap; consolidate before adding the fiat redirects. | Now | S |

## P2 — polish

| ID | Finding | Now/Gated |
|----|---------|-----------|
| **LC-013** | `/divergences` vs `/anomalies` overlap (divergence is a freeze reason on /anomalies). Merge or delineate. | Now |
| **LC-014** ✔ | Dead routes: bare `/convert`, `/convert/[from]`, `/research/adr` 404 (no index). | Now |
| **LC-032** | Repo dir still `ratesengine` (old brand). | Now |
| **LC-041** | Two-phase mixed-population cursor bakes fiat-first ordering into the wire (resolved by LC-001). | Now |

## W7 — Accessibility (WCAG 2.1 AA) — executed

**Headline: the prior 2026-06-14 Cmd-K Critical IS FIXED** (`SearchModal.tsx` now
has focus trap + restore + Escape + `role=dialog`/`aria-modal`) — **but the fix
landed on only 1 of 3 overlay surfaces.** Zero frontend tests, so none of this is
regression-guarded.

| ID | Sev | Finding |
|----|-----|---------|
| **LC-050** | Serious | **`RequestReveal` dialog** (the "show API request" tray on *every* panel) has no Escape, no focus trap, no focus move-in/restore — yet declares `role=dialog aria-modal`. Highest blast radius. (WCAG 2.4.3/2.1.1) |
| **LC-051** | Serious | **Mobile nav drawer** (`ConsoleShell.tsx`) — primary mobile nav — no focus trap/move-in/restore/dialog role (has Escape only). |
| **LC-052** | Serious | **Auth + dashboard form errors/success not announced** — plain `<div>`/`<Callout>`, no `role=alert`/`aria-live`; SR user submits and hears silence (sign-in, verify-code, key create/revoke). (WCAG 4.1.3) |
| **LC-053** | Moderate | `CurrencyCombobox` behaves as a combobox but exposes no ARIA (`aria-expanded`/`role=listbox`/`option`/`activedescendant`); highlight not scrolled into view. |
| **LC-054** | Moderate | Contrast fails: status badges `text-up/down` on `-subtle` ≈3.0–3.95:1; white on `up`/`bad-500` ≈3.3–3.76:1 (`<4.5:1`). `ProtocolView` used `-strong` correctly; `TxView`/`DirectionPill`/danger-button didn't. |
| **LC-055** | Minor | Unlabeled `<nav>` landmarks (×4); `Sparkline` `<svg aria-label>` lacks `role=img`; footer heading-level skip; `HomeTopMovers` renders `NaN%` + mis-colors 0% (unguarded `Number().toFixed`). |

**Correctly handled (not re-flagged):** skip link; up/down not color-alone
(DirectionPill pairs arrow+sign+aria-label); `<th scope=col>` defaults; no raw
`<img>` / no clickable `<div>` without role; label association; disciplined null
rendering (`—`); reduced-motion; focus-visible; text-scale tuned for AA;
`DegradedBanner` uses `role=status`.

## W8 — Empty / error / loading states — executed

| ID | Sev | Finding |
|----|-----|---------|
| **LC-056** | P1 (UX) | **Slow public views show a bare "Loading…" line, not a skeleton.** The exact slow pages (`/protocols` 5-17s, `/v1/tx` ~6s, `/markets`) render one muted text line for the entire wait; a `Skeleton` component exists + is used in the dashboard but not on the public data-heavy views. First-run users stare at one line for up to 17s. |
| (good) | — | Empty + error branches are otherwise handled well; null fields render `—`/`N/A`, not `null`/`$0`/`NaN` (except LC-055's HomeTopMovers). |

## W6 — Onboarding / dashboard / pricing-product coherence — executed

Core golden path works (magic-link → code → account-on-first-sight → dashboard;
key mint→reveal→copy); **the prior key-issuance bugs are FIXED** (rek_/sip_ prefix,
"last used 2055", instant-revoked — `dashboardkeys/handlers.go` `nilIfZero` + `sip_`).
Gaps are discoverability + route/doc coherence:

| ID | Sev | Finding |
|----|-----|---------|
| **LC-060** | **P0** | **Pricing product invisible on the landing page + primary nav — the commercial funnel leaks at the top.** The flagship *is* the pricing API, but the landing sells only "the protocol explorer"; the API section says "no auth, no API key"; nowhere does a visitor learn there are accounts/keys/tiers/SLAs. Primary nav "Developers" has docs/SDK/status but **no Pricing, no Sign-up**. `/pricing` is well-built but unreachable from where a first-timer looks. *Fix:* landing API-product section + "Get an API key →" CTA; add Pricing to primary nav; hero secondary CTA to `/pricing`. |
| **LC-061** | P1 | **Onboarding "first request" hint is a 404.** Dashboard `GettingStarted` step 3 = `GET /v1/price/XLM-USD`, but only `GET /v1/price?asset=&quote=` exists (no path route; `XLM` should be `native`) → a new dev's *first* copy-pasted call 404s. *Fix:* use `?asset=native&quote=fiat:USD` (reuse the home snippet). |
| **LC-062** | P1 | **Two doc destinations** (in-app `/docs` vs external `docs.stellarindex.io`, primary nav + hero + dashboard all point external; in-app reachable only via footer) + **inconsistent auth teaching** (`/docs` teaches `Authorization: Bearer`; dashboard/checklist teach `X-API-Key`). *Fix:* declare one canonical doc + unify on `X-API-Key`. (Operator: verify `docs.stellarindex.io` isn't a dead-end — not in this repo.) |
| **LC-063** | P2 | No bridge from the anonymous first-call → "now get a key for higher limits." |
| **LC-064** | P2 | **Business tier says 50,000 req/min on `/pricing`+`/signup` but 60,000 in the backend (`account.go`), `account-format.ts`, and the dashboard** — marketing contradicts the ladder on the page meant to close the sale. |
| **LC-065** | P2 | "Manage plan" overview CTA over-promises self-service billing (Stripe deferred; just routes to a `mailto:sales`). *Fix:* relabel "View plan". |

(LC-060/061 reinforce that LC-020 — the `/account/*` vs `/dashboard/*` nav split — also
causes reload+redirect on every signed-in click and a hard 404 off-Cloudflare.)

## Still-open (folded, not separately executed)

- **W9** user-facing copy terminology (currency/coin/rates → asset/price) — folds
  into LC-030.

## Remediation roadmap

1. **Ship-now bundle 1 (the flagship):** LC-001 + LC-002 + LC-043 per
   [fiat-split-implementation.md](fiat-split-implementation.md) — the user-requested
   `/external/fiat-currencies` split. (Non-breaking; highest visible impact.)
2. **Ship-now bundle 2 (cheap IA fixes):** LC-020 (Sidebar hrefs), LC-014 (dead
   routes), LC-021/022/023 (nav coherence incl. the new External section).
3. **Ship-now bundle 3 (structural):** LC-010/011/012 (source/DEX/market + route
   consolidation), LC-003/LC-030 (catalogue + naming convergence).
4. **Unit-D pre-v1 wire break (separate, deliberate):** LC-040 + LC-031.
5. **Quality sweeps:** W6-W9 (onboarding, a11y, states, copy) after the IA settles.
