import type { MetadataRoute } from 'next';

export const dynamic = 'force-static';

/**
 * robots.txt — emits the manifest at build time. The site is
 * fully public, but a few paths have no SEO value and shouldn't
 * eat crawler budget:
 *   /dev/   — design-iteration scaffolding
 *   /embed/ — iframe widget targets, not standalone content
 *   /auth/  — magic-link callback, expires after one click
 *   /account — authenticated dashboard, irrelevant to crawlers
 *   /signin, /signup — auth gateways, not content
 */
export default function robots(): MetadataRoute.Robots {
  return {
    rules: [
      {
        userAgent: '*',
        allow: '/',
        disallow: ['/dev/', '/embed/', '/auth/', '/account', '/signin', '/signup'],
      },
    ],
    sitemap: 'https://ratesengine.net/sitemap.xml',
    host: 'https://ratesengine.net',
  };
}
