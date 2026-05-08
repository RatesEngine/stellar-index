'use client';

import { useMemo, useState } from 'react';
import { useQuery } from '@tanstack/react-query';
import dynamic from 'next/dynamic';
import Link from 'next/link';
import { ArrowLeft } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { CurrencyCombobox } from '@/components/CurrencyCombobox';
import { apiGet, asExample } from '@/api/client';

// Lightweight-charts (~155 KB) is only needed once the user is on a
// per-currency page — lazy-load so the listing → detail nav still
// hits TTI quickly. ssr:false because lightweight-charts touches
// the canvas API on construction.
const LineChart = dynamic(
  () => import('@/components/charts/LineChart').then((m) => m.LineChart),
  { ssr: false, loading: () => <div className="h-[320px]" /> },
);

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
  history_range?: string;
  history?: HistoryPoint[];
  published_at?: string;
  fetched_at?: string;
  source?: string;
}

type RangeKey = '7d' | '30d' | '90d' | '1y' | '5y' | '10y' | 'all';

const RANGE_OPTIONS: { key: RangeKey; label: string }[] = [
  { key: '7d', label: '7d' },
  { key: '30d', label: '30d' },
  { key: '90d', label: '90d' },
  { key: '1y', label: '1y' },
  { key: '5y', label: '5y' },
  { key: '10y', label: '10y' },
  { key: 'all', label: 'All' },
];

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
          <Converter detail={detail} />
          <HistoryPanel detail={detail} />
          <CrossRatesTable detail={detail} />
          <AboutPanel detail={detail} />
          <FAQPanel detail={detail} />
        </>
      )}
    </div>
  );
}

function HistoryPanel({ detail }: { detail: CurrencyDetail }) {
  const [range, setRange] = useState<RangeKey>('1y');

  // 7d data ships in the base /v1/currencies/{ticker} response so
  // the default render needs no second fetch. Anything longer hits
  // /v1/currencies/{ticker}?range=<range> which queries the
  // fx_quotes hypertable server-side.
  const longQ = useQuery<HistoryPoint[]>({
    queryKey: ['/v1/currencies', detail.ticker, 'history', range],
    queryFn: async () => {
      const env = await apiGet<{ data: CurrencyDetail }>(
        `/v1/currencies/${detail.ticker}?range=${range}`,
        {},
      );
      return env.data.history ?? [];
    },
    enabled: range !== '7d',
    staleTime: 5 * 60_000,
  });

  const series: HistoryPoint[] =
    range === '7d'
      ? detail.history_7d ?? []
      : longQ.data ?? [];

  const hasData = series.length >= 2;
  const first = hasData ? series[0].inverse_usd : 0;
  const last = hasData ? series[series.length - 1].inverse_usd : 0;
  const changePct = hasData && first > 0 ? ((last - first) / first) * 100 : 0;
  const positive = changePct >= 0;

  const rangeLabel = RANGE_OPTIONS.find((o) => o.key === range)?.label ?? range;

  const stats = hasData ? computeRangeStats(series) : null;

  return (
    <Panel
      title={`${rangeLabel} USD value`}
      hint={`1 ${detail.ticker} expressed in USD${
        range === '7d' ? ' over the last week' : ''
      }`}
      source={asExample(`/v1/currencies/${detail.ticker}?range=${range}`, {})}
    >
      <div className="mb-3 flex flex-wrap items-center gap-2">
        <RangeSelector value={range} onChange={setRange} />
        {range !== '7d' && longQ.isFetching && (
          <span className="text-xs text-slate-500">Loading…</span>
        )}
      </div>
      {!hasData && !longQ.isFetching ? (
        <p className="text-sm text-slate-500">
          {range === '7d'
            ? 'History is warming up.'
            : `No persistent ${rangeLabel} history yet — backfill is in progress.`}
        </p>
      ) : (
        <div className="space-y-3">
          <div>
            <div className="text-xs uppercase tracking-wider text-slate-500">
              {rangeLabel} change
            </div>
            <div
              className={`mt-1 text-2xl font-mono tabular-nums ${
                positive ? 'text-emerald-700 dark:text-emerald-400' : 'text-rose-700 dark:text-rose-400'
              }`}
            >
              {hasData ? `${positive ? '+' : ''}${changePct.toFixed(2)}%` : '—'}
            </div>
          </div>
          {stats && <RangeStatsGrid stats={stats} ticker={detail.ticker} />}
          <LineChart
            data={series.map((p) => ({
              time: Math.floor(new Date(p.date).getTime() / 1000),
              value: p.inverse_usd,
            }))}
            positive={positive}
            height={range === '7d' ? 220 : 320}
          />
        </div>
      )}
    </Panel>
  );
}

