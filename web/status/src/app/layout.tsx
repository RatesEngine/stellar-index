import type { Metadata } from 'next';
import { Inter, JetBrains_Mono } from 'next/font/google';
import './globals.css';
import { ConsoleShell } from '@/components/ConsoleShell';

// Inter (UI) + JetBrains Mono (numeric / addresses / code) — loaded exactly
// like the explorer so the status page renders in the same type system.
// next/font self-hosts both at build time (no runtime Google dependency, no
// layout shift) and exposes them as the --font-sans / --font-mono CSS
// variables the Tailwind theme reads.
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

const SITE_URL = 'https://status.stellarindex.io';
const SITE_DESCRIPTION =
  'Real-time status of the Stellar Index API: per-service health, request latency, ingest freshness, active incidents.';

export const metadata: Metadata = {
  metadataBase: new URL(SITE_URL),
  title: 'Stellar Index — system status',
  description: SITE_DESCRIPTION,
  robots: { index: true, follow: true },
  openGraph: {
    type: 'website',
    siteName: 'Stellar Index — status',
    title: 'Stellar Index — system status',
    description: SITE_DESCRIPTION,
    url: SITE_URL,
    locale: 'en_US',
    images: [
      {
        url: '/og.svg',
        width: 1200,
        height: 630,
        alt: 'Stellar Index — system status',
        type: 'image/svg+xml',
      },
    ],
  },
  twitter: {
    card: 'summary_large_image',
    title: 'Stellar Index — system status',
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
    <html lang="en" className={`${inter.variable} ${jetbrainsMono.variable}`}>
      <body className="min-h-screen bg-surface-canvas">
        <ConsoleShell>{children}</ConsoleShell>
      </body>
    </html>
  );
}
