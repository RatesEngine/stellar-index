import type { Metadata } from 'next';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

type Params = Promise<{ slug: string }>;

interface Coin {
  asset_id: string;
  code: string;
  slug: string;
  price_usd?: string | null;
  change_24h_pct?: string | null;
  price_history_24h?: { t: string; p?: string | null }[];
  volume_24h_usd?: string | null;
}

export async function generateStaticParams() {
  const fallback = [{ slug: 'XLM' }];
  if (isCIStub) return fallback;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/coins?limit=500`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: { coins: { slug: string }[] } };
    const slugs = env.data?.coins?.map((c) => c.slug) ?? [];
    if (slugs.length === 0) return fallback;
    // Always include XLM + native explicitly.
    const seen = new Set<string>();
    const out: { slug: string }[] = [];
    for (const slug of ['XLM', 'native', ...slugs]) {
      if (!seen.has(slug)) {
        seen.add(slug);
        out.push({ slug });
      }
    }
    return out;
  } catch {
    return fallback;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { slug } = await params;
  return {
    title: `${slug} — embeddable price widget`,
    description: `Iframe-friendly Stellar price ticker for ${slug}. Designed to be dropped into a customer site at any width.`,
    robots: { index: false, follow: false },
  };
}

async function fetchCoin(slug: string): Promise<Coin | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/coins/${encodeURIComponent(slug)}`,
      { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: Coin };
    return env.data ?? null;
  } catch {
    return null;
  }
}

/**
 * /embed/asset/[slug] — minimal price widget designed to be iframed.
 *
 * No navbar, no footer, no global font weight overrides. Just the
 * price + 24h change + sparkline + "powered by" attribution. The
 * route uses a custom layout (siblings of layout.tsx via app router)
 * so the global Navbar / Footer don't render.
 *
 * Recommended iframe shape:
 *   <iframe src="https://ratesengine.net/embed/asset/USDC"
 *           width="320" height="160" frameborder="0"
 *           sandbox="allow-scripts"></iframe>
 *
 * SEO is opted out (robots: noindex) — these are widgets, not
 * destination pages.
 */
export default async function EmbedAssetPage({ params }: { params: Params }) {
  const { slug } = await params;
  const coin = await fetchCoin(slug);

  if (!coin) {
    return (
      <div className="flex h-full min-h-32 items-center justify-center px-3 py-3 text-sm text-slate-500">
        <span>No data for {slug}</span>
      </div>
    );
  }

  const priceNum = coin.price_usd ? Number(coin.price_usd) : null;
  const changeNum = coin.change_24h_pct ? Number(coin.change_24h_pct) : null;
  const points = coin.price_history_24h ?? [];

  return (
    <div className="flex h-full min-h-32 flex-col gap-2 bg-white px-4 py-3 text-slate-900 dark:bg-slate-900 dark:text-slate-100">
      <div className="flex items-baseline justify-between gap-2">
        <div className="flex items-baseline gap-2">
          <span className="text-base font-semibold tracking-tight">
            {coin.code}
          </span>
          <span className="font-mono text-[10px] text-slate-500">
            Stellar
          </span>
        </div>
        <a
          href={`https://ratesengine.net/assets/${slug}`}
          target="_blank"
          rel="noreferrer noopener"
          className="text-[10px] text-slate-400 hover:text-brand-600"
        >
          rates&shy;engine.net ↗
        </a>
      </div>
      <div className="flex items-baseline gap-3">
        <span className="font-mono text-2xl tabular-nums">
          {priceNum != null ? formatPrice(priceNum) : '—'}
        </span>
        {changeNum != null && Number.isFinite(changeNum) && (
          <span
            className={`rounded px-1.5 py-0.5 font-mono text-xs tabular-nums ${
              changeNum > 0
                ? 'bg-emerald-50 text-emerald-700 dark:bg-emerald-950/40 dark:text-emerald-300'
                : changeNum < 0
                  ? 'bg-rose-50 text-rose-700 dark:bg-rose-950/40 dark:text-rose-300'
                  : 'bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-400'
            }`}
          >
            {changeNum > 0 ? '+' : ''}
            {changeNum.toFixed(2)}% 24h
          </span>
        )}
      </div>
      {points.length > 0 && <Sparkline points={points} />}
      <div className="mt-auto flex items-center justify-between text-[10px] text-slate-400">
        <span>Powered by Rates Engine</span>
        {coin.volume_24h_usd && (
          <span className="font-mono tabular-nums">
            ${formatCompact(Number(coin.volume_24h_usd))} 24h vol
          </span>
        )}
      </div>
    </div>
  );
}

function Sparkline({ points }: { points: { p?: string | null }[] }) {
  const prices = points
    .map((p) => Number(p.p))
    .filter((n) => Number.isFinite(n) && n > 0);
  if (prices.length === 0) return null;
  const min = Math.min(...prices);
  const max = Math.max(...prices);
  const range = max - min || max * 0.01;
  const w = 280;
  const h = 32;
  const xStep = points.length > 1 ? w / (points.length - 1) : 0;
  const path = points
    .map((p, i) => {
      const n = Number(p.p);
      if (!Number.isFinite(n)) return null;
      const x = i * xStep;
      const y = h - ((n - min) / range) * h;
      return `${i === 0 ? 'M' : 'L'} ${x.toFixed(1)} ${y.toFixed(1)}`;
    })
    .filter(Boolean)
    .join(' ');
  const trendUp = prices[prices.length - 1]! >= prices[0]!;
  return (
    <svg
      viewBox={`0 0 ${w} ${h}`}
      preserveAspectRatio="none"
      className="h-8 w-full"
      aria-label="24-hour price sparkline"
    >
      <path
        d={path}
        fill="none"
        strokeWidth="1.5"
        className={trendUp ? 'stroke-emerald-500' : 'stroke-rose-500'}
      />
    </svg>
  );
}

function formatPrice(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (n >= 1) return `$${n.toFixed(n >= 100 ? 2 : 4)}`;
  if (n >= 0.001) return `$${n.toFixed(6)}`;
  if (n > 0) return `$${n.toExponential(3)}`;
  return '—';
}

function formatCompact(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 1_000) return `${(n / 1_000).toFixed(0)}k`;
  return n.toFixed(2);
}
