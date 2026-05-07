'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import Link from 'next/link';
import { ArrowLeft } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';

interface HistoryPoint {
  date: string;
  rate_usd: number;
  inverse_usd: number;
}

interface CurrencyDetail {
  ticker: string;
  name: string;
  rate_usd: number;
  inverse_usd: number;
  cross_rates: Record<string, number>;
  change_24h_pct?: number | null;
  change_7d_pct?: number | null;
  history_7d?: HistoryPoint[];
  published_at?: string;
  fetched_at?: string;
  source?: string;
}

const FEATURED_TARGETS = ['USD', 'EUR', 'GBP', 'JPY', 'CHF', 'CAD', 'AUD', 'CNY', 'INR', 'BRL', 'MXN', 'ZAR'];

export function CurrencyDetailView({ ticker }: { ticker: string }) {
  const upper = ticker.toUpperCase();

  const q = useQuery<CurrencyDetail>({
    queryKey: ['/v1/currencies', upper],
    queryFn: async () => {
      const env = await apiGet<{ data: CurrencyDetail }>(`/v1/currencies/${upper}`, {});
      return env.data;
    },
    refetchInterval: 60_000,
  });

  const detail = q.data;

  return (
    <div className="mx-auto max-w-6xl space-y-6 px-6 py-8">
      <Link
        href="/currencies"
        className="inline-flex items-center gap-1.5 text-sm text-slate-600 hover:text-brand-600 dark:text-slate-400"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        All currencies
      </Link>

      <header className="space-y-2 border-b border-slate-200 pb-4 dark:border-slate-800">
        <div className="flex flex-wrap items-baseline gap-3">
          <h1 className="text-3xl font-semibold tracking-tight">{upper}</h1>
          <span className="text-lg text-slate-600 dark:text-slate-400">
            {detail?.name ?? (q.isLoading ? 'Loading…' : '')}
          </span>
        </div>
        {detail && (
          <p className="text-sm text-slate-600 dark:text-slate-400">
            <span className="font-mono tabular-nums">1 USD = {formatRate(detail.rate_usd)} {upper}</span>
            <span className="mx-2 text-slate-400">·</span>
            <span className="font-mono tabular-nums">1 {upper} = ${formatRate(detail.inverse_usd)}</span>
            {(detail.change_24h_pct != null || detail.change_7d_pct != null) && (
              <>
                <span className="mx-2 text-slate-400">·</span>
                <ChangeChip label="24h" value={detail.change_24h_pct} />
                <span className="mx-1.5 text-slate-400">·</span>
                <ChangeChip label="7d" value={detail.change_7d_pct} />
              </>
            )}
            {detail.published_at && (
              <>
                <span className="mx-2 text-slate-400">·</span>
                <span>Source published {formatDate(detail.published_at)}</span>
              </>
            )}
          </p>
        )}
      </header>

      {q.isError && (
        <div className="rounded-md border border-red-200 bg-red-50 p-4 text-sm text-red-800 dark:border-red-900/40 dark:bg-red-950/40 dark:text-red-200">
          Couldn&apos;t load {upper}: {q.error instanceof Error ? q.error.message : 'unknown error'}
        </div>
      )}

      {detail && (
        <>
          <HistoryPanel detail={detail} />
          <Converter detail={detail} />
          <CrossRatesTable detail={detail} />
        </>
      )}
    </div>
  );
}

function HistoryPanel({ detail }: { detail: CurrencyDetail }) {
  const series = detail.history_7d ?? [];
  if (series.length < 2) return null;

  // Compute change vs first point + percent.
  const first = series[0].inverse_usd;
  const last = series[series.length - 1].inverse_usd;
  const changePct = first > 0 ? ((last - first) / first) * 100 : 0;
  const positive = changePct >= 0;

  return (
    <Panel
      title="7-day USD value"
      hint={`1 ${detail.ticker} expressed in USD over the last week`}
      source={asExample(`/v1/currencies/${detail.ticker}`, {})}
    >
      <div className="flex flex-wrap items-end justify-between gap-2">
        <div>
          <div className="text-xs uppercase tracking-wider text-slate-500">
            7d change
          </div>
          <div
            className={`mt-1 text-2xl font-mono tabular-nums ${
              positive ? 'text-emerald-700 dark:text-emerald-400' : 'text-rose-700 dark:text-rose-400'
            }`}
          >
            {positive ? '+' : ''}
            {changePct.toFixed(2)}%
          </div>
        </div>
        <Sparkline
          points={series.map((p) => p.inverse_usd)}
          dates={series.map((p) => p.date)}
          positive={positive}
        />
      </div>
    </Panel>
  );
}

