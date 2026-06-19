import type { Metadata } from 'next';
import Link from 'next/link';
import { Suspense } from 'react';

import { Panel } from '@/components/reveal';
import { asExample, API_BASE_URL } from '@/api/client';
import { formatCompact, formatPrice } from '@/lib/format';
import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES, serializeJsonLd } from '@/lib/seo';
import {
  Badge,
  Breadcrumbs,
  Callout,
  Container,
} from '@/components/ui';
import { AssetClientFallback } from './AssetClientFallback';
import { AssetSidebar } from './AssetSidebar';
import { AssetTabs, ActiveTabSlot } from './AssetTabs';
import { AssetAbout } from './AssetAbout';
import { ChartPanel } from './ChartPanel';
import { PriceSparklines } from './PriceSparklines';
import { IssuerPanel } from './IssuerPanel';
import { LiquidityTabPanel } from './LiquidityTabPanel';
import { HoldersTabPanel } from './HoldersTabPanel';
import { MarketsTabPanel } from './MarketsTabPanel';
import { HistoryTabPanel } from './HistoryTabPanel';
import { SupplyTabPanel } from './SupplyTabPanel';
import { lookupGlobalAsset, getCatalogue, type GlobalAssetView } from '../catalogue';

/**
 * /assets/[slug] — single asset detail page.
 *
 * Server component fetches every panel's data from the live API
 * at request time. There is no seed / synthesised content;
 * fields the API doesn't yet expose render as `—` rather than
 * a fabricated value.
 */
export async function generateStaticParams() {
  // Build-time fetch from the live API so every asset returned
  // by /v1/coins (capped at 500 per page today) gets a
  // pre-rendered route. CI builds without network connectivity
  // fall back to a single canonical slug so static export has
  // something to anchor on — Next.js refuses to build a dynamic
  // route under output:'export' with zero params.
  //
  // Native XLM is always force-included: it has no row in
  // classic_assets so `/v1/coins?limit=500` never returns it,
  // but the API has a special-case path for slug "XLM" / "native"
  // (see handleCoin → GetNativeCoinRow). Without this, the most
  // important asset on the network would 404 on the explorer.
  const fallback = [{ slug: 'XLM' }, { slug: 'native' }];
  // Reuse the build-time listing cache that fetchCoin populates
  // — generateStaticParams runs first, so this is what primes
  // the cache for the per-page renders that follow. One API call
  // does double duty.
  const cache = await getBuildCoinsCache();
  // Verified-currency catalogue slugs (us-dollar, chinese-yuan,
  // usdc, …) aren't in /v1/coins (which only knows about Stellar-
  // network assets), but they ARE valid /assets/[slug] routes that
  // render the cross-chain identity view. Pull them from
  // /v1/assets/verified so they get pre-rendered too.
  const verifiedSlugs = await fetchVerifiedSlugsForStaticParams();
  if ((!cache || cache.size === 0) && verifiedSlugs.length === 0) {
    return fallback;
  }
  // For catalogue entries we emit BOTH cases so user-typed URLs
  // and existing table links both resolve. /v1/coins emits
  // uppercase short-form slugs (XLM, USDC, AQUA); /v1/assets/verified
  // emits lowercase catalogue slugs. Without both, either the
  // explorer's own tables 404 (they link uppercase from /v1/coins)
  // or user-typed lowercase URLs 404.
  //
  // Non-catalogue Stellar assets (unverified codes like sBNB,
  // FOO, etc) keep their /v1/coins casing — they only ever appear
  // in one form.
  const seen = new Set<string>();
  const out: { slug: string }[] = [];
  const verifiedSet = new Set(verifiedSlugs.map((s) => s.toLowerCase()));
  const cacheKeys = cache ? Array.from(cache.keys()) : [];
  for (const slug of [
    ...verifiedSlugs,
    ...cacheKeys,
    ...fallback.map((f) => f.slug),
  ]) {
    if (!seen.has(slug)) {
      seen.add(slug);
      out.push({ slug });
    }
    // For catalogue slugs, also emit the alternate casing so
    // both /assets/usdc/ and /assets/USDC/ resolve. /v1/coins
    // gives us USDC; verified gives us usdc; both should work.
    const lower = slug.toLowerCase();
    const upper = slug.toUpperCase();
    if (verifiedSet.has(lower)) {
      if (!seen.has(lower)) {
        seen.add(lower);
        out.push({ slug: lower });
      }
      if (!seen.has(upper)) {
        seen.add(upper);
        out.push({ slug: upper });
      }
    }
  }
  return out.length > 0 ? out : fallback;
}

type Params = Promise<{ slug: string }>;

interface CoinSummary {
  asset_id: string;
  code: string;
  slug: string;
  issuer: string;
  observation_count: number;
  first_seen_ledger: number;
  last_seen_ledger: number;
  // Optional per-row metrics from /v1/coins/{slug} — null when
  // the asset has no off-chain peg / supply snapshot yet.
  price_usd?: string | null;
  volume_24h_usd?: string | null;
  market_cap_usd?: string | null;
  circulating_supply?: string | null;
  change_1h_pct?: string | null;
  change_24h_pct?: string | null;
  change_7d_pct?: string | null;
  // Top 5 markets the asset participates in (as base or
  // quote), ordered by 24h USD volume desc. Empty array
  // when the asset has no recent trades.
  top_markets?: TopMarket[];
  // 24 hourly USD-price samples (oldest first) covering the
  // trailing 24h. Each entry: { t: RFC3339, p: rounded-to-10dp
  // USD price or null }.
  price_history_24h?: { t: string; p?: string | null }[];
  // 7 daily USD-price samples (oldest first) covering the
  // trailing 7 days. Same shape as price_history_24h.
  price_history_7d?: { t: string; p?: string | null }[];
  // Count of distinct (base, quote) pairs the asset
  // participated in over the trailing 24h. 0 when the asset
  // went silent in that window.
  markets_count?: number | null;
  // All-time-high USD price + day it was set. Null when the
  // asset has no USD-quoted history. Sourced from prices_1d
  // filtered to USD-denominated quotes only — triangulated
  // paths excluded.
  ath?: { usd: string; at: string } | null;
  // Trade count over the trailing 24h (asset on either side).
  // Read from the trades hypertable directly. Companion to the
  // all-time `observation_count`.
  trade_count_24h?: number | null;
  // Non-empty when the asset's issuer is on the curated scam list
  // (sourced from stellar.expert's directory). Mirrors the
  // `scam_reason` field on /v1/issuers; surfaced here so the asset
  // detail page can render a warning at first paint instead of
  // waiting for the IssuerPanel fetch to complete.
  issuer_scam_reason?: string | null;
}

