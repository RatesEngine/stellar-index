'use client';

import { useState } from 'react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';

/**
 * AssetConverter — bidirectional USD ↔ asset converter.
 * Pure client-side maths off the price prop; refreshes when the
 * parent re-fetches the price.
 *
 * No FX leg yet — converts to USD only. Cross-currency conversion
 * (asset → EUR, JPY, etc.) lands when the asset detail page wires
 * the forex snapshot from /v1/currencies.
 */
export function AssetConverter({
  symbol,
  priceUSD,
}: {
  symbol: string;
  priceUSD: number | null;
}) {
  const [direction, setDirection] = useState<'usd-to-asset' | 'asset-to-usd'>(
    'usd-to-asset',
  );
  const [amount, setAmount] = useState('1');

  const numeric = Number(amount);
  const validInput = Number.isFinite(numeric) && numeric >= 0;

  let result: number | null = null;
  if (priceUSD != null && priceUSD > 0 && validInput) {
    result = direction === 'usd-to-asset' ? numeric / priceUSD : numeric * priceUSD;
  }

  const fromUnit = direction === 'usd-to-asset' ? 'USD' : symbol;
  const toUnit = direction === 'usd-to-asset' ? symbol : 'USD';

  return (
    <Panel
      title="Converter"
      hint={priceUSD != null ? `Live ${symbol}/USD price` : 'Awaiting live price'}
      source={asExample('/v1/price', { asset: symbol, quote: 'fiat:USD' })}
    >
      <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-[1fr_auto_1fr]">
        <label className="space-y-1">
          <span className="text-xs uppercase tracking-wider text-slate-500">From</span>
          <div className="flex items-center gap-2 rounded-md border border-slate-200 bg-white p-2 dark:border-slate-700 dark:bg-slate-900">
            <input
              type="number"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              min="0"
              step="any"
              inputMode="decimal"
              className="w-full bg-transparent text-2xl font-mono tabular-nums focus:outline-none"
            />
            <span className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs uppercase tracking-wider text-slate-700 dark:bg-slate-800 dark:text-slate-300">
              {fromUnit}
            </span>
          </div>
        </label>

        <button
          type="button"
          aria-label="Swap direction"
          onClick={() =>
            setDirection((d) => (d === 'usd-to-asset' ? 'asset-to-usd' : 'usd-to-asset'))
          }
          className="self-center rounded-md border border-slate-200 px-2 py-1 text-xs text-slate-500 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:text-slate-400 sm:mb-1"
        >
          ⇄
        </button>

        <label className="space-y-1">
          <span className="text-xs uppercase tracking-wider text-slate-500">To</span>
          <div className="flex items-center gap-2 rounded-md border border-slate-200 bg-white p-2 dark:border-slate-700 dark:bg-slate-900">
            <span className="w-full text-2xl font-mono tabular-nums text-slate-900 dark:text-slate-100">
              {result != null ? formatResult(result) : '—'}
            </span>
            <span className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs uppercase tracking-wider text-slate-700 dark:bg-slate-800 dark:text-slate-300">
              {toUnit}
            </span>
          </div>
        </label>
      </div>
      {priceUSD != null && priceUSD > 0 && (
        <p className="mt-3 text-xs text-slate-500">
          1 {symbol} = ${formatResult(priceUSD)} · 1 USD ={' '}
          {formatResult(1 / priceUSD)} {symbol}
        </p>
      )}
    </Panel>
  );
}

function formatResult(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (n === 0) return '0';
  if (n >= 1_000_000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (n >= 1) return n.toFixed(4);
  if (n >= 0.0001) return n.toFixed(6);
  return n.toExponential(3);
}