function Sparkline({
  points,
  positive,
  dates,
}: {
  points: number[];
  positive: boolean;
  dates?: string[];
}) {
  const w = 320;
  const h = 96;
  const padX = 28; // leave room for the right-side y-axis labels
  const padY = 6;
  const innerW = w - padX;
  const innerH = h - padY * 2;
  const min = Math.min(...points);
  const max = Math.max(...points);
  const range = max - min || 1;
  const stepX = innerW / (points.length - 1);
  const xy = points.map((p, i) => {
    const x = i * stepX;
    const y = padY + innerH - ((p - min) / range) * innerH;
    return { x, y, p };
  });
  const path = xy
    .map((pt, i) => `${i === 0 ? 'M' : 'L'}${pt.x.toFixed(2)},${pt.y.toFixed(2)}`)
    .join(' ');
  // Area fill: same path closed back to the baseline so the SVG
  // renders a tinted region under the line.
  const area =
    `${path} L${xy[xy.length - 1].x.toFixed(2)},${(padY + innerH).toFixed(2)} ` +
    `L${xy[0].x.toFixed(2)},${(padY + innerH).toFixed(2)} Z`;
  const stroke = positive ? '#059669' : '#e11d48';
  const fill = positive ? 'rgba(16,185,129,0.12)' : 'rgba(244,63,94,0.12)';
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className="overflow-visible">
      {/* min / max labels at the right edge — horizontal so the chart
          reads naturally. */}
      <text x={innerW + 4} y={padY + 4} className="fill-slate-500 text-[9px]" fontFamily="ui-monospace,monospace">
        {formatRate(max)}
      </text>
      <text x={innerW + 4} y={padY + innerH} className="fill-slate-500 text-[9px]" fontFamily="ui-monospace,monospace">
        {formatRate(min)}
      </text>
      <path d={area} fill={fill} stroke="none" />
      <path d={path} fill="none" stroke={stroke} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
      {/* Data points with hover titles for context — native SVG <title>
          renders as the OS tooltip when the user pauses over a dot. */}
      {xy.map((pt, i) => (
        <circle
          key={i}
          cx={pt.x}
          cy={pt.y}
          r={2.5}
          fill={stroke}
          stroke="white"
          strokeWidth={0.75}
        >
          <title>
            {dates?.[i] ? `${dates[i].slice(0, 10)} · ` : ''}
            {formatRate(pt.p)}
          </title>
        </circle>
      ))}
    </svg>
  );
}

function Converter({ detail }: { detail: CurrencyDetail }) {
  const [amount, setAmount] = useState('1');
  const [target, setTarget] = useState('USD');

  const numeric = Number(amount);
  const rate = target === detail.ticker ? 1 : target === 'USD' ? detail.inverse_usd : detail.cross_rates[target];

  const result = Number.isFinite(numeric) && Number.isFinite(rate) ? numeric * (rate ?? 0) : null;

  const allTargets = useMemo(() => {
    const keys = Object.keys(detail.cross_rates).filter((k) => k !== detail.ticker);
    if (!keys.includes('USD') && detail.ticker !== 'USD') keys.unshift('USD');
    return keys.sort();
  }, [detail]);

  return (
    <Panel
      title="Converter"
      hint={`Rates derived from ${detail.source ?? 'currency-api'}`}
      source={asExample(`/v1/currencies/${detail.ticker}`, {})}
    >
      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
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
              {detail.ticker}
            </span>
          </div>
        </label>
        <label className="space-y-1">
          <span className="text-xs uppercase tracking-wider text-slate-500">To</span>
          <div className="flex items-center gap-2 rounded-md border border-slate-200 bg-white p-2 dark:border-slate-700 dark:bg-slate-900">
            <span className="w-full text-2xl font-mono tabular-nums text-slate-900 dark:text-slate-100">
              {result != null ? formatRate(result) : '—'}
            </span>
            <CurrencyCombobox
              tickers={allTargets}
              value={target}
              onChange={setTarget}
            />
          </div>
        </label>
      </div>
      <p className="mt-3 text-xs text-slate-500">
        1 {detail.ticker} = {rate != null ? formatRate(rate) : '—'} {target}
      </p>
    </Panel>
  );
}