interface TopMarket {
  counterparty: string;
  side: 'base' | 'quote';
  volume_24h_usd?: string | null;
  trade_count_24h: number;
}

interface AssetDetail {
  asset_id: string;
  code: string;
  issuer?: string;
  type?: string;
  home_domain?: string;
  total_supply?: string;
  circulating_supply?: string;
  max_supply?: string;
  market_cap_usd?: string;
  fdv_usd?: string;
  volume_24h_usd?: string;
  supply_basis?: string;
  // Verified-currency collision warning (R-018 Phase 1.4a). Present
  // when the requested asset's code matches a verified currency's
  // Stellar ticker (USDC, EURC, AQUA, …) but the issuer doesn't
  // match the verified entry — someone has issued a fake "USDC" on
  // Stellar with their own G-strkey. Renders a prominent banner
  // linking to the verified asset.
  unverified_warning?: {
    verified_slug: string;
    verified_asset_id: string;
    verified_name: string;
    verified_issuer?: string;
    note: string;
  };
}

interface PriceResp {
  price?: string;
  quote?: string;
  age_seconds?: number;
  flags?: { stale?: boolean; triangulated?: boolean };
}

/**
 * GlobalAssetView is the wire shape `/v1/assets/{slug}` returns
 * when `{slug}` is a verified-currency catalogue slug (USDC, EURC,
 * AQUA, …). Distinct from the AssetDetail shape above which the
 * SAME endpoint returns for canonical asset_ids like `USDC-G5Z…`
 * or `native`. See R-018 Phase 1.4a for the dispatch rationale.
 *
 * The page fetches both: AssetDetail is the per-Stellar-asset
 * surface (always; keyed off coin.asset_id), and GlobalAssetView
 * is the cross-chain identity surface (when the route's slug
 * happens to be a verified-currency slug).
 */
// GlobalAssetView lives in ../catalogue.ts.

// Static export hits every page once at build time. CI's stub
// hostname doesn't resolve, and Node's DNS retry budget swallows
// the AbortSignal — bypass network entirely when the URL looks
// like a CI placeholder.
const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

// 8s per fetch. The previous 2s was too aggressive — every detail
// page was rendering "Asset not found" because the build's first
// /v1/coins/{slug} hit (cold connection pool) timed out, fetchCoin
// returned null, and the page render branched into the not-found
// state. With ~500 pages × 4 fetches × 8s worst case = ~30 min,
// well within CF Pages's 20-min build window per page (parallelised),
// and in practice the API responds in <300ms steady-state.
const BUILD_FETCH_TIMEOUT_MS = 8_000;

// Build-time listing cache. The previous per-slug fetchCoin made
// up to 500 parallel `/v1/coins/{slug}` calls during static
// export — even at ~300ms steady-state that's an api-side burst
// that ran into the per-handler 8s ceiling for slugs that landed
// during a brief slow window. The fallback "couldn't be
// prerendered" message then baked into the HTML for the unlucky
// slugs (XLM hit this in production).
//
// Fix: fetch the entire 500-row listing ONCE (with the same
// includes the page needs), build a Map<slug, CoinSummary>, and
// have fetchCoin read from the cache first. Falls back to the
// per-slug retry loop only when the slug isn't in the cache
// (e.g. a slug Next was asked about that wasn't in the listing —
// shouldn't happen normally since generateStaticParams derives
// from the same listing).
let buildCoinsCachePromise:
  | Promise<Map<string, CoinSummary> | null>
  | null = null;

function getBuildCoinsCache(): Promise<Map<string, CoinSummary> | null> {
  if (buildCoinsCachePromise) return buildCoinsCachePromise;
  buildCoinsCachePromise = (async () => {
    if (isCIStub) return null;
    try {
      // Migrated to /v1/assets?limit=500 — R-018 finish. Wire shape
      // is `{data: [AssetDetail], pagination: {next}}`. AssetDetail
      // is a superset of CoinSummary so every read column on this
      // page still resolves. Defensive on both shapes during the
      // migration overlap (in case a stale build hits an older API).
      const res = await fetch(
        `${API_BASE_URL}/v1/assets?limit=500&include=sparkline,sparkline7d,ath`,
        { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS * 2) },
      );
      if (!res.ok) return null;
      const env = (await res.json()) as {
        data: { coins?: CoinSummary[] } | CoinSummary[];
      };
      const rows = Array.isArray(env.data)
        ? env.data
        : (env.data?.coins ?? []);
      // Index by slug ONLY — not asset_id. The cache previously
      // double-indexed (slug + asset_id), which made
      // generateStaticParams emit ~1000 routes (500 short-form
      // slugs + 500 long-form asset_ids like USDC-GA5Z…). Both
      // routes rendered the same content; the long-form copies
      // were duplicate static work that consistently hung the
      // CF Pages build worker (180s × 3 retries on
      // /assets/USDC-GA5Z…). The canonical URL is the short
      // slug anyway; long-form navigations resolve via the same
      // page through the API at runtime.
      const map = new Map<string, CoinSummary>();
      for (const c of rows) {
        if (c.slug) map.set(c.slug, c);
      }
      return map;
    } catch {
      return null;
    }
  })();
  return buildCoinsCachePromise;
}

// fetchVerifiedSlugsForStaticParams pulls the verified-currency
// catalogue slugs from the shared catalogue (single
// /v1/assets/verified call, memoised across this route's
// generateStaticParams).
async function fetchVerifiedSlugsForStaticParams(): Promise<string[]> {
  const map = await getCatalogue();
  return Array.from(map.keys());
}

