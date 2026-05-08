import type { Metadata } from 'next';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

const PAIR_SEPARATOR = '~';

type Params = Promise<{ pair: string }>;

interface Market {
  base: string;
  quote: string;
  trade_count_24h: number;
  last_trade_at: string;
  volume_24h_usd?: string | null;
}

interface PriceResp {
  asset_id: string;
  quote: string;
  price?: string;
  observed_at?: string;
}

interface ChartPoint {
  t: string;
  p: string;
  v_usd?: string | null;
}

interface ChartResp {
  points: ChartPoint[];
}

function decodePairSlug(slug: string): { base: string; quote: string } | null {
  const decoded = decodeURIComponent(slug);
  const ix = decoded.indexOf(PAIR_SEPARATOR);
  if (ix === -1) return null;
  return { base: decoded.slice(0, ix), quote: decoded.slice(ix + 1) };
}

export async function generateStaticParams() {
  const fallback = [
    {
      pair: encodeURIComponent(
        `native${PAIR_SEPARATOR}USDC-GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN`,
      ),
    },
  ];
  if (isCIStub) return fallback;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/markets?limit=100&order_by=volume_24h_usd_desc`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: Market[] };
    const out = (env.data ?? [])
      .filter((m) => m.base && m.quote)
      .map((m) => ({ pair: `${m.base}${PAIR_SEPARATOR}${m.quote}` }));
    return out.length > 0 ? out : fallback;
  } catch {
    return fallback;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { pair } = await params;
  const decoded = decodePairSlug(pair);
  const label = decoded
    ? `${shortAsset(decoded.base)} / ${shortAsset(decoded.quote)}`
    : 'pair';
  return {
    title: `${label} — embeddable price widget`,
    description: `Iframe-friendly Stellar price ticker for ${label}.`,
    robots: { index: false, follow: false },
  };
}

async function fetchPrice(base: string, quote: string): Promise<PriceResp | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/price?asset=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: PriceResp };
    return env.data ?? null;
  } catch {
    return null;
  }
}

async function fetchChart(base: string, quote: string): Promise<ChartResp | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/chart?asset=${encodeURIComponent(base)}&quote=${encodeURIComponent(quote)}&timeframe=24h&granularity=1h`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: ChartResp };
    return env.data ?? null;
  } catch {
    return null;
  }
}

/**
 * /embed/pair/[base~quote] — iframe-friendly pair ticker.
 *
 * Same chrome-less shape as /embed/asset; trades the asset code
 * for a "BASE / QUOTE" label and uses the chart's first vs last
 * point to derive the 24h % change in lieu of asking the
 * /v1/coins endpoint.
 */