interface RangeStats {
  high: number;
  highDate: string;
  low: number;
  lowDate: string;
  current: number;
  fromHighPct: number;
  fromLowPct: number;
  daysSinceHigh: number;
  avgDailyMovePct: number;
  pointCount: number;
}

// computeRangeStats — derives volatility + range stats from the
// already-fetched history series (no extra fetch). Caller is
// responsible for ensuring `series` has ≥2 points.
function computeRangeStats(series: HistoryPoint[]): RangeStats {
  let high = -Infinity;
  let low = Infinity;
  let highIdx = 0;
  let lowIdx = 0;
  for (let i = 0; i < series.length; i++) {
    const v = series[i].inverse_usd;
    if (v > high) {
      high = v;
      highIdx = i;
    }
    if (v < low) {
      low = v;
      lowIdx = i;
    }
  }
  const current = series[series.length - 1].inverse_usd;
  // Average absolute day-over-day move as a percent of the prior
  // close. Skips zeros to avoid divide-by-zero on data gaps.
  let moveSum = 0;
  let moveN = 0;
  for (let i = 1; i < series.length; i++) {
    const prev = series[i - 1].inverse_usd;
    if (prev <= 0) continue;
    moveSum += Math.abs(series[i].inverse_usd - prev) / prev;
    moveN++;
  }
  const avgDailyMovePct = moveN > 0 ? (moveSum / moveN) * 100 : 0;
  const lastTs = new Date(series[series.length - 1].date).getTime();
  const highTs = new Date(series[highIdx].date).getTime();
  const daysSinceHigh = Math.max(
    0,
    Math.round((lastTs - highTs) / 86_400_000),
  );
  const fromHighPct = high > 0 ? ((current - high) / high) * 100 : 0;
  const fromLowPct = low > 0 ? ((current - low) / low) * 100 : 0;
  return {
    high,
    highDate: series[highIdx].date,
    low,
    lowDate: series[lowIdx].date,
    current,
    fromHighPct,
    fromLowPct,
    daysSinceHigh,
    avgDailyMovePct,
    pointCount: series.length,
  };
}

function RangeStatsGrid({
  stats,
  ticker,
}: {
  stats: RangeStats;
  ticker: string;
}) {
  return (
    <div className="grid grid-cols-2 gap-3 rounded-md border border-slate-200 bg-slate-50/60 p-3 text-sm dark:border-slate-800 dark:bg-slate-900/40 sm:grid-cols-4">
      <Stat
        label="Range high"
        value={`$${formatRate(stats.high)}`}
        sub={`${formatShortDate(stats.highDate)} · ${stats.daysSinceHigh}d ago`}
      />
      <Stat
        label="Range low"
        value={`$${formatRate(stats.low)}`}
        sub={formatShortDate(stats.lowDate)}
      />
      <Stat
        label="From high"
        value={`${stats.fromHighPct >= 0 ? '+' : ''}${stats.fromHighPct.toFixed(2)}%`}
        tone={stats.fromHighPct >= 0 ? 'pos' : 'neg'}
        sub={`From low ${stats.fromLowPct >= 0 ? '+' : ''}${stats.fromLowPct.toFixed(2)}%`}
      />
      <Stat
        label="Avg daily move"
        value={`±${stats.avgDailyMovePct.toFixed(2)}%`}
        sub={`across ${stats.pointCount} ${ticker} obs`}
      />
    </div>
  );
}

function Stat({
  label,
  value,
  sub,
  tone,
}: {
  label: string;
  value: string;
  sub?: string;
  tone?: 'pos' | 'neg';
}) {
  const toneClass =
    tone === 'pos'
      ? 'text-emerald-700 dark:text-emerald-400'
      : tone === 'neg'
        ? 'text-rose-700 dark:text-rose-400'
        : 'text-slate-900 dark:text-slate-100';
  return (
    <div>
      <div className="text-[10px] uppercase tracking-wider text-slate-500">
        {label}
      </div>
      <div className={`mt-0.5 font-mono text-base tabular-nums ${toneClass}`}>
        {value}
      </div>
      {sub && (
        <div className="text-[11px] text-slate-500">{sub}</div>
      )}
    </div>
  );
}

