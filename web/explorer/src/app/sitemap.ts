import type { MetadataRoute } from 'next';

import { API_BASE_URL } from '@/api/client';
import { loadADRs } from '@/lib/adr';
import { loadArchitectureDocs } from '@/lib/architecture';
import { loadDiscoveryDocs } from '@/lib/discovery';

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
    '/network',
    '/research',
    '/methodology',
    '/widgets',
    '/changelog',
    '/compare',
    '/anomalies',
    '/divergences',
    '/mev',
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

  const [assetSlugs, issuerKeys] = await Promise.all([
    fetchCoinSlugs(),
    fetchIssuerKeys(),
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

  return [
    ...staticPages,
    ...adrPages,
    ...archPages,
    ...discoveryPages,
    ...assetPages,
    ...issuerPages,
  ];
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
