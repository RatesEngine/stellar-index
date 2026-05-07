import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Exchanges — connected CEXes',
  description:
    'Centralised exchanges feeding Rates Engine — 24h volume, order-book depth, pair coverage, 7d volume chart. Per-exchange drill-down with full pair list.',
};

export default function ExchangesPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-6 px-6 py-12">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Exchanges</h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Connected CEXes — Binance, Coinbase, Kraken, Bitstamp.
          Per-venue 24h volume, order-book depth (where we have it),
          number of pairs, 7d volume chart.
        </p>
      </header>
      <div className="rounded-lg border border-slate-200 bg-slate-50 p-6 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-900/40 dark:text-slate-300">
        <p className="font-medium">Building the per-exchange aggregations.</p>
        <p className="mt-2">
          The /v1/sources endpoint already returns each venue&apos;s
          stats; we&apos;re wrapping it in the Exchanges-specific table
          shape (with a separate /exchanges/[name] detail page). Until
          that ships, every per-source breakdown lives at{' '}
          <Link href="/sources" className="text-brand-600 hover:underline">
            /sources
          </Link>
          .
        </p>
      </div>
    </div>
  );
}