function formatShortDate(iso: string): string {
  try {
    const d = new Date(iso);
    return d.toLocaleDateString(undefined, {
      year: 'numeric',
      month: 'short',
      day: 'numeric',
    });
  } catch {
    return iso.slice(0, 10);
  }
}

function RangeSelector({
  value,
  onChange,
}: {
  value: RangeKey;
  onChange: (v: RangeKey) => void;
}) {
  return (
    <div className="inline-flex rounded-md border border-slate-200 bg-white p-0.5 text-xs dark:border-slate-700 dark:bg-slate-900">
      {RANGE_OPTIONS.map((opt) => (
        <button
          key={opt.key}
          type="button"
          onClick={() => onChange(opt.key)}
          className={`rounded px-2 py-1 font-medium tabular-nums ${
            opt.key === value
              ? 'bg-brand-100 text-brand-900 dark:bg-brand-900/40 dark:text-brand-100'
              : 'text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800'
          }`}
        >
          {opt.label}
        </button>
      ))}
    </div>
  );
}

// (Local SVG Sparkline replaced by lightweight-charts LineChart for
// hover crosshair, panning, zooming. See @/components/charts/LineChart.)

function Converter({ detail }: { detail: CurrencyDetail }) {
  // Bidirectional swap widget: both sides have a searchable
  // currency picker. Default state mirrors the page (this currency
  // ↔ USD). Editing either input recomputes the other side using
  // the rate(from→to) cross-rate matrix.
  const [from, setFrom] = useState(detail.ticker);
  const [to, setTo] = useState<string>(detail.ticker === 'USD' ? 'EUR' : 'USD');
  const [amount, setAmount] = useState('1');
  const [activeSide, setActiveSide] = useState<'from' | 'to'>('from');

  const allTickers = useMemo(() => {
    const set = new Set<string>([detail.ticker, 'USD', ...Object.keys(detail.cross_rates)]);
    return Array.from(set).sort();
  }, [detail]);

  // Cross-rate matrix: rate(detail.ticker → other) is in
  // detail.cross_rates[other]. We synthesise rate(from→to) by
  // routing through detail.ticker — this is pure forex maths
  // (rate(A→B) = rate(detail→B) / rate(detail→A)).
  function rateOf(target: string): number | null {
    if (target === detail.ticker) return 1;
    if (target === 'USD') {
      return detail.inverse_usd > 0 ? detail.inverse_usd : null;
    }
    const v = detail.cross_rates[target];
    return v != null && Number.isFinite(v) ? v : null;
  }
  function rateBetween(a: string, b: string): number | null {
    const ra = rateOf(a);
    const rb = rateOf(b);
    if (ra == null || rb == null || ra === 0) return null;
    return rb / ra;
  }

  const fwdRate = rateBetween(from, to);
  const bwdRate = fwdRate != null && fwdRate !== 0 ? 1 / fwdRate : null;
  const numericAmount = Number(amount);
  const result = activeSide === 'from'
    ? (Number.isFinite(numericAmount) && fwdRate != null ? numericAmount * fwdRate : null)
    : (Number.isFinite(numericAmount) && bwdRate != null ? numericAmount * bwdRate : null);

  function swap() {
    setFrom(to);
    setTo(from);
    // Keep the visible value sensible: if the user was editing
    // 'from', the new 'from' is the old 'to' so reuse the result.
    if (result != null) setAmount(formatRate(result));
    setActiveSide('from');
  }

  // Display: when activeSide is 'from', the right input shows the
  // computed result; when 'to', the left input shows the computed
  // back-conversion. Both inputs are editable; clicking one
  // sets it as the active side.
  const fromValue = activeSide === 'from' ? amount : (result != null ? formatRate(result) : '—');
  const toValue = activeSide === 'to' ? amount : (result != null ? formatRate(result) : '—');

  return (
    <Panel
      title="Converter"
      hint={`Rates derived from ${detail.source ?? 'massive'}`}
      source={asExample(`/v1/currencies/${detail.ticker}`, {})}
    >
      <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-[1fr_auto_1fr]">
        <CurrencyInput
          label="You pay"
          tickers={allTickers}
          ticker={from}
          onTicker={setFrom}
          value={fromValue}
          onValue={(v) => {
            setActiveSide('from');
            setAmount(v);
          }}
          editable
        />
        <button
          type="button"
          aria-label="Swap currencies"
          onClick={swap}
          className="self-center rounded-md border border-slate-200 px-2 py-1 text-xs text-slate-500 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:text-slate-400 sm:mb-1"
        >
          ⇄
        </button>
        <CurrencyInput
          label="You get"
          tickers={allTickers}
          ticker={to}
          onTicker={setTo}
          value={toValue}
          onValue={(v) => {
            setActiveSide('to');
            setAmount(v);
          }}
          editable
        />
      </div>
      <p className="mt-3 text-xs text-slate-500">
        1 {from} = {fwdRate != null ? formatRate(fwdRate) : '—'} {to}
        {fwdRate != null && (
          <>
            <span className="mx-2 text-slate-400">·</span>
            <span>1 {to} = {formatRate(1 / fwdRate)} {from}</span>
          </>
        )}
      </p>
    </Panel>
  );
}