// fetchCoin retries up to 3x with a 500ms backoff on network /
// 5xx errors. Build-time fetch failures previously baked
// "Asset not found" into the static HTML for every slug rendered
// during a bad CF Pages build window — the retry plus the client
// fallback in the !coin branch make that scenario survivable.
// 4xx (404 included) returns null on the first try, no retry.
//
// Shape discriminator: /v1/assets/{slug} returns TWO different
// wire shapes (per CLAUDE.md "Things that will surprise you"):
//   - GlobalAssetView (catalogue slug like "usdc", "us-dollar",
//     "btc"): keys are ticker/slug/name/class. NO
//     asset_id, NO code.
//   - AssetDetail (canonical asset_id like "USDC-GA5Z...",
//     "native"): keys are asset_id/code/issuer/...
// fetchCoin only handles the AssetDetail case — when the response
// is a GlobalAssetView shape, return null so the page routes to
// VerifiedCurrencyView via the !coin branch. The discriminator is
// `asset_id` being present + truthy.
async function fetchCoin(slug: string): Promise<CoinSummary | null> {
  if (isCIStub) return null;
  // Cache-first: one listing call covers up to 500 rows. Per-slug
  // fetches only fire on misses (slugs the listing didn't return).
  const cache = await getBuildCoinsCache();
  if (cache) {
    const hit = cache.get(slug);
    if (hit) return hit;
  }
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      const res = await fetch(
        `${API_BASE_URL}/v1/assets/${encodeURIComponent(slug)}`,
        { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
      );
      if (res.status >= 400 && res.status < 500) return null;
      if (!res.ok) {
        if (attempt < 2) {
          await new Promise((resolve) => setTimeout(resolve, 500));
          continue;
        }
        return null;
      }
      const env = (await res.json()) as { data: CoinSummary };
      const data = env.data ?? null;
      // GlobalAssetView (catalogue slug) discriminator — the
      // catalogue dispatch response has no asset_id. Treat as
      // null so the caller renders VerifiedCurrencyView.
      if (!data || typeof data.asset_id !== 'string' || !data.asset_id) {
        return null;
      }
      return data;
    } catch {
      if (attempt < 2) {
        await new Promise((resolve) => setTimeout(resolve, 500));
        continue;
      }
      return null;
    }
  }
  return null;
}

async function fetchAssetDetail(assetId: string): Promise<AssetDetail | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/assets/${encodeURIComponent(assetId)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: AssetDetail };
    return env.data ?? null;
  } catch {
    return null;
  }
}

/**
 * fetchGlobalAsset reads from the shared build-time catalogue
 * (single /v1/assets/verified call, retried on 429, memoised at
 * module level — see ../catalogue.ts). Per-slug /v1/assets/{slug}
 * calls at build time would 429 in parallel against r1's
 * anon-tier rate limit and leave every cross-chain page rendering
 * as "Asset not found".
 */
async function fetchGlobalAsset(slug: string): Promise<GlobalAssetView | null> {
  return lookupGlobalAsset(slug);
}

async function fetchPriceDirect(
  asset: string,
  quote: string,
): Promise<PriceResp | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/price?asset=${encodeURIComponent(asset)}&quote=${encodeURIComponent(quote)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: PriceResp };
    return env.data ?? null;
  } catch {
    return null;
  }
}

// fetchPrice tries direct asset→USD first; on 404 it triangulates
// via XLM. Many active classic Stellar assets only trade against
// XLM (or stablecoins) on SDEX, so the aggregator's per-pair USD
// VWAP doesn't exist. The client-side compose lets the page show
// a real USD price anyway (asset/XLM × XLM/USD), tagged as
// triangulated so the user can see the provenance.
//
// XLM (asset_id "native") short-circuits — its direct USD VWAP
// is the canonical answer.
async function fetchPrice(assetId: string): Promise<PriceResp | null> {
  if (assetId === 'native') {
    return fetchPriceDirect('native', 'fiat:USD');
  }
  const direct = await fetchPriceDirect(assetId, 'fiat:USD');
  if (direct?.price) return direct;
  // Triangulate via XLM.
  const [vsXlm, xlmUsd] = await Promise.all([
    fetchPriceDirect(assetId, 'native'),
    fetchPriceDirect('native', 'fiat:USD'),
  ]);
  if (!vsXlm?.price || !xlmUsd?.price) return null;
  const a = Number(vsXlm.price);
  const b = Number(xlmUsd.price);
  if (!Number.isFinite(a) || !Number.isFinite(b) || a <= 0 || b <= 0) {
    return null;
  }
  const triangulated = (a * b).toFixed(12);
  return {
    price: triangulated,
    quote: 'fiat:USD',
    age_seconds: Math.max(
      vsXlm.age_seconds ?? 0,
      xlmUsd.age_seconds ?? 0,
    ),
    flags: { triangulated: true },
  };
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { slug } = await params;
  const [coin, globalView] = await Promise.all([
    fetchCoin(slug),
    fetchGlobalAsset(slug),
  ]);
  const code = globalView?.ticker ?? coin?.code ?? slug;
  const priceNum = coin?.price_usd ? Number(coin.price_usd) : null;
  const change24h = coin?.change_24h_pct ? Number(coin.change_24h_pct) : null;

  // Build a price + change suffix so the social-share preview is
  // dynamic — "USDC $1.0005 +0.05% 24h" reads as a real ticker
  // rather than boilerplate.
  let suffix = '';
  if (priceNum != null && Number.isFinite(priceNum)) {
    const priceStr =
      priceNum >= 1
        ? `$${priceNum.toFixed(priceNum >= 100 ? 2 : 4)}`
        : priceNum >= 0.001
          ? `$${priceNum.toFixed(6)}`
          : `$${priceNum.toExponential(3)}`;
    suffix = ` ${priceStr}`;
    if (change24h != null && Number.isFinite(change24h)) {
      const sign = change24h > 0 ? '+' : '';
      suffix += ` (${sign}${change24h.toFixed(2)}% 24h)`;
    }
  }

  // Verified-currency catalogue match → "<Class>" framing.
  // Stellar-only (no catalogue) → "Stellar asset" framing.
  const classLabel = globalView?.class
    ? globalView.class.charAt(0).toUpperCase() + globalView.class.slice(1)
    : null;
  const title = globalView
    ? `${code}${suffix} — ${classLabel ?? 'Verified asset'}`
    : `${code}${suffix} — Stellar asset`;
  const description = globalView
    ? `${globalView.name}: live prices and markets on Stellar.`
    : priceNum != null
      ? `${code} on Stellar:${suffix} · live VWAP across on-chain DEXes, classic SDEX, and major exchanges.`
      : `Live price, markets, and issuer detail for ${code} on Stellar — VWAP'd across on-chain DEXes, classic SDEX, and major exchanges.`;

  // Canonical URL: catalogue lowercase slug wins (cross-chain
  // identity is the canonical entity), then API-returned slug
  // (e.g. `XLM`, `USDC`, the SAC-wrapped form), then whatever
  // form the user typed. Without rel=canonical, Google would
  // treat /assets/XLM and /assets/native as separate pages with
  // duplicate content.
  const canonicalSlug = globalView?.slug ?? coin?.slug ?? slug;
  const canonical = `https://stellarindex.io/assets/${canonicalSlug}`;

  return {
    title,
    description,
    alternates: { canonical },
    openGraph: {
      title,
      description,
      url: canonical,
      type: 'website',
      images: SITE_OG_IMAGES,
    },
    twitter: {
      card: 'summary_large_image',
      title,
      description,
      images: SITE_TWITTER_IMAGES,
    },
  };
}