function CrossRatesTable({ detail }: { detail: CurrencyDetail }) {
  const [showAll, setShowAll] = useState(false);
  const featured = FEATURED_TARGETS.filter((t) => t !== detail.ticker && detail.cross_rates[t] != null);
  const visible = showAll
    ? Object.keys(detail.cross_rates).sort()
    : featured;

  return (
    <Panel
      title="Cross-rates"
      hint={`1 ${detail.ticker} expressed in other currencies`}
      source={asExample(`/v1/currencies/${detail.ticker}`, {})}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
              <Th>Currency</Th>
              <Th align="right">1 {detail.ticker} =</Th>
              <Th align="right">1 unit = {detail.ticker}</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {visible.map((t) => {
              const rate = detail.cross_rates[t];
              return (
                <tr key={t} className="hover:bg-slate-50 dark:hover:bg-slate-900/40">
                  <Td>
                    <Link
                      href={`/currencies/${t}`}
                      className="font-mono font-medium text-slate-900 hover:text-brand-600 dark:text-slate-100"
                    >
                      {t}
                    </Link>
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                      {formatRate(rate)} {t}
                    </span>
                  </Td>
                  <Td align="right">
                    <span className="font-mono tabular-nums text-slate-500">
                      {rate > 0 ? `${formatRate(1 / rate)} ${detail.ticker}` : '—'}
                    </span>
                  </Td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {!showAll && featured.length < Object.keys(detail.cross_rates).length && (
        <div className="border-t border-slate-200 px-4 py-2 text-center dark:border-slate-800">
          <button
            type="button"
            onClick={() => setShowAll(true)}
            className="text-xs text-brand-600 hover:underline"
          >
            Show all {Object.keys(detail.cross_rates).length} cross-rates →
          </button>
        </div>
      )}
    </Panel>
  );
}

function Th({ children, align }: { children: React.ReactNode; align?: 'left' | 'right' }) {
  return (
    <th scope="col" className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}>
      {children}
    </th>
  );
}

function Td({ children, align }: { children: React.ReactNode; align?: 'left' | 'right' }) {
  return (
    <td className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}>{children}</td>
  );
}

function ChangeChip({ label, value }: { label: string; value?: number | null }) {
  if (value == null || !Number.isFinite(value)) {
    return (
      <span className="font-mono text-xs text-slate-400">
        {label} —
      </span>
    );
  }
  const tone =
    value > 0
      ? 'text-emerald-600 dark:text-emerald-400'
      : value < 0
        ? 'text-rose-600 dark:text-rose-400'
        : 'text-slate-500';
  const sign = value > 0 ? '+' : '';
  return (
    <span className={`font-mono text-xs tabular-nums ${tone}`}>
      {label} {sign}
      {value.toFixed(2)}%
    </span>
  );
}

function formatRate(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  if (n >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (n >= 1) return n.toFixed(4);
  if (n >= 0.0001) return n.toFixed(6);
  return n.toExponential(3);
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    });
  } catch {
    return iso;
  }
}

// CurrencyCombobox — searchable picker over the full ticker list.
// Replaces a plain <select> for converters where the user has 100+
// currencies to pick from. Keyboard-friendly: arrow keys navigate
// the filtered list, Enter selects, Escape closes. Click-outside
// also closes. No external dependencies.
function CurrencyCombobox({
  tickers,
  value,
  onChange,
}: {
  tickers: string[];
  value: string;
  onChange: (v: string) => void;
}) {
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState('');
  const [highlight, setHighlight] = useState(0);
  const wrapRef = useRef<HTMLDivElement | null>(null);
  const inputRef = useRef<HTMLInputElement | null>(null);

  const filtered = useMemo(() => {
    const q = query.trim().toUpperCase();
    if (!q) return tickers;
    return tickers.filter((t) => t.includes(q));
  }, [tickers, query]);

  useEffect(() => {
    setHighlight(0);
  }, [query, open]);

  useEffect(() => {
    if (!open) return;
    function onClickOutside(e: MouseEvent) {
      if (wrapRef.current && !wrapRef.current.contains(e.target as Node)) {
        setOpen(false);
        setQuery('');
      }
    }
    document.addEventListener('mousedown', onClickOutside);
    return () => document.removeEventListener('mousedown', onClickOutside);
  }, [open]);

  useEffect(() => {
    if (open) inputRef.current?.focus();
  }, [open]);

  function commit(t: string) {
    onChange(t);
    setOpen(false);
    setQuery('');
  }

  return (
    <div ref={wrapRef} className="relative">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs uppercase tracking-wider text-slate-700 hover:bg-slate-200 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
      >
        {value} ▾
      </button>
      {open && (
        <div className="absolute right-0 top-full z-20 mt-1 w-56 overflow-hidden rounded-md border border-slate-200 bg-white shadow-lg dark:border-slate-700 dark:bg-slate-900">
          <input
            ref={inputRef}
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'ArrowDown') {
                e.preventDefault();
                setHighlight((h) => Math.min(h + 1, filtered.length - 1));
              } else if (e.key === 'ArrowUp') {
                e.preventDefault();
                setHighlight((h) => Math.max(h - 1, 0));
              } else if (e.key === 'Enter') {
                e.preventDefault();
                if (filtered[highlight]) commit(filtered[highlight]);
              } else if (e.key === 'Escape') {
                e.preventDefault();
                setOpen(false);
                setQuery('');
              }
            }}
            placeholder="Search currency…"
            className="w-full border-b border-slate-200 bg-white px-3 py-2 text-sm focus:outline-none dark:border-slate-700 dark:bg-slate-900"
          />
          <ul className="max-h-64 overflow-y-auto py-1 text-sm">
            {filtered.length === 0 && (
              <li className="px-3 py-2 text-xs text-slate-500">
                No matches
              </li>
            )}
            {filtered.map((t, i) => (
              <li key={t}>
                <button
                  type="button"
                  onClick={() => commit(t)}
                  onMouseEnter={() => setHighlight(i)}
                  className={`flex w-full items-center justify-between px-3 py-1.5 font-mono text-xs uppercase tracking-wider ${
                    i === highlight
                      ? 'bg-brand-50 text-brand-900 dark:bg-brand-900/30 dark:text-brand-100'
                      : 'text-slate-700 dark:text-slate-300'
                  }`}
                >
                  <span>{t}</span>
                  {t === value && (
                    <span className="text-[10px] text-slate-400">current</span>
                  )}
                </button>
              </li>
            ))}
          </ul>
        </div>
      )}
    </div>
  );
}
