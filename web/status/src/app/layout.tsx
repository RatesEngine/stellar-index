import type { Metadata } from 'next';
import './globals.css';

// The status page MOVED onto the main site at https://stellarindex.io/status
// (one site, unified nav). This project (Cloudflare Pages
// `stellarindex-status`, still bound to status.stellarindex.io) is now
// REDIRECT-ONLY: public/_redirects 301s every path to the new location.
// This minimal layout + page is only the build artifact the redirect
// rides on; it's shadowed by the edge redirect and rarely rendered.
export const metadata: Metadata = {
  title: 'Status moved — stellarindex.io/status',
  description: 'The Stellar Index status page now lives at stellarindex.io/status.',
  robots: { index: false, follow: true },
  alternates: { canonical: 'https://stellarindex.io/status' },
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="min-h-screen bg-surface-canvas">{children}</body>
    </html>
  );
}
