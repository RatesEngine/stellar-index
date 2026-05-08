import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import { SourceStatsPanel } from '@/app/dexes/[source]/SourceStatsPanel';
import { PairsTable } from './PairsTable';
import { VenueChart } from './VenueChart';

const CEX_INFO: Record<
  string,
  { name: string; type: string; homepage: string; docsUrl: string; blurb: string }
> = {
  binance: {
    name: 'Binance',
    type: 'CEX — REST + WebSocket spot tickers',
    homepage: 'https://www.binance.com',
    docsUrl: 'https://github.com/binance/binance-spot-api-docs',
    blurb:
      'Spot trading pairs against XLM. We poll Binance ticker streams for trade events; usd_volume is computed Phase-1-style from USD-pegged quotes (USDT, BUSD, USDC).',
  },
  coinbase: {
    name: 'Coinbase',
    type: 'CEX — Advanced Trade WebSocket',
    homepage: 'https://www.coinbase.com',
    docsUrl: 'https://docs.cloud.coinbase.com/advanced-trade-api',
    blurb:
      'XLM spot pairs from Coinbase Advanced Trade — direct USD quote for usd_volume populates with no FX leg. The market-data feed dropped 0-quote-amount canonical-validator violations after the fix in PR #49.',
  },
  kraken: {
    name: 'Kraken',
    type: 'CEX — public WebSocket trades',
    homepage: 'https://www.kraken.com',
    docsUrl: 'https://docs.kraken.com/websockets',
    blurb:
      'Kraken spot pairs against USD and EUR. Forex factor (X2.5) snaps EUR pairs into USD-equivalent volume.',
  },
  bitstamp: {
    name: 'Bitstamp',
    type: 'CEX — public WebSocket trades',
    homepage: 'https://www.bitstamp.net',
    docsUrl: 'https://www.bitstamp.net/websocket/v2/',
    blurb:
      'Long-running USD-quoted XLM pairs. Smaller volume share than Binance/Coinbase but contributes to the cross-CEX VWAP weighting.',
  },
};

type Params = Promise<{ name: string }>;

export function generateStaticParams() {
  return Object.keys(CEX_INFO).map((name) => ({ name }));
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { name } = await params;
  const info = CEX_INFO[name];
  if (!info) return { title: 'Exchange not found' };
  const canonical = `https://ratesengine.net/exchanges/${encodeURIComponent(name)}`;
  return {
    title: `${info.name} — every pair, live`,
    description: `All ${info.name} pairs observed in the last 14 days, with per-pair 24h trade count + last trade. Source: /v1/markets?source=${name}.`,
    alternates: { canonical },
  };
}

export default async function ExchangeDetailPage({
  params,
}: {
  params: Params;
}) {
  const { name } = await params;
  const info = CEX_INFO[name];
  if (!info) notFound();

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <Link
        href="/exchanges"
        className="inline-flex items-center gap-1.5 text-sm text-slate-600 hover:text-brand-600 dark:text-slate-400"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        All exchanges
      </Link>

      <header className="space-y-2 border-b border-slate-200 pb-4 dark:border-slate-800">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-3xl font-semibold tracking-tight">{info.name}</h1>
          <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-400">
            {info.type}
          </span>
        </div>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">{info.blurb}</p>
        <p className="max-w-3xl rounded-md border border-amber-200 bg-amber-50 p-3 text-xs text-amber-900 dark:border-amber-900/40 dark:bg-amber-950/40 dark:text-amber-200">
          <span className="font-semibold">Curated subscription, not a full mirror.</span>{' '}
          Rates Engine is a Stellar-network pricing API; from each CEX we
          subscribe to the pairs that triangulate to XLM (the largest XLM
          markets, the BTC/ETH crypto anchors, and ~17 top-cap globals
          for cross-venue VWAP coverage). The full venue order book is
          out of scope — see the source code at{' '}
          <code className="font-mono">internal/sources/external/cex/{name}/</code>.
        </p>
      </header>

      <SourceStatsPanel source={name} unitsLabel="pairs" />

      <VenueChart venue={name} />

      <PairsTable source={name} exchangeName={info.name} />

      <div className="flex flex-wrap gap-3 text-xs">
        <Link
          href={`/sources/${name}`}
          className="inline-flex items-center gap-1 text-slate-500 hover:text-brand-600"
        >
          Source registry detail →
        </Link>
        <a
          href={info.homepage}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex items-center gap-1 text-slate-500 hover:underline"
        >
          {info.name} homepage
          <ExternalLink className="h-3 w-3" />
        </a>
        <a
          href={info.docsUrl}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex items-center gap-1 text-slate-500 hover:underline"
        >
          API docs
          <ExternalLink className="h-3 w-3" />
        </a>
      </div>
    </div>
  );
}

