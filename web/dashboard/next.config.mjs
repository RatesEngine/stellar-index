/** @type {import('next').NextConfig} */
const nextConfig = {
  reactStrictMode: true,

  // Static-export the dashboard. Deployed to Cloudflare Pages
  // alongside the showcase. Auth is cookie-based: the API at
  // api.ratesengine.net sets a Domain=.ratesengine.net cookie
  // on /v1/auth/callback (see internal/api/v1/dashboardauth) so
  // every page loaded here can call /v1/account/* with
  // credentials: include and the cookie rides along
  // cross-subdomain.
  output: 'export',
  trailingSlash: true,

  images: {
    unoptimized: true,
  },

  productionBrowserSourceMaps: true,

  env: {
    // The API origin the dashboard talks to. Override in CI / preview
    // builds for staging deployments.
    NEXT_PUBLIC_API_BASE_URL:
      process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net',
  },
};

export default nextConfig;
