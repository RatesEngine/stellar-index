import type { Metadata } from 'next';
import './globals.css';

export const metadata: Metadata = {
  title: {
    default: 'Rates Engine — Stellar pricing explorer',
    template: '%s · Rates Engine',
  },
  description:
    'Comprehensive Stellar-network pricing API. Browse every asset, every pair, every protocol — backed by an independent VWAP across on-chain DEXes, classic SDEX, and major exchanges.',
};

export default function RootLayout({
  children,
}: {
  children: React.ReactNode;
}) {
  return (
    <html lang="en">
      <body className="min-h-screen flex flex-col">{children}</body>
    </html>
  );
}
