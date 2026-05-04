import type { MetadataRoute } from 'next';

export const dynamic = 'force-static';

/**
 * robots.txt — emits the manifest at build time. The site is
 * fully public so we allow everything; the only carve-out is
 * the dev/ path which exists for design-iteration only and
 * shouldn't be indexed.
 */
export default function robots(): MetadataRoute.Robots {
  return {
    rules: [
      {
        userAgent: '*',
        allow: '/',
        disallow: ['/dev/'],
      },
    ],
    sitemap: 'https://ratesengine.net/sitemap.xml',
  };
}
