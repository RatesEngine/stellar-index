import type { MetadataRoute } from 'next';

import { API_BASE_URL } from '@/api/client';
import { loadADRs } from '@/lib/adr';
import { loadArchitectureDocs } from '@/lib/architecture';
import { loadBlogPosts } from '@/lib/blog';
import { loadOperationsDocs } from '@/lib/operations';
import { fiatSlugFor } from '@/lib/fiat-slugs';

// Required for `output: 'export'` — sitemap is generated at build
// time and emitted as a static file. Same applies to robots.ts.
export const dynamic = 'force-static';

const SITE_URL = 'https://stellarindex.io';

/**
 * Build a sitemap URL that matches the canonical form the explorer
 * actually serves. With `trailingSlash: true` in next.config.js,
 * Next.js 308-redirects every non-trailing-slash URL to its
 * trailing-slash form. A sitemap full of redirect-targets is bad
 * SEO — Google penalises sitemaps that send crawlers through 308
 * hops, and the canonical form is already the trailing-slash one.
 *
 * `path` is the URL path relative to SITE_URL. Empty string is the
 * home page (`/`). Helper appends a `/` only when the path doesn't
 * already end with one (so a caller passing `/foo/` stays idempotent).
 */
function siteURL(path: string): string {
  if (path === '' || path === '/') return `${SITE_URL}/`;
  return path.endsWith('/') ? `${SITE_URL}${path}` : `${SITE_URL}${path}/`;
}

/**
 * sitemap.xml — generated at build time. Static pages are
 * enumerated explicitly; dynamic /assets/[slug] entries mirror
 * generateStaticParams: live API only, no seed fallback (the
 * /docs and /status routes have moved to the dedicated
 * docs.stellarindex.io and status.stellarindex.io subdomains
 * and are NOT enumerated here).
 */
export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  const now = new Date().toISOString();

  const staticPages: MetadataRoute.Sitemap = [
    '',
    '/assets',
    '/markets',
    '/issuers',
    '/sources',
    '/diagnostics',
    '/dexes',
    '/lending',
    '/aggregators',
    '/oracles',
    '/research',
    '/methodology',
    '/widgets',
    '/sdk',
    '/contact',
    '/changelog',
    '/anomalies',
    '/divergences',
    '/mev',
    '/exchanges',
    '/pricing',
    '/blog',
    '/company',
    '/careers',
    // NOTE: auth/app routes (/signin, /signup, /account) are deliberately
    // NOT listed — they're robots:noindex (no SEO value / private), and a
    // noindex URL in the sitemap is a Search Console error
    // ("Submitted URL marked 'noindex'").
  ].map((path) => ({
    url: siteURL(path),
    lastModified: now,
    changeFrequency: path === '' ? 'daily' : 'weekly',
    priority: path === '' ? 1 : 0.7,
  }));

  // Research pages: ADRs + curated architecture narratives. Both
  // surfaces are static-export pre-rendered and stable enough to
  // be worth indexing — they are cited from /methodology and from
  // every PR description.
  const adrPages: MetadataRoute.Sitemap = loadADRs().map((adr) => ({
    url: siteURL(`/research/adr/${adr.id}`),
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.5,
  }));
  const archPages: MetadataRoute.Sitemap = loadArchitectureDocs().map((d) => ({
    url: siteURL(`/research/architecture/${d.slug}`),
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.6,
  }));
  const opsPages: MetadataRoute.Sitemap = loadOperationsDocs().map((d) => ({
    url: siteURL(`/research/operations/${d.slug}`),
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.5,
  }));
  const blogPages: MetadataRoute.Sitemap = loadBlogPosts().map((p) => ({
    url: siteURL(`/blog/${p.slug}`),
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.6,
  }));

  const [
    assetSlugs,
    issuerKeys,
    currencyTickers,
    marketPairs,
    sources,
    lendingPools,
  ] = await Promise.all([
    fetchCoinSlugs(),
    fetchIssuerKeys(),
    fetchCurrencyTickers(),
    fetchMarketPairs(),
    fetchSources(),
    fetchLendingPools(),
  ]);
  const assetPages: MetadataRoute.Sitemap = assetSlugs.map((slug) => ({
    url: siteURL(`/assets/${slug}`),
    lastModified: now,
    changeFrequency: 'daily',
    priority: 0.6,
  }));
  const issuerPages: MetadataRoute.Sitemap = issuerKeys.map((g) => ({
    url: siteURL(`/issuers/${g}`),
    lastModified: now,
    changeFrequency: 'weekly',
    priority: 0.5,
  }));
  // Per-currency detail pages now live under /assets/{friendly-slug}
  // after the assets-unification migration. One entry per ticker —
  // canonical URL is the friendly form (us-dollar, japanese-yen, …).
  const currencyPages: MetadataRoute.Sitemap = currencyTickers.map((ticker) => ({
    url: siteURL(`/assets/${fiatSlugFor(ticker)}`),
    lastModified: now,
    changeFrequency: 'daily',
    priority: 0.7,
  }));

  // Per-pair detail pages. The route is /markets/{base}~{quote},
  // URL-encoded once. We pre-render the top 100 by 24h volume at
  // build time (see markets/[pair]/page.tsx); listing them in the
  // sitemap surfaces the highest-traffic pairs to crawlers without
  // exploding the file (every Stellar pair × 100s of issuers would
  // be tens of thousands of URLs of mostly-empty content).
  const marketPages: MetadataRoute.Sitemap = marketPairs.map((slug) => ({
    url: siteURL(`/markets/${encodeURIComponent(slug)}`),
    lastModified: now,
    changeFrequency: 'daily',
    priority: 0.6,
  }));
  // Per-source / per-exchange / per-dex detail pages. Every
  // source registry entry has a /sources/{name} page; only
  // ClassExchange entries with subclass=cex|dex have user-facing
  // /exchanges/{name} or /dexes/{source} pages.
  //
  // Pre-fix the sitemap emitted /exchanges/{name} AND /dexes/{name}
  // for *every* source — including aggregators (coingecko, cmc),
  // oracles (band, redstone, reflector-*), authority-sanity (ecb)
  // and lending (blend) — which produced ~33 sitemap entries that
  // 404'd at the page level. Google penalises sitemaps that
  // contain known-broken URLs, so we now gate emission on the
  // source's class+subclass to match the page's
  // generateStaticParams (CEX_INFO / DEX_INFO maps).
  const sourcePages: MetadataRoute.Sitemap = [];
  for (const s of sources) {
    sourcePages.push({
      url: siteURL(`/sources/${s.name}`),
      lastModified: now,
      changeFrequency: 'weekly',
      priority: 0.5,
    });
    if (s.class === 'exchange' && s.subclass === 'cex') {
      sourcePages.push({
        url: siteURL(`/exchanges/${s.name}`),
        lastModified: now,
        changeFrequency: 'weekly',
        priority: 0.5,
      });
    }
    if (s.class === 'exchange' && s.subclass === 'dex') {
      sourcePages.push({
        url: siteURL(`/dexes/${s.name}`),
        lastModified: now,
        changeFrequency: 'weekly',
        priority: 0.5,
      });
    }
  }
  // Lending pools — Blend pool detail pages. Small set today
  // (~9 pools), so list every one at priority 0.5.
  const lendingPages: MetadataRoute.Sitemap = lendingPools.map((id) => ({
    url: siteURL(`/lending/${id}`),
    lastModified: now,
    changeFrequency: 'weekly',
    priority: 0.5,
  }));

  return [
    ...staticPages,
    ...blogPages,
    ...adrPages,
    ...archPages,
    ...opsPages,
    ...assetPages,
    ...issuerPages,
    ...currencyPages,
    ...marketPages,
    ...sourcePages,
    ...lendingPages,
  ];
}

