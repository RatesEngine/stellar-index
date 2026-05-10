'use client';

import { useState, useMemo } from 'react';
import { ArrowLeftRight } from 'lucide-react';
import { useQuery } from '@tanstack/react-query';

import { apiGet } from '@/api/client';

interface CurrencyDetail {
  ticker: string;
  rate_usd: number;
  inverse_usd: number;
  cross_rates: Record<string, number>;
}

/**
 * ConvertPair — interactive client-side converter for the
 * `/convert/[from]/[to]` static page. The server-rendered page
 * already shows the rate and snippet ladder; this component lets
 * the visitor type a custom amount.
 *
 * Refreshes the rate every 60s via /v1/currencies so a tab left
 * open all afternoon doesn't go stale. SSR-rendered initial value
 * comes from `initialRate` so the first paint is correct without a
 * client roundtrip.
 */
export function ConvertPair({
  from,
  to,
  initialRate,
  initialInverse,
}: {
  from: string;
  to: string;
  initialRate: number | null;
  initialInverse: number | null;
}) {
  const [amount, setAmount] = useState('1');
  const [direction, setDirection] = useState<'forward' | 'reverse'>('forward');

  // Live-refresh the rate so the converter doesn't go stale.
  const q = useQuery<CurrencyDetail | null>({
    queryKey: ['/v1/currencies', from, 'for-convert'],
    queryFn: async () => {
      const env = await apiGet<{ data: CurrencyDetail }>(`/v1/currencies/${from}`, {});
      return env.data ?? null;
    },
    refetchInterval: 60_000,
    staleTime: 30_000,
  });

  const liveRate = q.data?.cross_rates?.[to];
  const rate = liveRate != null && Number.isFinite(liveRate) ? liveRate : initialRate;
  const inverse = useMemo(
    () => (rate != null && rate > 0 ? 1 / rate : initialInverse),
    [rate, initialInverse],
  );

  const numeric = Number(amount);
  const result = useMemo(() => {
    if (!Number.isFinite(numeric) || rate == null || inverse == null) return null;
    return direction === 'forward' ? numeric * rate : numeric * inverse;
  }, [numeric, rate, inverse, direction]);

  const fromLabel = direction === 'forward' ? from : to;
  const toLabel = direction === 'forward' ? to : from;

  return (
    <section className="rounded-xl border border-slate-200 bg-white p-5 shadow-sm dark:border-slate-800 dark:bg-slate-900">
      <h2 className="mb-4 text-lg font-semibold tracking-tight">
        Convert {fromLabel} → {toLabel}
      </h2>
      <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_auto_1fr]">
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
              aria-label={`Amount in ${fromLabel}`}
            />
            <span className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs uppercase tracking-wider text-slate-700 dark:bg-slate-800 dark:text-slate-300">
              {fromLabel}
            </span>
          </div>
        </label>

        <button
          type="button"
          onClick={() => setDirection((d) => (d === 'forward' ? 'reverse' : 'forward'))}
          className="self-end rounded-md border border-slate-200 bg-white p-2 text-slate-500 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-400"
          aria-label="Swap direction"
          title="Swap direction"
        >
          <ArrowLeftRight className="h-4 w-4" />
        </button>

        <label className="space-y-1">
          <span className="text-xs uppercase tracking-wider text-slate-500">To</span>
          <div className="flex items-center gap-2 rounded-md border border-slate-200 bg-white p-2 dark:border-slate-700 dark:bg-slate-900">
            <span className="w-full text-2xl font-mono tabular-nums text-slate-900 dark:text-slate-100">
              {result != null ? formatRate(result) : '—'}
            </span>
            <span className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs uppercase tracking-wider text-slate-700 dark:bg-slate-800 dark:text-slate-300">
              {toLabel}
            </span>
          </div>
        </label>
      </div>
      <p className="mt-3 text-xs text-slate-500">
        {rate != null && inverse != null ? (
          <>
            1 {fromLabel} ={' '}
            <span className="font-mono tabular-nums">
              {formatRate(direction === 'forward' ? rate : inverse)}
            </span>{' '}
            {toLabel}
            {q.dataUpdatedAt > 0 && (
              <>
                <span className="mx-1.5">·</span>
                Updated {formatRelativeTime(q.dataUpdatedAt)}
              </>
            )}
          </>
        ) : (
          'Rate currently unavailable.'
        )}
      </p>
    </section>
  );
}

function formatRate(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (Math.abs(n) >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (Math.abs(n) >= 1) return n.toFixed(4);
  if (Math.abs(n) >= 0.01) return n.toFixed(6);
  return n.toFixed(8);
}

function formatRelativeTime(ms: number): string {
  const diff = Date.now() - ms;
  if (diff < 5_000) return 'just now';
  if (diff < 60_000) return `${Math.floor(diff / 1000)}s ago`;
  if (diff < 3600_000) return `${Math.floor(diff / 60_000)}m ago`;
  return new Date(ms).toLocaleTimeString();
}
