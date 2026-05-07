import type { MetadataRoute } from 'next';

import { loadIncidents } from '@/lib/incidents';

// Required for `output: 'export'` — sitemap is generated at build
// time and emitted as a static file.
export const dynamic = 'force-static';

const SITE_URL = 'https://status.ratesengine.net';

export default function sitemap(): MetadataRoute.Sitemap {
  const now = new Date().toISOString();
  const home: MetadataRoute.Sitemap = [
    {
      url: SITE_URL,
      lastModified: now,
      changeFrequency: 'always',
      priority: 1,
    },
  ];
  const incidents: MetadataRoute.Sitemap = loadIncidents().map((i) => ({
    url: `${SITE_URL}/incident/${i.slug}`,
    lastModified: i.resolved_at ?? i.started_at ?? now,
    changeFrequency: 'never',
    priority: 0.4,
  }));
  return [...home, ...incidents];
}
