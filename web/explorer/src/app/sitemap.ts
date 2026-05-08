import type { MetadataRoute } from 'next';

import { API_BASE_URL } from '@/api/client';
import { loadADRs } from '@/lib/adr';
import { loadArchitectureDocs } from '@/lib/architecture';
import { loadBlogPosts } from '@/lib/blog';
import { loadDiscoveryDocs } from '@/lib/discovery';
import { loadOperationsDocs } from '@/lib/operations';
import { friendlySlugFor } from '@/app/currencies/[ticker]/slugs';

// Required for `output: 'export'` — sitemap is generated at build
// time and emitted as a static file. Same applies to robots.ts.
export const dynamic = 'force-static';

const SITE_URL = 'https://ratesengine.net';

/**
 * sitemap.xml — generated at build time. Static pages are
 * enumerated explicitly; dynamic /assets/[slug] entries mirror
 * generateStaticParams: live API only, no seed fallback (the
 * /docs and /status routes have moved to the dedicated
 * docs.ratesengine.net and status.ratesengine.net subdomains
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
    '/networks',
    '/research',
    '/methodology',
    '/widgets',
    '/sdk',
    '/contact',
    '/changelog',
    '/anomalies',
    '/divergences',
    '/mev',
    '/currencies',
    '/exchanges',
    '/pricing',
    '/blog',
    '/company',
    '/careers',
    '/signin',
    '/signup',
    '/account',
  ].map((path) => ({
    url: `${SITE_URL}${path}`,
    lastModified: now,
    changeFrequency: path === '' ? 'daily' : 'weekly',
    priority: path === '' ? 1 : 0.7,
  }));

  // Research pages: ADRs + curated architecture narratives. Both
  // surfaces are static-export pre-rendered and stable enough to
  // be worth indexing — they are cited from /methodology and from
  // every PR description.
  const adrPages: MetadataRoute.Sitemap = loadADRs().map((adr) => ({
    url: `${SITE_URL}/research/adr/${adr.id}`,
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.5,
  }));
  const archPages: MetadataRoute.Sitemap = loadArchitectureDocs().map((d) => ({
    url: `${SITE_URL}/research/architecture/${d.slug}`,
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.6,
  }));
  const discoveryPages: MetadataRoute.Sitemap = loadDiscoveryDocs().map((d) => ({
    url: `${SITE_URL}/research/discovery/${d.slug}`,
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.5,
  }));
  const opsPages: MetadataRoute.Sitemap = loadOperationsDocs().map((d) => ({
    url: `${SITE_URL}/research/operations/${d.slug}`,
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.5,
  }));
  const blogPages: MetadataRoute.Sitemap = loadBlogPosts().map((p) => ({
    url: `${SITE_URL}/blog/${p.slug}`,
    lastModified: now,
    changeFrequency: 'monthly',
    priority: 0.6,
  }));

  const [assetSlugs, issuerKeys, currencyTickers, marketPairs] = await Promise.all([
    fetchCoinSlugs(),
    fetchIssuerKeys(),
    fetchCurrencyTickers(),
    fetchMarketPairs(),
  ]);
  const assetPages: MetadataRoute.Sitemap = assetSlugs.map((slug) => ({
    url: `${SITE_URL}/assets/${slug}`,
    lastModified: now,
    changeFrequency: 'daily',
    priority: 0.6,
  }));
  const issuerPages: MetadataRoute.Sitemap = issuerKeys.map((g) => ({
    url: `${SITE_URL}/issuers/${g}`,
    lastModified: now,
    changeFrequency: 'weekly',
    priority: 0.5,
  }));
  // Per-currency detail pages. Both forms are pre-rendered:
  //   /currencies/{ticker}        — bare ISO 4217 code (USD, EUR…)
  //   /currencies/{friendly-slug} — curated SEO-friendly form
  //                                 (us-dollar, japanese-yen…)
  // The friendly-slug form is the canonical share URL when a
  // curated entry exists; the ISO form is preserved so a typed
  // /currencies/USD doesn't 404. Sitemap lists both with the
  // friendly form at slightly higher priority so search indexers
  // pick that as canonical.
  const currencyPages: MetadataRoute.Sitemap = [];
  for (const ticker of currencyTickers) {
    currencyPages.push({
      url: `${SITE_URL}/currencies/${ticker}`,
      lastModified: now,
      changeFrequency: 'daily',
      priority: 0.6,
    });
    const friendly = friendlySlugFor(ticker);
    if (friendly !== ticker.toLowerCase()) {
      // Curated friendly slug exists and differs from the bare
      // ISO code → also list it as the canonical share URL.
      currencyPages.push({
        url: `${SITE_URL}/currencies/${friendly}`,
        lastModified: now,
        changeFrequency: 'daily',
        priority: 0.7,
      });
    }
  }

  // Per-pair detail pages. The route is /markets/{base}~{quote},
  // URL-encoded once. We pre-render the top 100 by 24h volume at
  // build time (see markets/[pair]/page.tsx); listing them in the
  // sitemap surfaces the highest-traffic pairs to crawlers without
  // exploding the file (every Stellar pair × 100s of issuers would
  // be tens of thousands of URLs of mostly-empty content).
  const marketPages: MetadataRoute.Sitemap = marketPairs.map((slug) => ({
    url: `${SITE_URL}/markets/${encodeURIComponent(slug)}`,
    lastModified: now,
    changeFrequency: 'daily',
    priority: 0.6,
  }));

  return [
    ...staticPages,
    ...blogPages,
    ...adrPages,
    ...archPages,
    ...discoveryPages,
    ...opsPages,
    ...assetPages,
    ...issuerPages,
    ...currencyPages,
    ...marketPages,
  ];
}

async function fetchMarketPairs(): Promise<string[]> {
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/markets?limit=100&order_by=volume_24h_usd_desc`,
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
  try {
    const res = await fetch(`${API_BASE_URL}/v1/currencies`, {
      signal: AbortSignal.timeout(5_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as {
      data: { currencies?: { ticker: string }[] };
    };
    return (env.data?.currencies ?? []).map((c) => c.ticker);
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
    const res = await fetch(`${API_BASE_URL}/v1/coins?limit=500`, {
      signal: AbortSignal.timeout(5_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: { coins: { slug: string }[] } };
    return (env.data?.coins ?? []).map((c) => c.slug);
  } catch {
    return [];
  }
}
