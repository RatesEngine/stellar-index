import type { Metadata } from 'next';
import './globals.css';
import { Navbar } from '@/components/nav/Navbar';
import { Footer } from '@/components/nav/Footer';
import { QueryProvider } from '@/components/QueryProvider';

const SITE_URL = 'https://ratesengine.net';
const SITE_NAME = 'Rates Engine';
const SITE_DESCRIPTION =
  'Comprehensive Stellar-network pricing API. Browse every asset, every pair, every protocol — backed by an independent VWAP across on-chain DEXes, classic SDEX, and major exchanges.';

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: {
    default: `${SITE_NAME} — Stellar pricing explorer`,
    template: `%s · ${SITE_NAME}`,
  },
  description: SITE_DESCRIPTION,
  applicationName: SITE_NAME,
  keywords: [
    'Stellar',
    'XLM',
    'pricing',
    'VWAP',
    'TWAP',
    'OHLC',
    'oracle',
    'SDEX',
    'Soroswap',
    'Phoenix',
    'Aquarius',
    'Reflector',
    'Blend',
    'API',
  ],
  openGraph: {
    type: 'website',
    siteName: SITE_NAME,
    title: `${SITE_NAME} — Stellar pricing explorer`,
    description: SITE_DESCRIPTION,
    url: SITE_URL,
    locale: 'en_US',
    images: [
      {
        url: '/og.svg',
        width: 1200,
        height: 630,
        alt: `${SITE_NAME} — Stellar pricing explorer`,
        type: 'image/svg+xml',
      },
    ],
  },
  twitter: {
    card: 'summary_large_image',
    title: `${SITE_NAME} — Stellar pricing explorer`,
    description: SITE_DESCRIPTION,
    images: ['/og.svg'],
  },
  robots: {
    index: true,
    follow: true,
    googleBot: {
      index: true,
      follow: true,
      'max-image-preview': 'large',
      'max-snippet': -1,
    },
  },
  alternates: {
    // Default canonical for the home page. Detail pages override
    // this in their own generateMetadata; without it the root URL
    // would be served without a <link rel="canonical">, leaving
    // search engines free to treat https://ratesengine.net/ vs
    // https://ratesengine.net (no trailing slash) vs
    // https://ratesengine.net/index.html as separate pages.
    canonical: '/',
    types: {
      'application/atom+xml': [
        { url: '/blog.atom', title: 'Rates Engine — engineering notes' },
        { url: '/changelog.atom', title: 'Rates Engine — changelog' },
      ],
    },
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  // Inline theme-init script — sets html.dark BEFORE first paint
  // based on localStorage (`re.theme` ∈ {light,dark,system}) or
  // OS prefers-color-scheme as fallback. Without this users would
  // see a flash of the wrong theme on page load.
  const themeInit = `
(function () {
  try {
    var v = localStorage.getItem('re.theme');
    var prefersDark = window.matchMedia('(prefers-color-scheme: dark)').matches;
    var dark = v === 'dark' || ((v === null || v === 'system') && prefersDark);
    if (dark) document.documentElement.classList.add('dark');
  } catch (_) {}
})();
`.trim();
  return (
    <html lang="en">
      <head>
        {/* Build identifier — same SHA + time as the footer badge,
            in machine-readable form. `curl -s ratesengine.net | grep
            re-build` reveals the live build without rendering JS. */}
        <meta
          name="re-build-sha"
          content={process.env.NEXT_PUBLIC_BUILD_SHA ?? 'dev'}
        />
        <meta
          name="re-build-time"
          content={process.env.NEXT_PUBLIC_BUILD_TIME ?? ''}
        />
        {/* Set html.dark before first paint to avoid theme flash */}
        <script dangerouslySetInnerHTML={{ __html: themeInit }} />
        {/* Schema.org JSON-LD — Organization + WebSite. Lets Google
            render the brand panel and a sitelinks search box at
            ratesengine.net pointing at /assets?q=…. */}
        <script
          type="application/ld+json"
          dangerouslySetInnerHTML={{
            __html: JSON.stringify({
              '@context': 'https://schema.org',
              '@graph': [
                {
                  '@type': 'Organization',
                  '@id': `${SITE_URL}#org`,
                  name: SITE_NAME,
                  url: SITE_URL,
                  logo: `${SITE_URL}/icon.svg`,
                  description: SITE_DESCRIPTION,
                  sameAs: [
                    'https://github.com/RatesEngine/rates-engine',
                  ],
                  contactPoint: [
                    {
                      '@type': 'ContactPoint',
                      contactType: 'security',
                      email: 'security@ratesengine.net',
                    },
                    {
                      '@type': 'ContactPoint',
                      contactType: 'sales',
                      email: 'sales@ratesengine.net',
                    },
                  ],
                },
                {
                  '@type': 'WebSite',
                  '@id': `${SITE_URL}#site`,
                  url: SITE_URL,
                  name: SITE_NAME,
                  description: SITE_DESCRIPTION,
                  publisher: { '@id': `${SITE_URL}#org` },
                  potentialAction: {
                    '@type': 'SearchAction',
                    target: {
                      '@type': 'EntryPoint',
                      urlTemplate: `${SITE_URL}/assets?q={search_term_string}`,
                    },
                    'query-input': 'required name=search_term_string',
                  },
                },
              ],
            }),
          }}
        />
      </head>
      <body className="flex min-h-screen flex-col">
        <QueryProvider>
          <Navbar />
          <main className="flex-1">{children}</main>
          <Footer />
        </QueryProvider>
      </body>
    </html>
  );
}
