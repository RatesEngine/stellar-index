// Shared SEO helpers — single source of truth for the social-share
// preview image so every detail page gets the same og:image.
//
// Why this exists: Next.js 15 metadata "merges" nested openGraph
// fields between layout + page, BUT the merge has been observed to
// drop `openGraph.images` from the layout when a page sets its own
// openGraph block (audited 2026-05-09 across /assets, /currencies,
// /issuers, /sources, /exchanges, /dexes, /lending, /convert,
// /research/* — every detail page that overrides openGraph rendered
// without og:image, while pages that don't override inherited the
// layout image fine). Per-page openGraph blocks now spread
// SITE_OG_IMAGES so the social card image is explicit and consistent.
//
// Twitter card images use the same asset.

export const SITE_OG_IMAGE_PATH = '/og.svg';

export const SITE_OG_IMAGES = [
  {
    url: SITE_OG_IMAGE_PATH,
    width: 1200,
    height: 630,
    alt: 'Rates Engine — Stellar pricing explorer',
    type: 'image/svg+xml',
  },
];

// Convenience for `twitter.images`, which is a flat string[]. Same
// asset as openGraph.images, but Twitter expects the URL directly.
export const SITE_TWITTER_IMAGES = [SITE_OG_IMAGE_PATH];