export default async function AssetDetailPage({ params }: { params: Params }) {
  const { slug } = await params;
  const [coin, globalViewEarly] = await Promise.all([
    fetchCoin(slug),
    fetchGlobalAsset(slug),
  ]);

  // For verified-catalogue slugs, /assets/[slug] is the
  // verified-currency identity page. This branch fires regardless
  // of whether /v1/coins also returned a row for the slug (USDC
  // has rows; XLM has native); the catalogue dispatch is
  // authoritative for the parent page.
  if (globalViewEarly) {
    return <VerifiedCurrencyView slug={slug} view={globalViewEarly} />;
  }

  if (!coin) {
    // No catalogue entry AND no /v1/coins row. Two reasons:
    //   (a) slug really doesn't exist
    //   (b) build host couldn't reach the API — fall back to the
    //       client component to retry from the user's browser.
    return (
      <Container className="space-y-8 py-8 sm:py-10">
        <header className="space-y-3">
          <Breadcrumbs
            items={[{ label: 'Assets', href: '/assets' }, { label: slug }]}
          />
          <h1 className="text-h1 font-semibold text-ink">{slug}</h1>
        </header>
        <AssetClientFallback slug={slug} />
      </Container>
    );
  }

  const [detail, price] = await Promise.all([
    fetchAssetDetail(coin.asset_id),
    fetchPrice(coin.asset_id),
  ]);
  // Reaching this point means slug had a /v1/coins row but NO
  // catalogue entry — an unverified Stellar asset. There's no
  // cross-chain identity to render, so globalView is null and
  // every `globalView?.x` reference below short-circuits. Cast
  // through to keep the wider type so downstream `?.` chains
  // typecheck without further narrowing pain.
  const globalView = globalViewEarly as GlobalAssetView | null;

  // Schema.org BreadcrumbList — gives Google a structured
  // hierarchy (Home → Assets → XLM) so search results can
  // render the breadcrumb path under the title.
  const breadcrumbLD = {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      {
        '@type': 'ListItem',
        position: 1,
        name: 'Home',
        item: 'https://stellarindex.io',
      },
      {
        '@type': 'ListItem',
        position: 2,
        name: 'Assets',
        item: 'https://stellarindex.io/assets',
      },
      {
        '@type': 'ListItem',
        position: 3,
        name: coin.code,
        item: `https://stellarindex.io/assets/${coin.slug}`,
      },
    ],
  };
  // Schema.org FAQPage — the same Q/A pairs that render in the
  // visible AssetFAQ panel below. Emitting them as JSON-LD lets
  // Google pick them up for rich-snippet rendering on currency-
  // pair queries like "what is XLM" / "how is USDC priced".
  // Source of truth is assetFaqFor; the visible panel and this
  // structured-data block read from the same function so the
  // copy can never drift.
  const faqLD = {
    '@context': 'https://schema.org',
    '@type': 'FAQPage',
    mainEntity: assetFaqFor(coin.code, !!coin.issuer).map((entry) => ({
      '@type': 'Question',
      name: entry.q,
      acceptedAnswer: {
        '@type': 'Answer',
        text: entry.a,
      },
    })),
  };
  return (
    <Container className="space-y-8 py-8 sm:py-10">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: serializeJsonLd(breadcrumbLD) }}
      />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: serializeJsonLd(faqLD) }}
      />
      <header className="space-y-3">
        <Breadcrumbs
          items={[
            { label: 'Assets', href: '/assets' },
            { label: coin.code },
          ]}
        />
        <div className="flex flex-wrap items-baseline gap-4">
          <h1 className="text-h1 font-semibold text-ink">
            {coin.code}
          </h1>
          {globalView?.name && globalView.name !== coin.code && (
            <span className="text-lg text-ink-muted">
              {globalView.name}
            </span>
          )}
          {globalView && (
            <Badge
              tone="ok"
              title={
                globalView.verified_issuer
                  ? `Verified by ${globalView.verified_issuer}`
                  : 'Verified currency'
              }
            >
              <svg
                xmlns="http://www.w3.org/2000/svg"
                viewBox="0 0 20 20"
                fill="currentColor"
                className="h-3 w-3"
                aria-hidden="true"
              >
                <path
                  fillRule="evenodd"
                  d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
                  clipRule="evenodd"
                />
              </svg>
              Verified
            </Badge>
          )}
          {detail?.type && (
            <Badge title="Asset type">{detail.type}</Badge>
          )}
        </div>
        {globalView?.verified_issuer && (
          <p className="text-sm text-ink-body">
            Issued by{' '}
            <span className="font-medium text-ink-body">
              {globalView.verified_issuer}
            </span>
          </p>
        )}
        {!globalView?.verified_issuer && detail?.home_domain && (
          <p className="text-sm text-ink-body">
            Issuer home domain:{' '}
            <code className="font-mono">{detail.home_domain}</code>
          </p>
        )}
        {coin.issuer_scam_reason && (
          <div role="alert">
            <Callout tone="bad" title="Known scam asset">
              {coin.issuer_scam_reason}. The issuer is on the stellar.expert
              curated directory of malicious accounts — do not trust this asset,
              do not establish trustlines, and do not execute the prices below as
              if they reflected an honest market.
            </Callout>
          </div>
        )}
        {detail?.unverified_warning && (
          <div
            role="alert"
            className="rounded-md border border-warn-300 bg-warn-50 p-3 text-sm text-warn-700"
          >
            <div className="mb-1 flex items-center gap-2">
              <strong className="font-semibold">
                Unverified {coin.code}
              </strong>
              <Badge tone="warn">Ticker collision</Badge>
            </div>
            <p>
              {detail.unverified_warning.note} The verified asset is
              available at{' '}
              <Link
                href={`/assets/${detail.unverified_warning.verified_slug}`}
                className="font-medium underline hover:text-warn-700"
              >
                {detail.unverified_warning.verified_name}
              </Link>
              {detail.unverified_warning.verified_issuer && (
                <span>
                  {' '}— issued by{' '}
                  <span className="font-medium">
                    {detail.unverified_warning.verified_issuer}
                  </span>
                </span>
              )}
              .
            </p>
          </div>
        )}
      </header>

      <div className="grid gap-6 lg:grid-cols-[336px_minmax(0,1fr)]">
        <aside className="space-y-4 lg:sticky lg:top-20 lg:self-start">
          <AssetSidebar
            coin={coin}
            detail={detail}
            priceUSD={
              price?.price
                ? Number(price.price)
                : coin.price_usd
                  ? Number(coin.price_usd)
                  : null
            }
            name={globalView?.name}
            homeDomain={detail?.home_domain}
          />
        </aside>

        <div className="min-w-0 space-y-4">
          {(() => {
            const parts = coin.asset_id.split('-');
            const issuer =
              parts.length === 2 && parts[1].startsWith('G') ? parts[1] : null;
            return issuer ? (
              <Suspense fallback={null}>
                <IssuerPanel gStrkey={issuer} />
              </Suspense>
            ) : null;
          })()}

          <Suspense fallback={null}>
            <AssetTabs slug={coin.slug} hasIssuer={false} />
          </Suspense>

          <Suspense fallback={null}>
            <ActiveTabSlot
              overview={
                <OverviewBody coin={coin} detail={detail} price={price} />
              }
              chart={<ChartPanel assetID={coin.asset_id} />}
              markets={<MarketsTabPanel assetID={coin.asset_id} />}
              history={<HistoryTabPanel assetID={coin.asset_id} />}
              supply={<SupplyTabPanel assetID={coin.asset_id} />}
              holders={<HoldersTabPanel assetID={coin.asset_id} />}
              liquidity={
                <LiquidityTabPanel assetID={coin.asset_id} code={coin.code} />
              }
            />
          </Suspense>
        </div>
      </div>
    </Container>
  );
}

