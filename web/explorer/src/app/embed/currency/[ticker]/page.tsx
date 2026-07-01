import type { Metadata } from 'next';

import { assetHrefFor } from '@/lib/fiat-slugs';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.stellarindex.io';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

type Params = Promise<{ ticker: string }>;

// Wire shape of /v1/assets/{ticker} for a fiat catalogue entry —
// returns GlobalAssetView when the ticker resolves to a verified
// currency. F-1201 migrated this from /v1/currencies/{ticker}.
interface GlobalAssetView {
  ticker: string;
  slug: string;
  name: string;
  class: string; // crypto | stablecoin | fiat
  price_usd?: string | null; // "1 unit of asset = X USD" (decimal string)
}

interface CurrencyDetail {
  ticker: string;
  name: string;
  rate_usd: number; // 1 USD = X local (= 1 / price_usd)
  inverse_usd: number; // 1 local = X USD (= price_usd)
}

const FALLBACK = ['USD', 'EUR', 'GBP', 'JPY', 'CHF', 'CAD', 'AUD', 'CNY'];

export async function generateStaticParams() {
  if (isCIStub) return FALLBACK.map((ticker) => ({ ticker }));
  // Migrated from /v1/currencies → /v1/assets/verified (rc.48 +
  // F-1201 audit-2026-05-12). Filter to class=fiat to keep the
  // pre-rendered set focused on FX-rate widget consumers.
  try {
    const res = await fetch(`${API_BASE_URL}/v1/assets/verified`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as {
      data: Array<{ ticker: string; class: string }>;
    };
    const tickers = (env.data ?? [])
      .filter((row) => row.class === 'fiat')
      .map((row) => row.ticker);
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

interface ChartPoint {
  t: string;
  p?: string | null;
}

// fetchFxSeries pulls the trailing-7d daily FX series (1 ticker = X USD)
// from /v1/chart so the widget shows a real sparkline + 7d change rather
// than a static price. Degrades to [] on any error (price-only render).
async function fetchFxSeries(ticker: string): Promise<{ date: string; inverse_usd: number }[]> {
  if (isCIStub) return [];
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/chart?asset=fiat:${encodeURIComponent(ticker)}&quote=fiat:USD&timeframe=1w&granularity=1d`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return [];
    const env = (await res.json()) as { data?: { points?: ChartPoint[] } };
    return (env.data?.points ?? [])
      .map((p) => ({ date: p.t, inverse_usd: p.p != null ? Number(p.p) : NaN }))
      .filter((p) => Number.isFinite(p.inverse_usd) && p.inverse_usd > 0);
  } catch {
    return [];
  }
}

async function fetchCurrency(ticker: string): Promise<CurrencyDetail | null> {
  if (isCIStub) return null;
  // Migrated from /v1/currencies/{ticker} → /v1/assets/{ticker}.
  // The catalogue dispatcher resolves an ISO ticker (EUR, GBP, …)
  // via LookupByTicker and returns the cross-chain GlobalAssetView
  // shape; we project that into the CurrencyDetail shape this
  // widget renders. Sparkline + 24h/7d change come from a future
  // /v1/chart hookup (deliberately omitted for now to keep this
  // widget rendering after rc.48 — better a static price than a
  // 404).
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/assets/${encodeURIComponent(ticker.toUpperCase())}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: GlobalAssetView };
    const view = env.data;
    if (!view || view.class !== 'fiat') return null;
    const priceUSD = view.price_usd ? Number(view.price_usd) : 0;
    if (!(priceUSD > 0)) return null;
    return {
      ticker: view.ticker,
      name: view.name,
      rate_usd: 1 / priceUSD, // 1 USD = X local
      inverse_usd: priceUSD,  // 1 local = X USD
    };
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
  const [cur, series] = await Promise.all([
    fetchCurrency(upper),
    fetchFxSeries(upper),
  ]);

  if (!cur) {
    return (
      <div className="flex h-full min-h-32 items-center justify-center px-3 py-3 text-sm text-ink-muted">
        <span>No data for {upper}</span>
      </div>
    );
  }

  const priceUSD = cur.inverse_usd > 0 ? cur.inverse_usd : null;
  // 7d change + sparkline now come from /v1/chart (fetchFxSeries). 24h
  // change needs intraday granularity we don't pull here, so the 24h
  // chip stays hidden — better honest than fabricated.
  const change7d: number | null =
    series.length >= 2 && series[0].inverse_usd > 0
      ? ((series[series.length - 1].inverse_usd - series[0].inverse_usd) / series[0].inverse_usd) * 100
      : null;
  const change24h: number | null = null;

  return (
    <div className="flex h-full min-h-32 flex-col gap-2 bg-surface px-4 py-3 text-ink">
      <div className="flex items-baseline justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <span className="text-base font-semibold tracking-tight">{upper}</span>
          <span className="font-mono text-[10px] text-ink-muted">{cur.name}</span>
        </div>
        <a
          href={`https://stellarindex.io${assetHrefFor(upper)}`}
          target="_blank"
          rel="noreferrer noopener"
          className="text-[10px] text-ink-faint hover:text-brand-600"
        >
          stellarindex.io ↗
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
      <div className="mt-auto flex items-center justify-between text-[10px] text-ink-faint">
        <span>Powered by Stellar Index</span>
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
      ? 'bg-up-subtle text-up'
      : pct < 0
        ? 'bg-down-subtle text-down'
        : 'bg-surface-subtle text-ink-body';
  return (
    <span className={`rounded-sm px-1.5 py-0.5 font-mono text-[11px] tabular-nums ${cls}`}>
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