type SitemapSource = {
  name: string;
  class: string;
  subclass: string;
};

async function fetchSources(): Promise<SitemapSource[]> {
  try {
    const res = await fetch(`${API_BASE_URL}/v1/sources`, {
      signal: AbortSignal.timeout(5_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as {
      data: { name: string; class?: string; subclass?: string }[];
    };
    return (env.data ?? []).map((s) => ({
      name: s.name,
      class: s.class ?? '',
      subclass: s.subclass ?? '',
    }));
  } catch {
    return [];
  }
}

async function fetchLendingPools(): Promise<string[]> {
  try {
    const res = await fetch(`${API_BASE_URL}/v1/lending/pools`, {
      signal: AbortSignal.timeout(5_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    // /v1/lending/pools returns the pool contract address in the `pool`
    // field (matching the /lending/[pool] route's generateStaticParams).
    // Reading `contract_id` here (the field doesn't exist) emitted
    // `/lending/undefined` into the sitemap — a 404 Google penalises.
    const env = (await res.json()) as {
      data: { pool: string }[];
    };
    return (env.data ?? []).map((p) => p.pool).filter(Boolean);
  } catch {
    return [];
  }
}

async function fetchMarketPairs(): Promise<string[]> {
  try {
    // Match the per-pair generateStaticParams cap (500) so the
    // sitemap doesn't undercount the routes we actually
    // pre-render. Pre-2026-05-08 this was 100 in both places —
    // bumped together so Google sees the same surface that
    // returns 200.
    const res = await fetch(
      `${API_BASE_URL}/v1/markets?limit=500&order_by=volume_24h_usd_desc`,
      { signal: AbortSignal.timeout(5_000) },
    );
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as {
      data: { base: string; quote: string }[];
    };
    return (env.data ?? []).map((m) => `${m.base}~${m.quote}`);
  } catch {
    return [];
  }
}

async function fetchCurrencyTickers(): Promise<string[]> {
  // Migrated from /v1/currencies → /v1/assets/verified (rc.48 +
  // F-1201 audit-2026-05-12). The new endpoint returns the full
  // verified-currency catalogue with `class` ∈ {crypto, stablecoin,
  // fiat}; filter to fiat client-side so the sitemap only includes
  // the fiat tickers (which is what the per-currency converter
  // pages cover).
  try {
    const res = await fetch(`${API_BASE_URL}/v1/assets/verified`, {
      signal: AbortSignal.timeout(5_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as {
      data: Array<{ ticker: string; class: string }>;
    };
    return (env.data ?? [])
      .filter((row) => row.class === 'fiat')
      .map((row) => row.ticker);
  } catch {
    return [];
  }
}

async function fetchIssuerKeys(): Promise<string[]> {
  try {
    const res = await fetch(`${API_BASE_URL}/v1/issuers?limit=100`, {
      signal: AbortSignal.timeout(5_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: { g_strkey: string }[] };
    return (env.data ?? []).map((i) => i.g_strkey);
  } catch {
    return [];
  }
}

async function fetchCoinSlugs(): Promise<string[]> {
  try {
    const res = await fetch(`${API_BASE_URL}/v1/assets?limit=500`, {
      signal: AbortSignal.timeout(5_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    // /v1/assets returns rows with `slug` populated when sourced
    // from the coins reader (rc.47 lift). Fall back to `asset_id`
    // for any row without a friendly slug — those routes are still
    // pre-rendered under /assets/{asset_id}.
    const env = (await res.json()) as {
      data: { slug?: string; asset_id?: string }[];
    };
    return (env.data ?? [])
      .map((d) => d.slug || d.asset_id || '')
      .filter(Boolean);
  } catch {
    return [];
  }
}
