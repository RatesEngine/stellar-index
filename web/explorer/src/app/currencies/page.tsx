import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Currencies — fiat forex coverage',
  description:
    'World fiat currencies — OHLC, 24h change vs USD, sorted by reserve circulation. Currency converter, per-currency drill-down with trading information.',
};

export default function CurrenciesPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-6 px-6 py-12">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Currencies</h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          World fiat currencies — table sortable by ticker, 1h / 24h / 7d
          change vs USD (or your chosen base), market cap, 24h volume,
          circulating supply, last 7 days sparkline. Per-currency
          drill-down with candlestick OHLC chart, forex quotes, trade
          information, and a converter widget.
        </p>
      </header>
      <div className="rounded-lg border border-slate-200 bg-slate-50 p-6 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-900/40 dark:text-slate-300">
        <p className="font-medium">Wiring forex source.</p>
        <p className="mt-2">
          Forex coverage isn&apos;t in the live API yet — Rates Engine
          ingests on-chain and CEX trade data today. We&apos;re evaluating
          a forex feed (Massive.com or equivalent licensed source) before
          shipping this page with real numbers.
        </p>
        <p className="mt-3">
          Need fiat conversion right now? Every USD price on{' '}
          <Link href="/assets" className="text-brand-600 hover:underline">
            /assets
          </Link>{' '}
          is computed from on-chain XLM/USDC plus the FX leg, and{' '}
          <Link href="/markets" className="text-brand-600 hover:underline">
            /markets
          </Link>{' '}
          shows the underlying pairs.
        </p>
      </div>
    </div>
  );
}