function CurrencyInput({
  label,
  tickers,
  ticker,
  onTicker,
  value,
  onValue,
  editable,
}: {
  label: string;
  tickers: string[];
  ticker: string;
  onTicker: (t: string) => void;
  value: string;
  onValue: (v: string) => void;
  editable?: boolean;
}) {
  return (
    <label className="space-y-1">
      <span className="text-xs uppercase tracking-wider text-slate-500">{label}</span>
      <div className="flex items-center gap-2 rounded-md border border-slate-200 bg-white p-2 dark:border-slate-700 dark:bg-slate-900">
        <input
          type="number"
          value={value === '—' ? '' : value}
          placeholder={value === '—' ? '—' : ''}
          onChange={editable ? (e) => onValue(e.target.value) : undefined}
          readOnly={!editable}
          min="0"
          step="any"
          inputMode="decimal"
          className="w-full bg-transparent text-2xl font-mono tabular-nums focus:outline-none"
        />
        <CurrencyCombobox tickers={tickers} value={ticker} onChange={onTicker} />
      </div>
    </label>
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

// (Local CurrencyCombobox extracted to @/components/CurrencyCombobox.)

// CURATED_ABOUT — short descriptions for the major fiat currencies.
// Sourced from the issuing central bank's own positioning + ISO 4217
// metadata. Currencies not in this map render a generic "About"
// section that calls out the ISO code + last-published rate;
// expanding the curated set is a one-line addition here. Multiple
// paragraphs separated by blank lines render as separate <p>'s.
const CURATED_ABOUT: Record<string, string> = {
  USD: `The United States dollar is the official currency of the United States and the world's primary reserve currency. Issued by the Federal Reserve System, it underpins the largest share of international trade, foreign-exchange reserves, and most major commodity benchmarks. The "$" symbol predates the country itself and was inherited from the Spanish colonial peso.

Most fiat currencies on Rates Engine are quoted via their USD cross-rate. When a stablecoin says "USD-pegged," it almost always means USDC, USDT, or another token redeemable 1:1 against this currency.`,
  EUR: `The euro is the official currency of 20 European Union member states (the eurozone) and the second-most-held reserve currency globally. Managed by the European Central Bank, it replaced 12 national currencies on January 1, 1999 (cash circulation began January 1, 2002).

EUR-pegged stablecoins like EURC and EUROC track this rate; on the Stellar network EURC is issued by Circle and is one of the largest non-USD stablecoins by volume.`,
  GBP: `The pound sterling is the world's oldest currency in continuous use, dating to ~775 AD. Issued by the Bank of England, it remains a major reserve currency despite the UK's smaller economic share than USD or EUR. The "£" glyph derives from the Latin "libra" (a pound weight).`,
  JPY: `The Japanese yen is the third-most-traded currency on FX markets and a traditional safe-haven asset during global risk-off episodes. Issued by the Bank of Japan, it has the unusual property among major currencies of being quoted without subdivisions in everyday use — there is no Japanese equivalent of "cents." Sub-yen units (sen, rin) exist on paper but were withdrawn from circulation in 1953.`,
  CHF: `The Swiss franc is widely regarded as a safe-haven currency, partly because of Switzerland's neutrality and historically prudent monetary policy. Issued by the Swiss National Bank, it carried a 1.20-per-EUR floor between 2011 and January 2015, when the SNB abruptly removed the cap and the franc appreciated ~15% in a single day.`,
  CNY: `The Chinese yuan (renminbi) is the official currency of the People's Republic of China, issued by the People's Bank of China. It is partially convertible — the offshore yuan (CNH) trades freely while the onshore yuan (CNY) is managed within a daily band against a basket of currencies.`,
  INR: `The Indian rupee is the official currency of India, issued by the Reserve Bank of India. The "₹" symbol was adopted in 2010, replacing the "Rs." abbreviation. India's economy is one of the largest emerging markets and a major remittance corridor — the Stellar network sees significant ZAR/USD/EUR/INR-via-stablecoin flows.`,
  XLM: `Stellar Lumens (XLM) is the native asset of the Stellar network — the blockchain that Rates Engine indexes natively. Lumens pay transaction fees, fund minimum account reserves (currently 1 XLM per base reserve unit + 0.5 per ledger entry), and serve as a convenient bridge asset for path-payments between any two issued tokens.

XLM has a fixed maximum supply of ~50B; the Stellar Development Foundation periodically retires from its inflation pool. Unlike Bitcoin's purely-mined supply curve, lumen supply changes are governed by SDF allocation policy.`,
};

// CURATED_FAQ — common questions surfaced per page. Generic
// fallback covers tickers without curated entries.
function faqFor(ticker: string, name: string): { q: string; a: string }[] {
  const generic = [
    {
      q: `What is ${ticker}?`,
      a: `${ticker} is the ISO 4217 currency code for ${name}. Rates Engine quotes its rate against USD via the Massive (Polygon.io) forex feed, refreshed hourly with daily-grain data sourced from major reference series.`,
    },
    {
      q: `How is the rate calculated?`,
      a: `We pull the upstream's grouped-daily snapshot once an hour and surface its USD-base rate verbatim. The "1 USD = N units" form is canonical; the "1 unit = $X" inverse is computed at display time. No internal smoothing or aggregation is applied to the fiat feed — the value you see is what the upstream published.`,
    },
    {
      q: `What is circulating supply for a fiat currency?`,
      a: `For fiat we use the central bank's broadest commonly-published monetary aggregate (typically M2). Where that's unavailable we fall back to monetary base or the issuer's own circulation declaration. Sourced from the curated CSV in internal/sources/forex/circulation_data.csv; not every currency has a recent series.`,
    },
    {
      q: `How often does the rate update?`,
      a: `Hourly. The forex worker refreshes from the upstream every 60 minutes; persistent fx_quotes hypertable rows are upserted on the same cadence. The detail page's "Source published" timestamp shows the upstream's own publish date.`,
    },
  ];
  return generic;
}

function AboutPanel({ detail }: { detail: CurrencyDetail }) {
  const text = CURATED_ABOUT[detail.ticker];
  if (!text) {
    return null; // suppress for tickers without a curated entry
  }
  return <ExpandableText title={`About ${detail.name}`} body={text} />;
}

function ExpandableText({ title, body }: { title: string; body: string }) {
  const [expanded, setExpanded] = useState(false);
  const paragraphs = body.split(/\n\s*\n/).filter(Boolean);
  const teaser = paragraphs[0];
  const more = paragraphs.slice(1);
  const hasMore = more.length > 0;
  return (
    <Panel
      title={title}
      bodyClassName="text-sm text-slate-700 dark:text-slate-300 space-y-3 leading-relaxed"
    >
      <p>{teaser}</p>
      {expanded && more.map((p, i) => <p key={i}>{p}</p>)}
      {hasMore && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="text-xs text-brand-600 hover:underline"
        >
          {expanded ? 'Show less' : 'Read more →'}
        </button>
      )}
    </Panel>
  );
}

function FAQPanel({ detail }: { detail: CurrencyDetail }) {
  const items = faqFor(detail.ticker, detail.name);
  return (
    <Panel
      title="FAQ"
      hint="Common questions about this currency"
      bodyClassName="space-y-2 text-sm"
    >
      {items.map((it, i) => (
        <FAQItem key={i} q={it.q} a={it.a} />
      ))}
    </Panel>
  );
}

function FAQItem({ q, a }: { q: string; a: string }) {
  const [open, setOpen] = useState(false);
  return (
    <div className="rounded-lg border border-slate-200 dark:border-slate-800">
      <button
        type="button"
        onClick={() => setOpen((v) => !v)}
        aria-expanded={open}
        className="flex w-full items-center justify-between px-3 py-2 text-left font-medium text-slate-900 hover:bg-slate-50 dark:text-slate-100 dark:hover:bg-slate-900/40"
      >
        <span>{q}</span>
        <span aria-hidden className="text-xs text-slate-400">{open ? '−' : '+'}</span>
      </button>
      {open && (
        <p className="border-t border-slate-200 px-3 py-2 text-sm leading-relaxed text-slate-700 dark:border-slate-800 dark:text-slate-300">
          {a}
        </p>
      )}
    </div>
  );
}
