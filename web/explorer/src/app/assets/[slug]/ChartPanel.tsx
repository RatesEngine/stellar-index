'use client';

import { useState } from 'react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { MarketChart } from '@/components/charts/MarketChart';

type Quote = 'native' | 'fiat:USD';
const QUOTES: { key: Quote; label: string }[] = [
  { key: 'native', label: 'XLM' },
  { key: 'fiat:USD', label: 'USD' },
];

// shortLabel renders a compact base label for the chart caption.
function shortLabel(assetID: string): string {
  if (assetID === 'native') return 'XLM';
  if (assetID.startsWith('fiat:')) return assetID.slice(5);
  const dash = assetID.indexOf('-');
  if (dash > 0) return assetID.slice(0, dash);
  if (assetID.length > 10) return `${assetID.slice(0, 4)}…${assetID.slice(-4)}`;
  return assetID;
}

/**
 * Chart tab for /assets/[slug]?tab=chart — real OHLC candles + volume
 * (the shared MarketChart over /v1/ohlc). The quote toggle picks the
 * counter-asset: most classic Stellar assets trade vs XLM on SDEX,
 * while off-chain crypto:* feeds (Binance / Bitstamp / …) have direct
 * USD pairs. Native XLM + fiat assets only make sense vs USD, so the
 * XLM option is dropped for those.
 */
export function ChartPanel({ assetID }: { assetID: string }) {
  const isNative = assetID === 'native';
  const isFiat = assetID.startsWith('fiat:');
  const quoteOptions = isNative || isFiat ? QUOTES.filter((q) => q.key !== 'native') : QUOTES;
  const [quote, setQuote] = useState<Quote>(isNative || isFiat ? 'fiat:USD' : 'native');

  return (
    <Panel
      title="Price chart"
      hint="OHLC + volume"
      source={asExample('/v1/ohlc', { base: assetID, quote, interval: '15m', limit: 672 })}
      bodyClassName="space-y-3"
    >
      {quoteOptions.length > 1 && (
        <div className="flex items-center gap-1">
          <span className="text-[11px] uppercase tracking-wider text-ink-muted">Quote</span>
          <div className="inline-flex overflow-hidden rounded-md border border-line">
            {quoteOptions.map((opt) => (
              <button
                key={opt.key}
                type="button"
                onClick={() => setQuote(opt.key)}
                aria-pressed={opt.key === quote}
                className={`px-2 py-1 text-xs ${
                  opt.key === quote
                    ? 'bg-brand-600 text-white'
                    : 'bg-surface text-ink-body hover:bg-surface-muted'
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>
        </div>
      )}
      <MarketChart
        base={assetID}
        quote={quote}
        baseLabel={shortLabel(assetID)}
        quoteLabel={quote === 'native' ? 'XLM' : 'USD'}
        height={420}
      />
    </Panel>
  );
}