export default async function EmbedPairPage({ params }: { params: Params }) {
  const { pair } = await params;
  const decoded = decodePairSlug(pair);
  if (!decoded) {
    return (
      <div className="flex h-full min-h-32 items-center justify-center px-3 py-3 text-sm text-slate-500">
        Invalid pair slug
      </div>
    );
  }
  const { base, quote } = decoded;
  const [price, chart] = await Promise.all([
    fetchPrice(base, quote),
    fetchChart(base, quote),
  ]);

  const priceNum = price?.price ? Number(price.price) : null;
  const points = chart?.points ?? [];
  const change24h =
    points.length >= 2 && points[0]?.p && points[points.length - 1]?.p
      ? (Number(points[points.length - 1].p) / Number(points[0].p) - 1) * 100
      : null;

  const baseLabel = shortAsset(base);
  const quoteLabel = shortAsset(quote);
  const linkSlug = encodeURIComponent(`${base}${PAIR_SEPARATOR}${quote}`);

  return (
    <div className="flex h-full min-h-32 flex-col gap-2 bg-white px-4 py-3 text-slate-900 dark:bg-slate-900 dark:text-slate-100">
      <div className="flex items-baseline justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <span className="text-base font-semibold tracking-tight">
            {baseLabel} / {quoteLabel}
          </span>
          <span className="font-mono text-[10px] text-slate-500">Stellar</span>
        </div>
        <a
          href={`https://ratesengine.net/markets/${linkSlug}`}
          target="_blank"
          rel="noreferrer noopener"
          className="text-[10px] text-slate-400 hover:text-brand-600"
        >
          rates&shy;engine.net ↗
        </a>
      </div>
      <div className="flex items-baseline gap-2">
        <span className="font-mono text-2xl tabular-nums">
          {priceNum != null ? formatPrice(priceNum) : '—'}
        </span>
        <ChangeChip pct={change24h} label="24h" />
      </div>
      {points.length > 0 && <Sparkline points={points} positive={(change24h ?? 0) >= 0} />}
      <div className="mt-auto text-[10px] text-slate-400">
        Powered by Rates Engine
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

function Sparkline({ points, positive }: { points: { p?: string | null }[]; positive: boolean }) {
  const w = 280;
  const h = 32;
  const valid = points
    .map((pt, i) => ({ i, n: pt.p ? Number(pt.p) : null }))
    .filter((p) => p.n != null && Number.isFinite(p.n!) && p.n! > 0);
  if (valid.length < 2) return null;
  const min = Math.min(...valid.map((p) => p.n!));
  const max = Math.max(...valid.map((p) => p.n!));
  const range = max - min || max * 0.01;
  const xStep = points.length > 1 ? w / (points.length - 1) : 0;
  const segs: string[] = [];
  type Run = { start: number; end: number };
  const runs: Run[] = [];
  let pen = false;
  let runStart = -1;
  let lastIdx = -1;
  points.forEach((pt, i) => {
    const n = pt.p ? Number(pt.p) : null;
    if (n == null || !Number.isFinite(n) || n <= 0) {
      if (pen && runStart >= 0) runs.push({ start: runStart, end: lastIdx });
      pen = false;
      runStart = -1;
      return;
    }
    const x = i * xStep;
    const y = h - ((n - min) / range) * h;
    segs.push(`${pen ? 'L' : 'M'} ${x.toFixed(1)} ${y.toFixed(1)}`);
    if (!pen) runStart = i;
    pen = true;
    lastIdx = i;
  });
  if (pen && runStart >= 0) runs.push({ start: runStart, end: lastIdx });
  const fill = positive ? 'rgba(16,185,129,0.14)' : 'rgba(244,63,94,0.14)';
  const areaSegs: string[] = [];
  for (const run of runs) {
    let started = false;
    for (let i = run.start; i <= run.end; i++) {
      const n = points[i]?.p ? Number(points[i].p) : null;
      if (n == null || !Number.isFinite(n) || n <= 0) continue;
      const x = i * xStep;
      const y = h - ((n - min) / range) * h;
      areaSegs.push(`${started ? 'L' : 'M'} ${x.toFixed(1)} ${y.toFixed(1)}`);
      started = true;
    }
    const xStart = run.start * xStep;
    const xEnd = run.end * xStep;
    areaSegs.push(`L ${xEnd.toFixed(1)} ${h} L ${xStart.toFixed(1)} ${h} Z`);
  }
  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      className="h-8 w-full"
      aria-label="24-hour price sparkline"
    >
      <path d={areaSegs.join(' ')} stroke="none" fill={fill} />
      <path
        d={segs.join(' ')}
        fill="none"
        strokeWidth="1.5"
        className={positive ? 'stroke-emerald-500' : 'stroke-rose-500'}
      />
    </svg>
  );
}

function shortAsset(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical.replace('crypto:', '');
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  return canonical.slice(0, dashIx);
}

function formatPrice(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (n >= 1) return n.toFixed(n >= 100 ? 2 : 4);
  if (n >= 0.001) return n.toFixed(6);
  if (n > 0) return n.toExponential(3);
  return '—';
}
