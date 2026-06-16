import type { Metadata } from 'next';
import { Inter, JetBrains_Mono } from 'next/font/google';
import './globals.css';
import { Navbar } from '@/components/nav/Navbar';
import { DegradedBanner } from '@/components/nav/DegradedBanner';
import { Footer } from '@/components/nav/Footer';
import { QueryProvider } from '@/components/QueryProvider';

// Inter (UI) + JetBrains Mono (numeric / addresses / code). next/font
// self-hosts both at build time — no runtime Google dependency, no layout
// shift — and exposes them as the --font-sans / --font-mono CSS variables
// the Tailwind theme reads.
const inter = Inter({
  subsets: ['latin'],
  display: 'swap',
  variable: '--font-sans',
});
const jetbrainsMono = JetBrains_Mono({
  subsets: ['latin'],
  display: 'swap',
  variable: '--font-mono',
});

const SITE_URL = 'https://stellarindex.io';
const SITE_NAME = 'Stellar Index';
const SITE_DESCRIPTION =
  'The protocol explorer for the Stellar network. Every contract, every event, every trade — complete, verified, per-protocol on-chain data, plus an independent VWAP pricing API across on-chain DEXes, classic SDEX, and major exchanges.';

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
    // search engines free to treat https://stellarindex.io/ vs
    // https://stellarindex.io (no trailing slash) vs
    // https://stellarindex.io/index.html as separate pages.
    canonical: '/',
    types: {
      'application/atom+xml': [
        { url: '/blog.atom', title: 'Stellar Index — engineering notes' },
        { url: '/changelog.atom', title: 'Stellar Index — changelog' },
      ],
    },
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en" className={`${inter.variable} ${jetbrainsMono.variable}`}>
      <head>
        {/* Build identifier — same SHA + time as the footer badge,
            in machine-readable form. `curl -s stellarindex.io | grep
            re-build` reveals the live build without rendering JS. */}
        <meta
          name="re-build-sha"
          content={process.env.NEXT_PUBLIC_BUILD_SHA ?? 'dev'}
        />
        <meta
          name="re-build-time"
          content={process.env.NEXT_PUBLIC_BUILD_TIME ?? ''}
        />
        {/* Schema.org JSON-LD — Organization + WebSite. Lets Google
            render the brand panel and a sitelinks search box at
            stellarindex.io pointing at /assets?q=…. */}
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
                    'https://github.com/StellarIndex/stellar-index',
                  ],
                  contactPoint: [
                    {
                      '@type': 'ContactPoint',
                      contactType: 'security',
                      email: 'security@stellarindex.io',
                    },
                    {
                      '@type': 'ContactPoint',
                      contactType: 'sales',
                      email: 'sales@stellarindex.io',
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
        {/* a11y (audit-2026-06-14 Q3): bypass-blocks skip link — keyboard
            users jump past the nav + dropdowns to the page content. Visually
            hidden until focused. */}
        <a
          href="#main"
          className="sr-only focus:not-sr-only focus:absolute focus:left-4 focus:top-4 focus:z-[100] focus:rounded-md focus:bg-white focus:px-3 focus:py-2 focus:text-sm focus:font-medium focus:text-brand-700 focus:shadow-elevated"
        >
          Skip to main content
        </a>
        <QueryProvider>
          <Navbar />
          <DegradedBanner />
          <main id="main" className="flex-1">
            {children}
          </main>
          <Footer />
        </QueryProvider>
      </body>
    </html>
  );
}
