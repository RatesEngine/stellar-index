/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,

  // Static-export the entire site. Deployed to Cloudflare Pages
  // (or rsync'd to r1 nginx behind Cloudflare CDN) — same vendor
  // story as api.ratesengine.net per docs/operations/cdn-setup.md.
  // Dynamic routes (/coins/[slug], /contracts/[id], etc.) use
  // client-side rendering: the build emits a shell, the page
  // hydrates and fetches data from the API based on the URL.
  // High-traffic dynamic routes can be pre-rendered at build time
  // via `generateStaticParams` (added in Phase 8).
  output: 'export',

  // Static export needs explicit trailing-slash handling for
  // Cloudflare Pages routing. Trailing slash → directory-style
  // URL → directly maps to filesystem.
  trailingSlash: true,

  // No server image optimization in static-export mode.
  images: {
    unoptimized: true,
  },

  // Default 60s per page is too tight under build load — the
  // explorer pre-renders ~500 asset pages, each doing 4-6 API
  // fetches. When the API rate-limiter kicks in the cumulative
  // wait per page (each 8s timeout, optional retries) can exceed
  // 60s for slugs the build happens to hit late in the queue.
  // 180s gives headroom without papering over a real hang —
  // if a page legitimately stalls forever we still notice.
  // Tracked since 2026-05-11 deploy stuck on /assets/WGUARDIAN-...
  staticPageGenerationTimeout: 180,

  // Sourcemaps in production help when debugging from issue reports.
  productionBrowserSourceMaps: true,

  // All API access is client-side from the browser to api.ratesengine.net,
  // which is already CDN-cached per cdn-setup.md. No server-side fetches
  // needed — that's the entire point of the static-export architecture.
  env: {
    NEXT_PUBLIC_API_BASE_URL:
      process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net',
  },
};

export default nextConfig;