function OverviewBody({
  coin,
  detail,
  price,
}: {
  coin: CoinSummary;
  detail: AssetDetail | null;
  price: PriceResp | null;
}) {
  const priceNum = parsePrice(price?.price);
  const hasSupply =
    detail?.circulating_supply != null ||
    detail?.total_supply != null ||
    detail?.max_supply != null ||
    detail?.market_cap_usd != null ||
    detail?.fdv_usd != null;
  return (
    <div className="space-y-4">
      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Panel
          title="Price"
          source={asExample('/v1/price', { asset: coin.asset_id, quote: 'fiat:USD' })}
          panelId="price-card"
          className="lg:col-span-2"
          bodyClassName="space-y-4"
        >
          <div className="flex flex-wrap items-baseline gap-4">
            <span className="font-mono text-3xl tabular-nums">
              {priceNum != null ? `$${formatPrice(priceNum)}` : '—'}
            </span>
            {(() => {
              const peg = peggedTo(coin.code);
              if (peg) {
                return <PeggedBadge currency={peg} />;
              }
              return (
                <>
                  {coin.change_1h_pct != null && (
                    <ChangePctLabel raw={coin.change_1h_pct} window="1h" />
                  )}
                  {coin.change_24h_pct != null && (
                    <ChangePctLabel raw={coin.change_24h_pct} window="24h" />
                  )}
                  {coin.change_7d_pct != null && (
                    <ChangePctLabel raw={coin.change_7d_pct} window="7d" />
                  )}
                </>
              );
            })()}
            {price?.flags?.stale && (
              <span className="rounded bg-warn-50 px-2 py-0.5 text-[11px] uppercase tracking-wider text-warn-700">
                Stale
              </span>
            )}
            {price?.flags?.triangulated && (
              <span className="rounded bg-brand-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-brand-800">
                Triangulated via XLM
              </span>
            )}
          </div>
          <PriceSparklines
            points24h={coin.price_history_24h ?? []}
            points7d={coin.price_history_7d ?? []}
          />
          <dl className="grid grid-cols-2 gap-3 border-t border-line pt-3 text-sm sm:grid-cols-3 lg:grid-cols-5">
            <Stat
              label="Volume 24h"
              value={fmtUsd(detail?.volume_24h_usd ?? coin.volume_24h_usd)}
            />
            <Stat
              label="Markets 24h"
              value={
                coin.markets_count != null
                  ? coin.markets_count.toLocaleString()
                  : '—'
              }
            />
            <Stat
              label="Market cap"
              value={fmtUsd(detail?.market_cap_usd ?? coin.market_cap_usd)}
            />
            <Stat
              label="Circulating"
              value={fmtNum(detail?.circulating_supply ?? coin.circulating_supply)}
            />
            <Stat
              label={
                coin.ath?.at
                  ? `ATH · ${coin.ath.at.slice(0, 10)}`
                  : 'ATH'
              }
              value={fmtUsd(coin.ath?.usd ?? null)}
              accent={athDrawdown(coin.price_usd, coin.ath?.usd)?.label}
              accentTone={athDrawdown(coin.price_usd, coin.ath?.usd)?.tone}
            />
          </dl>
        </Panel>

        <Panel title="Observations" panelId="obs-card">
          <dl className="grid grid-cols-2 gap-2 text-sm">
            <Stat
              label="Total"
              value={formatCompact(coin.observation_count)}
            />
            <Stat
              label="Trades 24h"
              value={
                coin.trade_count_24h != null
                  ? formatCompact(coin.trade_count_24h)
                  : '—'
              }
            />
            <Stat
              label="First seen"
              mono
              value={
                coin.first_seen_ledger != null
                  ? `#${coin.first_seen_ledger.toLocaleString()}`
                  : '—'
              }
            />
            <Stat
              label="Last seen"
              mono
              value={
                coin.last_seen_ledger != null
                  ? `#${coin.last_seen_ledger.toLocaleString()}`
                  : '—'
              }
            />
          </dl>
        </Panel>
      </div>

      <Panel
        title="External views"
        hint="Cross-reference this asset on other Stellar explorers"
        bodyClassName="text-sm text-ink-body"
      >
        <ul className="space-y-2">
          <li>
            <a
              href={
                coin.asset_id === 'native'
                  ? 'https://stellar.expert/explorer/public/asset/XLM'
                  : `https://stellar.expert/explorer/public/asset/${coin.asset_id.replace('-', '-')}`
              }
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1.5 hover:text-brand-600 hover:underline"
            >
              stellar.expert
              <span className="text-[10px] uppercase tracking-wider text-ink-faint">↗</span>
            </a>
            <span className="ml-2 text-xs text-ink-faint">
              holders, supply, on-chain history
            </span>
          </li>
          {coin.issuer && (
            <li>
              <Link
                href={`/issuers/${coin.issuer}`}
                className="inline-flex items-center gap-1.5 hover:text-brand-600 hover:underline"
              >
                Issuer detail
              </Link>
              <span className="ml-2 font-mono text-xs text-ink-faint">
                {coin.issuer.slice(0, 8)}…{coin.issuer.slice(-4)}
              </span>
            </li>
          )}
        </ul>
      </Panel>

      {coin.top_markets && coin.top_markets.length > 0 && (
        <Panel
          title="Top markets"
          hint={`${coin.top_markets.length} most active by 24h volume`}
          source={asExample('/v1/assets/{slug}', { slug: coin.slug })}
          bodyClassName="-mx-4"
        >
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-line text-sm">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
                  <th className="px-4 py-2 font-medium">Side</th>
                  <th className="px-4 py-2 font-medium">vs</th>
                  <th className="px-4 py-2 text-right font-medium">24h volume</th>
                  <th className="px-4 py-2 text-right font-medium">24h trades</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line-subtle">
                {coin.top_markets.map((m) => {
                  const pairURL = topMarketHref(coin.asset_id, m);
                  return (
                    <tr
                      key={`${m.side}|${m.counterparty}`}
                      className="hover:bg-surface-muted"
                    >
                      <td className="px-4 py-3">
                        <span className="rounded bg-surface-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-body">
                          {m.side}
                        </span>
                      </td>
                      <td className="px-4 py-3 font-mono text-xs">
                        {pairURL ? (
                          <Link
                            href={pairURL}
                            className="text-ink-body hover:text-brand-600 hover:underline"
                          >
                            {shortCounterparty(m.counterparty)}
                          </Link>
                        ) : (
                          shortCounterparty(m.counterparty)
                        )}
                      </td>
                      <td className="px-4 py-3 text-right">
                        {m.volume_24h_usd ? (
                          <span className="font-mono tabular-nums">
                            ${fmtCompact(Number(m.volume_24h_usd))}
                          </span>
                        ) : (
                          <span className="text-ink-faint">—</span>
                        )}
                      </td>
                      <td className="px-4 py-3 text-right font-mono tabular-nums text-ink-muted">
                        {fmtCompact(m.trade_count_24h)}
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </div>
        </Panel>
      )}

      {hasSupply && (
        <Panel
          title="Supply"
          hint="From /v1/assets — circulating / total / max where the supply pipeline has computed them."
          source={asExample('/v1/assets/{asset_id}', { asset_id: coin.asset_id })}
        >
          <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
            {detail?.circulating_supply != null && (
              <Stat
                label="Circulating"
                value={fmtNum(detail.circulating_supply)}
              />
            )}
            {detail?.total_supply != null && (
              <Stat label="Total" value={fmtNum(detail.total_supply)} />
            )}
            {detail?.max_supply != null && (
              <Stat label="Max" value={fmtNum(detail.max_supply)} />
            )}
            {detail?.fdv_usd != null && (
              <Stat label="FDV" value={fmtUsd(detail.fdv_usd)} />
            )}
            {detail?.supply_basis && (
              <Stat label="Supply basis" value={detail.supply_basis} />
            )}
          </dl>
        </Panel>
      )}
      {coin.issuer && (
        <Panel
          title="Issuer"
          source={asExample(`/v1/issuers/${coin.issuer}`)}
        >
          <dl className="grid grid-cols-1 gap-2 text-sm sm:grid-cols-2">
            <div>
              <dt className="text-[11px] uppercase tracking-wider text-ink-muted">
                G-strkey
              </dt>
              <dd className="font-mono text-xs">
                <Link
                  href={`/issuers/${coin.issuer}`}
                  className="hover:text-brand-600"
                  title={coin.issuer}
                >
                  {`${coin.issuer.slice(0, 12)}…${coin.issuer.slice(-6)}`}
                </Link>
              </dd>
            </div>
            {detail?.home_domain && (
              <Stat label="Home domain" mono value={detail.home_domain} />
            )}
          </dl>
        </Panel>
      )}

      <AssetAbout symbol={coin.code} />
      <AssetFAQ symbol={coin.code} hasIssuer={!!coin.issuer} />
    </div>
  );
}

// (CURATED_ASSET_ABOUT extracted to ./AssetAbout — the panel is a
// 'use client' component so it can collapse paragraphs behind a
// "Read more →" toggle.)

// CURATED_FAQ — generic answers parameterised by the asset's
// own code so the same five-question set renders sensibly for
// every asset.
function assetFaqFor(symbol: string, hasIssuer: boolean): { q: string; a: string }[] {
  const issuerNote = hasIssuer
    ? `As a classic credit asset, ${symbol} has a designated issuer account holding the canonical issuance authority — see the Issuer panel above for SEP-1 metadata, auth flags, and the home domain that pinned the issuer's identity.`
    : `As a Soroban-native or smart-contract token, ${symbol} doesn't have a classic Stellar issuer account. Its issuance is governed by the contract's own logic; on-chain mint/burn events drive its supply.`;
  return [
    {
      q: `What is ${symbol}?`,
      a: `${symbol} is one of the assets we index on the Stellar network. Stellar Index pulls live trades for it from the Soroban DEX corpus (Soroswap, Phoenix, Aquarius, Comet) plus the classic SDEX order book, plus CEX feeds (Binance, Coinbase, Kraken, Bitstamp) where the symbol exists. The price you see is a 24h-trailing VWAP across every active venue.`,
    },
    {
      q: `Where does the price come from?`,
      a: `We compute a volume-weighted average across every connected exchange that's actively trading ${symbol} in the trailing 24 hours. Source-class exchanges (CEX + on-chain DEX) contribute by default; aggregators and oracles are reported alongside but excluded from the VWAP itself to avoid double-counting upstream markets.`,
    },
    {
      q: `What is circulating supply for a Stellar asset?`,
      a: `For classic credit assets we use the issuer's current balance held by non-issuer accounts (the on-chain definition of "in circulation"); for Soroban tokens we track mint/burn events on the contract. SEP-1 fixed_number / max_number declarations from the issuer's stellar.toml override the on-chain count when the issuer pledges a hard cap.`,
    },
    {
      q: `${symbol} issuer details`,
      a: issuerNote,
    },
    {
      q: `How fresh is this data?`,
      a: `On-chain trades land in the indexer within ~6 seconds of the ledger close (the Stellar consensus cadence). CEX feeds stream live via WebSocket; the 24h VWAP recomputes continuously. The chart's last-trade timestamp shows the most recent observation we ingested for this asset.`,
    },
  ];
}

// (AssetAbout extracted to ./AssetAbout for the read-more toggle.)

function AssetFAQ({ symbol, hasIssuer }: { symbol: string; hasIssuer: boolean }) {
  const items = assetFaqFor(symbol, hasIssuer);
  return (
    <Panel
      title="FAQ"
      hint="Common questions about this asset"
      bodyClassName="space-y-2 text-sm"
    >
      {items.map((it, i) => (
        <AssetFAQItem key={i} q={it.q} a={it.a} />
      ))}
    </Panel>
  );
}

function AssetFAQItem({ q, a }: { q: string; a: string }) {
  return (
    <details className="group rounded-lg border border-line">
      <summary className="flex cursor-pointer items-center justify-between px-3 py-2 font-medium text-ink hover:bg-surface-muted">
        <span>{q}</span>
        <span aria-hidden className="text-xs text-ink-faint group-open:rotate-45 transition-transform">+</span>
      </summary>
      <p className="border-t border-line px-3 py-2 text-sm leading-relaxed text-ink-body">
        {a}
      </p>
    </details>
  );
}

function parsePrice(raw: string | undefined): number | null {
  if (!raw) return null;
  const n = Number(raw);
  return Number.isFinite(n) ? n : null;
}

function fmtUsd(raw: string | null | undefined): string {
  if (!raw) return '—';
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  return `$${formatCompact(n)}`;
}

function fmtNum(raw: string | null | undefined): string {
  if (!raw) return '—';
  const n = Number(raw);
  if (!Number.isFinite(n)) return '—';
  return formatCompact(n);
}

// athDrawdown computes the % drop from ATH given the current
// price + ATH price (both as wire-format strings). Returns null
// when either side is missing or invalid; otherwise returns a
// formatted label + colour tone matching the AssetsTable
// FromATH column so the same signal looks the same wherever
// it appears.
function athDrawdown(
  priceRaw: string | null | undefined,
  athRaw: string | null | undefined,
): { label: string; tone: 'emerald' | 'amber' | 'rose' | 'slate' } | null {
  if (!priceRaw || !athRaw) return null;
  const p = Number(priceRaw);
  const a = Number(athRaw);
  if (!Number.isFinite(p) || !Number.isFinite(a) || a <= 0) return null;
  const pct = ((p - a) / a) * 100;
  const label = pct > 0 ? '0.0%' : `${pct.toFixed(1)}%`;
  const tone =
    pct > -1 ? 'emerald' : pct > -25 ? 'slate' : pct > -75 ? 'amber' : 'rose';
  return { label, tone };
}

// ChangePctLabel renders a signed percentage with emerald-up /
// rose-down / slate-zero colour. Accepts the wire-format string
// (e.g. "+1.27", "-0.05", "0.00") and the window label.
// peggedTo recognises the well-known stablecoins on Stellar by
// asset code and returns the fiat they're soft-pegged to. Used to
// suppress the meaningless change pills (a 0.00% / 0.05% pill on
// USDC tells the reader nothing — "Pegged to USD" is honest).
//
// Codes are case-sensitive on Stellar (alphanum4 / alphanum12);
// pegs not on this list still show change pills as before.
function peggedTo(code: string): string | null {
  switch (code) {
    case 'USDC':
    case 'USDT':
    case 'PYUSD':
    case 'DAI':
    case 'BUSD':
    case 'TUSD':
    case 'USDP':
      return 'USD';
    case 'EURC':
    case 'EUROC':
    case 'EUROB':
      return 'EUR';
    case 'MXNe':
      return 'MXN';
    case 'BRZ':
      return 'BRL';
    case 'GBPC':
      return 'GBP';
    case 'AUDD':
      return 'AUD';
    case 'NGNT':
      return 'NGN';
    default:
      return null;
  }
}

function PeggedBadge({ currency }: { currency: string }) {
  return (
    <span className="inline-flex items-center gap-1 rounded bg-brand-50 px-2 py-0.5 font-mono text-xs uppercase tracking-wider text-brand-700">
      <span className="text-[10px] opacity-70">PEG</span>
      {currency}
    </span>
  );
}

function ChangePctLabel({
  raw,
  window,
}: {
  raw: string | null | undefined;
  window: '1h' | '24h' | '7d';
}) {
  if (raw == null) return null;
  const n = Number(raw);
  if (!Number.isFinite(n)) return null;
  const tone =
    n > 0
      ? 'bg-up-subtle text-up'
      : n < 0
        ? 'bg-down-subtle text-down'
        : 'bg-surface-subtle text-ink-body';
  const sign = n > 0 ? '+' : '';
  return (
    <span
      className={`rounded px-2 py-0.5 font-mono text-xs tabular-nums ${tone}`}
    >
      {sign}
      {n.toFixed(2)}%
      <span className="ml-1 text-[10px] uppercase tracking-wider opacity-70">
        {window}
      </span>
    </span>
  );
}

// fmtCompact wraps formatCompact for inline use in table cells —
// the Markets preview uses it for both USD volume and the trade
// count column.
function fmtCompact(n: number): string {
  return formatCompact(n);
}

// topMarketHref builds the /markets/{base}~{quote} URL for one of
// the asset's top markets. The asset on /assets/[slug] is the
// "us" side; `m.side` says whether we were base or quote, and
// the counterparty is the OTHER asset_id. Counterparty strings
// like `fiat:USD` aren't routable on /markets/[pair] (no asset_id),
// so we return null in that case and fall back to plain text.
function topMarketHref(
  ourAssetID: string,
  m: TopMarket,
): string | null {
  const cp = m.counterparty;
  if (!cp || cp.startsWith('fiat:') || cp.startsWith('crypto:')) return null;
  const base = m.side === 'base' ? ourAssetID : cp;
  const quote = m.side === 'base' ? cp : ourAssetID;
  return `/markets/${encodeURIComponent(`${base}~${quote}`)}`;
}

// shortCounterparty renders a counterparty asset_id compactly:
// `<code>-<short-issuer>` for classic, `crypto:<sym>` straight,
// numeric (XLM trustline form) → "XLM", `fiat:USD` → "USD".
function shortCounterparty(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical;
  if (/^\d+$/.test(canonical)) return 'XLM';
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  const code = canonical.slice(0, dashIx);
  const issuer = canonical.slice(dashIx + 1);
  return `${code} (${issuer.slice(0, 6)}…${issuer.slice(-4)})`;
}

function Stat({
  label,
  value,
  mono,
  accent,
  accentTone,
}: {
  label: string;
  value: string;
  mono?: boolean;
  // Optional secondary signal rendered as a small coloured chip
  // after the value. Used by the ATH stat to surface the drawdown
  // alongside the raw all-time-high price.
  accent?: string;
  accentTone?: 'emerald' | 'slate' | 'amber' | 'rose';
}) {
  const accentColor =
    accentTone === 'emerald'
      ? 'text-up'
      : accentTone === 'amber'
        ? 'text-warn-700'
        : accentTone === 'rose'
          ? 'text-down'
          : 'text-ink-muted';
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-ink-muted">
        {label}
      </dt>
      <dd className={mono ? 'font-mono text-xs' : 'tabular-nums'}>
        {value}
        {accent && (
          <span className={`ml-1.5 text-[11px] font-mono ${accentColor}`}>
            {accent}
          </span>
        )}
      </dd>
    </div>
  );
}

// VerifiedCurrencyView renders /assets/{verified-slug} for slugs
// that exist in the verified-currency catalogue but have no row in
// /v1/coins (fiat tickers like us-dollar / chinese-yuan; crypto
// tickers that don't trade on Stellar like usdt / wbtc). Without
// this branch those routes 404'd at build time because the page's
// primary fetchCoin returned null and the AssetClientFallback
// loops on a /v1/coins/{slug} retry that's never going to succeed.
//
// Renders: header with name + ticker + class + USD price (from
// GlobalAssetView.price_usd); chart panel (only fires for
// fiat:fiat pairs via /v1/chart's fx_quotes path — crypto
// verified slugs without a Stellar issuer skip the chart).
function VerifiedCurrencyView({
  slug,
  view,
}: {
  slug: string;
  view: GlobalAssetView;
}) {
  const isFiat = (view as GlobalAssetView & { class?: string }).class === 'fiat';
  // For fiat tickers the canonical chart asset_id is `fiat:<ISO>`.
  // For crypto verified slugs we don't have a slug-level chart.
  const chartAssetID = isFiat ? `fiat:${view.ticker}` : null;
  const priceNum = view.price_usd ? Number(view.price_usd) : null;
  return (
    <Container className="space-y-8 py-8 sm:py-10">
      <header className="space-y-3">
        <Breadcrumbs
          items={[
            { label: 'Assets', href: '/assets' },
            { label: view.ticker },
          ]}
        />
        <h1 className="flex flex-wrap items-baseline gap-3 text-h1 font-semibold text-ink">
          <span>{view.name}</span>
          <span className="font-mono text-base text-ink-muted">
            {view.ticker}
          </span>
          <Badge>
            {(view as GlobalAssetView & { class?: string }).class ?? 'verified'}
          </Badge>
        </h1>
        {priceNum != null && Number.isFinite(priceNum) && (
          <div className="font-mono text-2xl tnum text-ink">
            ${priceNum < 0.001 ? priceNum.toExponential(3) : priceNum.toFixed(priceNum >= 100 ? 2 : 6)}
            <span className="ml-2 text-xs text-ink-muted">USD</span>
          </div>
        )}
        {view.description && (
          <p className="max-w-prose text-[15px] leading-relaxed text-ink-muted">
            {view.description}
          </p>
        )}
      </header>

      {chartAssetID && (
        <ChartPanel assetID={chartAssetID} />
      )}

      {/* Markets panel — surfaces every market the asset trades on
          across both Stellar SDEX and CEX feeds. The slug is
          passed through to /v1/markets?asset=<slug> which the
          server expands (R-018 phase 2) into every catalogue
          asset_id form for the slug and unions trade rows. So
          /assets/USDC shows USDC-GA5Z... (SDEX), and BTC/ETH/XLM
          tickers show their crypto:<TICKER> CEX pairs from
          Binance/Coinbase/Kraken/Bitstamp under one panel. */}
      <MarketsTabPanel assetID={view.slug} />

      {slug && (
        <p className="text-xs text-ink-muted">
          Slug:{' '}
          <code className="font-mono">{slug}</code>
        </p>
      )}
    </Container>
  );
}
