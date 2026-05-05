import type { MetadataRoute } from 'next';

import { API_BASE_URL } from '@/api/client';
import { SEED_COINS } from '@/lib/coins-seed';

// Required for `output: 'export'` — sitemap is generated at build
// time and emitted as a static file. Same applies to robots.ts.
export const dynamic = 'force-static';

const SITE_URL = 'https://ratesengine.net';

/**
 * sitemap.xml — generated at build time. Static pages are
 * enumerated explicitly; dynamic `/coins/[slug]` pages mirror
 * the `generateStaticParams` strategy: live API top-100 unioned
 * with the design seed, falling back to seed-only if the API
 * isn't reachable from the build environment.
 */
export default async function sitemap(): Promise<MetadataRoute.Sitemap> {
  const now = new Date().toISOString();

  const staticPages: MetadataRoute.Sitemap = [
    '',
    '/coins',
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
    '/anomalies',
    '/divergences',
    '/mev',
    '/docs',
    '/signup',
    '/status',
  ].map((path) => ({
    url: `${SITE_URL}${path}`,
    lastModified: now,
    changeFrequency: path === '' ? 'daily' : 'weekly',
    priority: path === '' ? 1 : 0.7,
  }));

  // Dynamic /coins/[slug] entries — fetch the same top-100 we
  // pre-render at build time.
  const coinSlugs = await fetchCoinSlugs();
  const coinPages: MetadataRoute.Sitemap = coinSlugs.map((slug) => ({
    url: `${SITE_URL}/coins/${slug}`,
    lastModified: now,
    changeFrequency: 'daily',
    priority: 0.6,
  }));

  return [...staticPages, ...coinPages];
}

async function fetchCoinSlugs(): Promise<string[]> {
  const seed = SEED_COINS.map((c) => c.slug);
  try {
    const res = await fetch(`${API_BASE_URL}/v1/coins?limit=100`, {
      signal: AbortSignal.timeout(5_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: { slug: string }[] };
    const live = (env.data ?? []).map((c) => c.slug);
    return [...new Set([...seed, ...live])];
  } catch {
    return seed;
  }
}
