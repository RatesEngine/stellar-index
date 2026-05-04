import type { Metadata } from 'next';
import './globals.css';
import { Navbar } from '@/components/nav/Navbar';
import { Footer } from '@/components/nav/Footer';
import { QueryProvider } from '@/components/QueryProvider';

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
