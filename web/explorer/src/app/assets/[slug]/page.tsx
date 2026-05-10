import type { Metadata } from 'next';
import Link from 'next/link';
import { Suspense } from 'react';

import { Panel } from '@/components/reveal';
import { asExample, API_BASE_URL } from '@/api/client';
import { formatCompact, formatPrice } from '@/lib/format';
import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES } from '@/lib/seo';
import { AssetClientFallback } from './AssetClientFallback';
import { AssetTabs, ActiveTabSlot } from './AssetTabs';
import { AssetAbout } from './AssetAbout';
import { AssetConverter } from './AssetConverter';
import { ChartPanel } from './ChartPanel';
import { PriceSparklines } from './PriceSparklines';
import { IssuerPanel } from './IssuerPanel';
import { LiquidityTabPanel } from './LiquidityTabPanel';
import { MarketsTabPanel } from './MarketsTabPanel';
import { HistoryTabPanel } from './HistoryTabPanel';
import { SupplyTabPanel } from './SupplyTabPanel';

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
  try {
    // 10s here — generateStaticParams is a one-shot at build
    // time; the 500-asset listing query is heavier than a single
    // /v1/coins/{slug} hit so allow more headroom.
    const res = await fetch(`${API_BASE_URL}/v1/coins?limit=500`, {
      signal: AbortSignal.timeout(10_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: { coins: { slug: string }[] } };
    const slugs = (env.data?.coins ?? []).map((c) => c.slug);
    const seen = new Set<string>();
    const out: { slug: string }[] = [];
    for (const slug of [...fallback.map((f) => f.slug), ...slugs]) {
      if (!seen.has(slug)) {
        seen.add(slug);
        out.push({ slug });
      }
    }
    return out.length > 0 ? out : fallback;
  } catch {
    return fallback;
  }
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
}

interface PriceResp {
  price?: string;
  quote?: string;
  age_seconds?: number;
  flags?: { stale?: boolean; triangulated?: boolean };
}

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

// fetchCoin retries up to 3x with a 500ms backoff on network /
// 5xx errors. Build-time fetch failures previously baked
// "Asset not found" into the static HTML for every slug rendered
// during a bad CF Pages build window — the retry plus the client
// fallback in the !coin branch make that scenario survivable.
// 4xx (404 included) returns null on the first try, no retry.
async function fetchCoin(slug: string): Promise<CoinSummary | null> {
  if (isCIStub) return null;
  for (let attempt = 0; attempt < 3; attempt++) {
    try {
      const res = await fetch(
        `${API_BASE_URL}/v1/coins/${encodeURIComponent(slug)}`,
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
      return env.data ?? null;
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
  const coin = await fetchCoin(slug);
  const code = coin?.code ?? slug;
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

  const title = `${code}${suffix} — Stellar asset`;
  const description =
    priceNum != null
      ? `${code} on Stellar:${suffix} · live VWAP across on-chain DEXes, classic SDEX, and major exchanges.`
      : `Live price, markets, and issuer detail for ${code} on Stellar — VWAP'd across on-chain DEXes, classic SDEX, and major exchanges.`;

  // Canonical URL: prefer the API-returned slug (e.g. `XLM`,
  // `USDC`, the SAC-wrapped form for SAC tokens) over whatever
  // form the user typed (`xlm`, `usdc-GA5Z…`, etc). Without a
  // rel=canonical, Google would treat /assets/XLM and
  // /assets/native as separate pages with duplicate content.
  const canonicalSlug = coin?.slug ?? slug;
  const canonical = `https://ratesengine.net/assets/${canonicalSlug}`;

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
  const coin = await fetchCoin(slug);

  if (!coin) {
    // Build-time /v1/coins/{slug} fetch returned null. Two
    // possibilities: (a) the slug really doesn't exist, or (b)
    // the CF Pages build host couldn't reach api.ratesengine.net
    // during this build (cold connection pool / API restart). We
    // can't distinguish at build time, so we hand off to a client
    // fallback that retries from the user's browser and renders
    // the right state (real not-found vs. transient build issue).
    return (
      <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
        <header className="space-y-3">
          <nav className="text-xs text-slate-500">
            <Link href="/assets" className="hover:text-brand-600">
              Assets
            </Link>{' '}
            / <span>{slug}</span>
          </nav>
          <h1 className="text-3xl font-semibold tracking-tight">{slug}</h1>
        </header>
        <AssetClientFallback slug={slug} />
      </div>
    );
  }

  const [detail, price] = await Promise.all([
    fetchAssetDetail(coin.asset_id),
    fetchPrice(coin.asset_id),
  ]);

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
        item: 'https://ratesengine.net',
      },
      {
        '@type': 'ListItem',
        position: 2,
        name: 'Assets',
        item: 'https://ratesengine.net/assets',
      },
      {
        '@type': 'ListItem',
        position: 3,
        name: coin.code,
        item: `https://ratesengine.net/assets/${coin.slug}`,
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
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(breadcrumbLD) }}
      />
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: JSON.stringify(faqLD) }}
      />
      <header className="space-y-3">
        <nav className="text-xs text-slate-500">
          <Link href="/assets" className="hover:text-brand-600">
            Assets
          </Link>{' '}
          /{' '}
          <span className="text-slate-700 dark:text-slate-300">
            {coin.code}
          </span>
        </nav>
        <div className="flex flex-wrap items-baseline gap-4">
          <h1 className="text-3xl font-semibold tracking-tight">
            {coin.code}
          </h1>
          {detail?.type && (
            <span
              className="rounded-md bg-slate-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300"
              title="Asset type"
            >
              {detail.type}
            </span>
          )}
        </div>
        {detail?.home_domain && (
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Issuer home domain:{' '}
            <code className="font-mono">{detail.home_domain}</code>
          </p>
        )}
        {coin.issuer_scam_reason && (
          <div
            role="alert"
            className="rounded-md border border-red-300 bg-red-50 p-3 text-sm text-red-900 dark:border-red-800 dark:bg-red-950/40 dark:text-red-200"
          >
            <strong className="font-semibold">Known scam asset</strong> ·{' '}
            {coin.issuer_scam_reason}. The issuer is on the
            stellar.expert curated directory of malicious accounts —
            do not trust this asset, do not establish trustlines, and
            do not execute the prices below as if they reflected an
            honest market.
          </div>
        )}
      </header>

      <Suspense fallback={null}>
        <AssetTabs slug={coin.slug} hasIssuer={!!coin.issuer} />
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
          issuer={
            coin.issuer ? <IssuerPanel gStrkey={coin.issuer} /> : undefined
          }
          liquidity={
            <LiquidityTabPanel assetID={coin.asset_id} code={coin.code} />
          }
        />
      </Suspense>
    </div>
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
              <span className="rounded bg-amber-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-amber-800 dark:bg-amber-900/40 dark:text-amber-200">
                Stale
              </span>
            )}
            {price?.flags?.triangulated && (
              <span className="rounded bg-sky-100 px-2 py-0.5 text-[11px] uppercase tracking-wider text-sky-800 dark:bg-sky-900/40 dark:text-sky-200">
                Triangulated via XLM
              </span>
            )}
          </div>
          <PriceSparklines
            points24h={coin.price_history_24h ?? []}
            points7d={coin.price_history_7d ?? []}
          />
          <dl className="grid grid-cols-2 gap-3 border-t border-slate-200 pt-3 text-sm dark:border-slate-800 sm:grid-cols-3 lg:grid-cols-5">
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
              value={`#${coin.first_seen_ledger.toLocaleString()}`}
            />
            <Stat
              label="Last seen"
              mono
              value={`#${coin.last_seen_ledger.toLocaleString()}`}
            />
          </dl>
        </Panel>
      </div>

      <Panel
        title="External views"
        hint="Cross-reference this asset on other Stellar explorers"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400"
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
              <span className="text-[10px] uppercase tracking-wider text-slate-400">↗</span>
            </a>
            <span className="ml-2 text-xs text-slate-400">
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
              <span className="ml-2 font-mono text-xs text-slate-400">
                {coin.issuer.slice(0, 8)}…{coin.issuer.slice(-4)}
              </span>
            </li>
          )}
        </ul>
      </Panel>

      <AssetConverter symbol={coin.code} priceUSD={priceNum} />

      {coin.top_markets && coin.top_markets.length > 0 && (
        <Panel
          title="Top markets"
          hint={`${coin.top_markets.length} most active by 24h volume`}
          source={asExample('/v1/coins/{slug}', { slug: coin.slug })}
          bodyClassName="-mx-4"
        >
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
                  <th className="px-4 py-2 font-medium">Side</th>
                  <th className="px-4 py-2 font-medium">vs</th>
                  <th className="px-4 py-2 text-right font-medium">24h volume</th>
                  <th className="px-4 py-2 text-right font-medium">24h trades</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                {coin.top_markets.map((m) => {
                  const pairURL = topMarketHref(coin.asset_id, m);
                  return (
                    <tr
                      key={`${m.side}|${m.counterparty}`}
                      className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                    >
                      <td className="px-4 py-3">
                        <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                          {m.side}
                        </span>
                      </td>
                      <td className="px-4 py-3 font-mono text-xs">
                        {pairURL ? (
                          <Link
                            href={pairURL}
                            className="text-slate-700 hover:text-brand-600 hover:underline dark:text-slate-300"
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
                          <span className="text-slate-300 dark:text-slate-700">—</span>
                        )}
                      </td>
                      <td className="px-4 py-3 text-right font-mono tabular-nums text-slate-500">
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
              <dt className="text-[11px] uppercase tracking-wider text-slate-500">
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
      a: `${symbol} is one of the assets we index on the Stellar network. Rates Engine pulls live trades for it from the Soroban DEX corpus (Soroswap, Phoenix, Aquarius, Comet) plus the classic SDEX order book, plus CEX feeds (Binance, Coinbase, Kraken, Bitstamp) where the symbol exists. The price you see is a 24h-trailing VWAP across every active venue.`,
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
    <details className="group rounded-lg border border-slate-200 dark:border-slate-800">
      <summary className="flex cursor-pointer items-center justify-between px-3 py-2 font-medium text-slate-900 hover:bg-slate-50 dark:text-slate-100 dark:hover:bg-slate-900/40">
        <span>{q}</span>
        <span aria-hidden className="text-xs text-slate-400 group-open:rotate-45 transition-transform">+</span>
      </summary>
      <p className="border-t border-slate-200 px-3 py-2 text-sm leading-relaxed text-slate-700 dark:border-slate-800 dark:text-slate-300">
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
    <span className="inline-flex items-center gap-1 rounded bg-sky-50 px-2 py-0.5 font-mono text-xs uppercase tracking-wider text-sky-700 dark:bg-sky-950/40 dark:text-sky-300">
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
      ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
      : n < 0
        ? 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300'
        : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400';
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
      ? 'text-emerald-600 dark:text-emerald-400'
      : accentTone === 'amber'
        ? 'text-amber-600 dark:text-amber-400'
        : accentTone === 'rose'
          ? 'text-rose-600 dark:text-rose-400'
          : 'text-slate-500 dark:text-slate-400';
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-slate-500">
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
