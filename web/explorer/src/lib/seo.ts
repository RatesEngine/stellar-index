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

// PNG, not SVG: Twitter/X, Facebook, LinkedIn, Slack, iMessage all reject
// SVG og:images (no raster = no link-preview thumbnail). Keep it 1200×630.
export const SITE_OG_IMAGE_PATH = '/og.png';

export const SITE_OG_IMAGES = [
  {
    url: SITE_OG_IMAGE_PATH,
    width: 1200,
    height: 630,
    alt: 'Stellar Index — Stellar pricing explorer',
    type: 'image/png',
  },
];

// Convenience for `twitter.images`, which is a flat string[]. Same
// asset as openGraph.images, but Twitter expects the URL directly.
export const SITE_TWITTER_IMAGES = [SITE_OG_IMAGE_PATH];

// serializeJsonLd stringifies a schema.org object for injection into a
// `<script type="application/ld+json" dangerouslySetInnerHTML>` block —
// SAFELY. Plain `JSON.stringify` escapes `"` (so a value can't break out
// of its JSON string) but NOT `<` / `>` / `&`, so any data-derived value
// containing the literal sequence `</script>` would terminate the script
// element early and let the rest render as live markup. Several JSON-LD
// blocks embed attacker-influenced, build-time-fetched strings — e.g. an
// issuer's SEP-1 `ORG_NAME` (from their own stellar.toml) on
// /issuers/[g] — so a hostile issuer could otherwise bake a stored-XSS
// payload into the statically-rendered page. We escape the HTML-sensitive
// characters as JSON `\uXXXX` escapes: still valid JSON (the browser's
// JSON-LD parser decodes them back), but inert to the HTML tokenizer.
// U+2028 / U+2029 (JS line terminators) are escaped too — harmless for
// JSON-LD, but it keeps the output safe in a plain `<script>` context too.
export function serializeJsonLd(data: unknown): string {
  // U+2028 / U+2029 are spelled via fromCharCode so this source stays
  // pure-ASCII (the raw separators are invisible and easy to mangle).
  const lineSep = String.fromCharCode(0x2028);
  const paraSep = String.fromCharCode(0x2029);
  return JSON.stringify(data)
    .replace(/</g, '\\u003c')
    .replace(/>/g, '\\u003e')
    .replace(/&/g, '\\u0026')
    .split(lineSep)
    .join('\\u2028')
    .split(paraSep)
    .join('\\u2029');
}
