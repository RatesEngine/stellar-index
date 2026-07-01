'use client';

import { useEffect, useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { CurrencyCombobox } from '@/components/CurrencyCombobox';
import { apiGet, asExample } from '@/api/client';

interface CurrencyRow {
  ticker: string;
  name: string;
  rate_usd: number;
}

// FEATURED — kept short so the dropdown isn't overwhelming. Users
// can switch to "All currencies" to see every ticker the forex
// snapshot returns.
const FEATURED = ['USD', 'EUR', 'GBP', 'JPY', 'CHF', 'CAD', 'AUD', 'CNY', 'INR', 'BRL', 'MXN'];

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
  const [direction, setDirection] = useState<'fiat-to-asset' | 'asset-to-fiat'>(
    'fiat-to-asset',
  );
  const [amount, setAmount] = useState('1');
  const [target, setTarget] = useState('USD');
  const [showAll, setShowAll] = useState(false);

  // Pull the forex snapshot to power non-USD targets. Stale data is
  // fine — the snapshot refreshes every 5 min, and a stale FX leg
  // on a crypto-asset converter is dominated by the crypto's own
  // volatility anyway.
  //
  // F-1201 migration: pre-rc.48 this called /v1/currencies (single
  // bulk endpoint with `rate_usd` already in the "1 USD = N target"
  // shape this converter expects). rc.48 removed that route; we
  // now use /v1/price/batch which returns "1 unit of asset = X
  // USD" and invert at the boundary. Names are missing from the
  // batch response — we don't render them in this converter so
  // that's fine.
  const fx = useQuery<CurrencyRow[]>({
    queryKey: ['/v1/price/batch', 'forAssetConverter'],
    queryFn: async () => {
      const assetIds = FEATURED.filter((t) => t !== 'USD')
        .map((t) => `fiat:${t}`)
        .join(',');
      const env = await apiGet<{
        data: Array<{ asset_id: string; price: string | null }>;
      }>(`/v1/price/batch?asset_ids=${encodeURIComponent(assetIds)}&quote=fiat:USD`, {});
      const rows: CurrencyRow[] = [];
      for (const row of env.data ?? []) {
        const ticker = row.asset_id.replace(/^fiat:/, '');
        const priceUSD = row.price ? Number(row.price) : 0;
        if (!(priceUSD > 0)) continue;
        // /v1/price/batch returns 1 ticker = X USD; the converter
        // expects rate_usd in the "1 USD = N target" form. Invert.
        rows.push({ ticker, name: ticker, rate_usd: 1 / priceUSD });
      }
      return rows;
    },
    refetchInterval: 5 * 60_000,
  });

  const fxByTicker = useMemo(() => {
    const m: Record<string, number> = { USD: 1 };
    for (const c of fx.data ?? []) m[c.ticker] = c.rate_usd;
    return m;
  }, [fx.data]);

  // rate_usd here means "1 USD = N target" so 1 asset = N * priceUSD target.
  const targetRate = fxByTicker[target] ?? null;
  const numeric = Number(amount);
  const validInput = Number.isFinite(numeric) && numeric >= 0;

  let result: number | null = null;
  if (priceUSD != null && priceUSD > 0 && targetRate != null && validInput) {
    if (direction === 'fiat-to-asset') {
      // amount target → asset: convert target → USD (÷ targetRate),
      // then USD → asset (÷ priceUSD).
      result = numeric / targetRate / priceUSD;
    } else {
      // asset → target: convert asset → USD (× priceUSD), then
      // USD → target (× targetRate).
      result = numeric * priceUSD * targetRate;
    }
  }

  const fromUnit = direction === 'fiat-to-asset' ? target : symbol;
  const toUnit = direction === 'fiat-to-asset' ? symbol : target;

  // Available targets — featured first, then the rest if "show all"
  // is toggled. Filter against the forex snapshot so we don't list
  // tickers we can't actually convert.
  // Memoise the ticker set so its identity is stable across renders
  // (otherwise the useEffect dependency tracker reruns on every render).
  const tickerSet = useMemo(() => {
    const s = new Set((fx.data ?? []).map((c) => c.ticker));
    s.add('USD');
    return s;
  }, [fx.data]);

  const allTickers = useMemo(() => Array.from(tickerSet).sort(), [tickerSet]);

  // Once any currency outside the FEATURED set is picked
  // (e.g. via the searchable combobox typing "ZAR"), promote to
  // showAll mode so the combobox keeps the long-tail list visible.
  useEffect(() => {
    if (!showAll && tickerSet.has(target) && !FEATURED.includes(target)) {
      setShowAll(true);
    }
  }, [target, showAll, tickerSet]);

  return (
    <Panel
      title="Converter"
      hint={priceUSD != null ? `Live ${symbol}/USD price + forex snapshot` : 'Awaiting live price'}
      source={asExample('/v1/price', { asset: symbol, quote: 'fiat:USD' })}
    >
      <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-[1fr_auto_1fr]">
        <label className="space-y-1">
          <span className="text-xs uppercase tracking-wider text-ink-muted">From</span>
          <div className="flex items-center gap-2 rounded-md border border-line bg-surface p-2">
            <input
              type="number"
              value={amount}
              onChange={(e) => setAmount(e.target.value)}
              min="0"
              step="any"
              inputMode="decimal"
              className="w-full bg-transparent text-2xl font-mono tabular-nums focus:outline-hidden"
            />
            {direction === 'fiat-to-asset' ? (
              <CurrencyCombobox
                value={target}
                onChange={(v) => {
                  setTarget(v);
                  setShowAll(true);
                }}
                tickers={allTickers}
              />
            ) : (
              <span className="rounded-sm bg-surface-subtle px-1.5 py-0.5 font-mono text-xs uppercase tracking-wider text-ink-body">
                {fromUnit}
              </span>
            )}
          </div>
        </label>

        <button
          type="button"
          aria-label="Swap direction"
          onClick={() =>
            setDirection((d) => (d === 'fiat-to-asset' ? 'asset-to-fiat' : 'fiat-to-asset'))
          }
          className="self-center rounded-md border border-line px-2 py-1 text-xs text-ink-muted hover:border-brand-500 hover:text-brand-600 sm:mb-1"
        >
          ⇄
        </button>

        <label className="space-y-1">
          <span className="text-xs uppercase tracking-wider text-ink-muted">To</span>
          <div className="flex items-center gap-2 rounded-md border border-line bg-surface p-2">
            <span className="w-full text-2xl font-mono tabular-nums text-ink">
              {result != null ? formatResult(result) : '—'}
            </span>
            {direction === 'asset-to-fiat' ? (
              <CurrencyCombobox
                value={target}
                onChange={(v) => {
                  setTarget(v);
                  setShowAll(true);
                }}
                tickers={allTickers}
              />
            ) : (
              <span className="rounded-sm bg-surface-subtle px-1.5 py-0.5 font-mono text-xs uppercase tracking-wider text-ink-body">
                {toUnit}
              </span>
            )}
          </div>
        </label>
      </div>
      {priceUSD != null && priceUSD > 0 && targetRate != null && targetRate > 0 && (
        <p className="mt-3 text-xs text-ink-muted">
          1 {symbol} = {formatResult(priceUSD * targetRate)} {target} · 1 {target} ={' '}
          {formatResult(1 / (priceUSD * targetRate))} {symbol}
          {target !== 'USD' && (
            <>
              <span className="mx-2 text-ink-faint">·</span>
              <span>FX leg: 1 USD = {formatResult(targetRate)} {target}</span>
            </>
          )}
        </p>
      )}
    </Panel>
  );
}

// (CurrencySelect replaced by the searchable @/components/CurrencyCombobox.)

function formatResult(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (n === 0) return '0';
  if (n >= 1_000_000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (n >= 1) return n.toFixed(4);
  if (n >= 0.0001) return n.toFixed(6);
  return n.toExponential(3);
}
