import type { Metadata } from 'next';
import './globals.css';

const SITE_URL = 'https://status.ratesengine.net';
const SITE_DESCRIPTION =
  'Real-time status of the Rates Engine API: per-service health, request latency, ingest freshness, active incidents.';

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: 'Rates Engine — system status',
  description: SITE_DESCRIPTION,
  robots: { index: true, follow: true },
  openGraph: {
    type: 'website',
    siteName: 'Rates Engine — status',
    title: 'Rates Engine — system status',
    description: SITE_DESCRIPTION,
    url: SITE_URL,
    locale: 'en_US',
    images: [
      {
        url: '/og.svg',
        width: 1200,
        height: 630,
        alt: 'Rates Engine — system status',
        type: 'image/svg+xml',
      },
    ],
  },
  twitter: {
    card: 'summary_large_image',
    title: 'Rates Engine — system status',
    description: SITE_DESCRIPTION,
    images: ['/og.svg'],
  },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-surface-subtle">{children}</body>
    </html>
  );
}
