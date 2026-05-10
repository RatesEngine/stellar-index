import type { Metadata } from 'next';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

type Params = Promise<{ ticker: string }>;

interface CurrencyDetail {
  ticker: string;
  name: string;
  rate_usd: number;
  inverse_usd: number;
  change_24h_pct?: number | null;
  change_7d_pct?: number | null;
  history_7d?: { date: string; rate_usd: number; inverse_usd: number }[];
}

const FALLBACK = ['USD', 'EUR', 'GBP', 'JPY', 'CHF', 'CAD', 'AUD', 'CNY'];

export async function generateStaticParams() {
  if (isCIStub) return FALLBACK.map((ticker) => ({ ticker }));
  try {
    const res = await fetch(`${API_BASE_URL}/v1/currencies`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: { currencies?: { ticker: string }[] } };
    const tickers = env.data?.currencies?.map((c) => c.ticker) ?? [];
    if (tickers.length === 0) return FALLBACK.map((t) => ({ ticker: t }));
    return tickers.map((ticker) => ({ ticker }));
  } catch {
    return FALLBACK.map((t) => ({ ticker: t }));
  }
}

export async function generateMetadata({ params }: { params: Params }): Promise<Metadata> {
  const { ticker } = await params;
  return {
    title: `${ticker.toUpperCase()} — embeddable currency widget`,
    description: `Iframe-friendly forex rate ticker for ${ticker.toUpperCase()} vs USD. Drop into any site at any width.`,
    robots: { index: false, follow: false },
  };
}

async function fetchCurrency(ticker: string): Promise<CurrencyDetail | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/currencies/${encodeURIComponent(ticker.toUpperCase())}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: CurrencyDetail };
    return env.data ?? null;
  } catch {
    return null;
  }
}

/**
 * /embed/currency/[ticker] — minimal forex widget for iframe embeds.
 *
 * Shape mirrors /embed/asset/[slug]: ticker / name / inverse-USD
 * rate / 7d change / sparkline / "powered by" attribution. SEO
 * opted-out via robots noindex.
 */
export default async function EmbedCurrencyPage({ params }: { params: Params }) {
  const { ticker } = await params;
  const upper = ticker.toUpperCase();
  const cur = await fetchCurrency(upper);

  if (!cur) {
    return (
      <div className="flex h-full min-h-32 items-center justify-center px-3 py-3 text-sm text-slate-500">
        <span>No data for {upper}</span>
      </div>
    );
  }

  const priceUSD = cur.inverse_usd > 0 ? cur.inverse_usd : null;
  const series = cur.history_7d ?? [];
  // Prefer the API-computed 7d change (consistent with the rest of
  // the explorer) and fall back to deriving from history when the
  // upstream omits it.
  let change7d: number | null = cur.change_7d_pct ?? null;
  if (change7d == null && series.length >= 2) {
    const first = series[0].inverse_usd;
    const last = series[series.length - 1].inverse_usd;
    if (first > 0 && last > 0) {
      change7d = ((last - first) / first) * 100;
    }
  }
  const change24h: number | null = cur.change_24h_pct ?? null;

  return (
    <div className="flex h-full min-h-32 flex-col gap-2 bg-white px-4 py-3 text-slate-900 dark:bg-slate-900 dark:text-slate-100">
      <div className="flex items-baseline justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <span className="text-base font-semibold tracking-tight">{upper}</span>
          <span className="font-mono text-[10px] text-slate-500">{cur.name}</span>
        </div>
        <a
          href={`https://ratesengine.net/currencies/${upper}`}
          target="_blank"
          rel="noreferrer noopener"
          className="text-[10px] text-slate-400 hover:text-brand-600"
        >
          rates&shy;engine.net ↗
        </a>
      </div>
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-2xl tabular-nums">
          {priceUSD != null ? `$${formatRate(priceUSD)}` : '—'}
        </span>
        <ChangeChip pct={change24h} label="24h" />
        <ChangeChip pct={change7d} label="7d" />
      </div>
      {series.length >= 2 && (
        <Sparkline
          points={series.map((p) => p.inverse_usd)}
          positive={(change7d ?? 0) >= 0}
        />
      )}
      <div className="mt-auto flex items-center justify-between text-[10px] text-slate-400">
        <span>Powered by Rates Engine</span>
        {cur.rate_usd > 0 && (
          <span className="font-mono tabular-nums">
            1 USD = {formatRate(cur.rate_usd)} {upper}
          </span>
        )}
      </div>
    </div>
  );
}

function ChangeChip({ pct, label }: { pct: number | null | undefined; label: string }) {
  if (pct == null || !Number.isFinite(pct)) return null;
  const cls =
    pct > 0
      ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
      : pct < 0
        ? 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300'
        : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400';
  return (
    <span className={`rounded px-1.5 py-0.5 font-mono text-[11px] tabular-nums ${cls}`}>
      {pct > 0 ? '+' : ''}
      {pct.toFixed(2)}% {label}
    </span>
  );
}

function Sparkline({ points, positive }: { points: number[]; positive: boolean }) {
  const valid = points.filter((n) => Number.isFinite(n) && n > 0);
  if (valid.length < 2) return null;
  const min = Math.min(...valid);
  const max = Math.max(...valid);
  const range = max - min || max * 0.01;
  const w = 280;
  const h = 36;
  const stepX = w / (valid.length - 1);
  const xy = valid.map((p, i) => ({
    x: i * stepX,
    y: h - ((p - min) / range) * h,
  }));
  const path = xy
    .map((pt, i) => `${i === 0 ? 'M' : 'L'}${pt.x.toFixed(2)},${pt.y.toFixed(2)}`)
    .join(' ');
  const area = `${path} L${xy[xy.length - 1].x.toFixed(2)},${h} L${xy[0].x.toFixed(2)},${h} Z`;
  const stroke = positive ? '#059669' : '#e11d48';
  const fill = positive ? 'rgba(16,185,129,0.14)' : 'rgba(244,63,94,0.14)';
  return (
    <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" className="h-9 w-full">
      <path d={area} fill={fill} stroke="none" />
      <path d={path} fill="none" stroke={stroke} strokeWidth="1.5" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

function formatRate(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  if (n >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (n >= 1) return n.toFixed(4);
  if (n >= 0.0001) return n.toFixed(6);
  return n.toExponential(3);
}
