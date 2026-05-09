import type { Metadata } from 'next';
import Link from 'next/link';
import { ArrowLeft, ArrowLeftRight } from 'lucide-react';

import { SITE_OG_IMAGES } from '@/lib/seo';
import { ConvertPair } from './ConvertPair';

const API_BASE_URL =
  process.env.NEXT_PUBLIC_API_BASE_URL ?? 'https://api.ratesengine.net';

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

// Fallback majors so a brand-new build with no upstream still
// produces a meaningful matrix. Same set as /currencies/[ticker]'s
// fallback so the two routes stay aligned.
const FALLBACK_TICKERS = [
  'USD', 'EUR', 'GBP', 'JPY', 'CHF', 'CAD', 'AUD', 'CNY',
  'INR', 'BRL', 'MXN', 'ZAR', 'NZD', 'SGD', 'HKD', 'SEK',
];

// Common amounts to render as static "X = Y" snippets for SEO body
// content. Picks naturally-queried ladder values across orders of
// magnitude — Google search-volume tools show "1 X to Y", "100 X
// to Y", "1000 X to Y" all rank as distinct queries with non-trivial
// volume even for the same currency pair.
const SNIPPET_AMOUNTS = [1, 10, 100, 1000, 10000];

type Params = Promise<{ from: string; to: string }>;

interface CurrencyDetail {
  ticker: string;
  name: string;
  rate_usd: number;
  inverse_usd: number;
  cross_rates: Record<string, number>;
  published_at?: string;
  source?: string;
}

interface CurrencyListEntry {
  ticker: string;
  name?: string;
}

async function fetchTickers(): Promise<string[]> {
  if (isCIStub) return FALLBACK_TICKERS;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/currencies`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as {
      data: { currencies?: CurrencyListEntry[] };
    };
    const tickers = env.data?.currencies?.map((c) => c.ticker).filter(Boolean) ?? [];
    return tickers.length > 0 ? tickers : FALLBACK_TICKERS;
  } catch {
    return FALLBACK_TICKERS;
  }
}

// Top-20 fiat majors that hub all the long-tail conversions. These
// are the currencies people search "from" and "to" — picking them
// as the hub axis covers >99% of organic search volume per
// SimilarWeb 2024 forex-traffic ranking. Adding more names doesn't
// move the SEO needle but does inflate the deploy file count.
const HUB_TICKERS = [
  'USD', 'EUR', 'GBP', 'JPY', 'CHF', 'CAD', 'AUD', 'CNY',
  'INR', 'BRL', 'MXN', 'ZAR', 'NZD', 'SGD', 'HKD', 'SEK',
  'NOK', 'KRW', 'TRY', 'PLN',
];

// Hub-and-spoke: top-20 majors × all-110 currencies, both directions.
// 20 × 109 × 2 = ~4,360 pages — well inside Cloudflare Pages'
// 20,000-file/deploy ceiling once Next.js's .html/.meta/.rsc trio
// per route is counted. The full N×N (12k pages, ~36k files) blows
// the cap; this design captures the SEO surface that matters:
// every "X to a major" + "a major to X" query has a static page.
// Long-tail X→Y (both non-hub) routes resolve via the dynamic
// fallback Next.js falls back to.
export async function generateStaticParams() {
  const tickers = await fetchTickers();
  const out: { from: string; to: string }[] = [];
  const hubSet = new Set(HUB_TICKERS);

  // Pass 1: every hub × every ticker (forward).
  for (const from of HUB_TICKERS) {
    for (const to of tickers) {
      if (from === to) continue;
      out.push({ from, to });
    }
  }
  // Pass 2: every ticker → every hub (reverse). Skip pairs we
  // already produced (hub × hub is in pass 1).
  for (const from of tickers) {
    if (hubSet.has(from)) continue;
    for (const to of HUB_TICKERS) {
      if (from === to) continue;
      out.push({ from, to });
    }
  }
  return out;
}

async function fetchDetail(from: string): Promise<CurrencyDetail | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/currencies/${from.toUpperCase()}`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return null;
    const env = (await res.json()) as { data: CurrencyDetail };
    return env.data ?? null;
  } catch {
    return null;
  }
}

export async function generateMetadata({ params }: { params: Params }): Promise<Metadata> {
  const { from, to } = await params;
  const f = from.toUpperCase();
  const t = to.toUpperCase();
  const detail = await fetchDetail(f);
  const rate = detail?.cross_rates?.[t];
  const ratePart = rate != null
    ? ` 1 ${f} = ${formatRateForMeta(rate)} ${t}.`
    : '';
  return {
    title: `${f} to ${t} — live exchange rate + currency converter`,
    description: `Convert ${f} to ${t} at the live mid-market rate.${ratePart} Real-time forex rate, interactive converter, and ${f}/${t} cross-rates at common amounts (1, 10, 100, 1000, 10000).`,
    alternates: {
      canonical: `https://ratesengine.net/convert/${f}/${t}`,
    },
    openGraph: {
      title: `${f} to ${t} converter`,
      description: rate != null
        ? `1 ${f} = ${formatRateForMeta(rate)} ${t} — live forex rate.`
        : `Live ${f} to ${t} forex rate + converter.`,
      url: `https://ratesengine.net/convert/${f}/${t}`,
      type: 'website',
      images: SITE_OG_IMAGES,
    },
  };
}

export default async function ConvertPage({ params }: { params: Params }) {
  const { from, to } = await params;
  const f = from.toUpperCase();
  const t = to.toUpperCase();

  const detail = await fetchDetail(f);
  const rate = detail?.cross_rates?.[t] ?? null;
  const inverse = rate != null && rate > 0 ? 1 / rate : null;

  return (
    <div className="mx-auto max-w-4xl space-y-6 px-6 py-8">
      <Link
        href={`/currencies/${f}`}
        className="inline-flex items-center gap-1.5 text-sm text-slate-600 hover:text-brand-600 dark:text-slate-400"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        {f} overview
      </Link>

      <header className="space-y-3 border-b border-slate-200 pb-5 dark:border-slate-800">
        <h1 className="text-3xl font-semibold tracking-tight">
          {f} to {t}
          {detail?.name && (
            <span className="ml-3 text-base font-normal text-slate-500">
              {detail.name} → {t}
            </span>
          )}
        </h1>
        {rate != null ? (
          <p className="text-2xl font-mono tabular-nums text-slate-900 dark:text-slate-100">
            1 {f} = {formatRate(rate)} {t}
          </p>
        ) : (
          <p className="text-sm text-slate-500">Rate currently unavailable.</p>
        )}
        {inverse != null && (
          <p className="text-sm font-mono tabular-nums text-slate-600 dark:text-slate-400">
            1 {t} = {formatRate(inverse)} {f}
          </p>
        )}
        {detail?.source && (
          <p className="text-xs text-slate-500">
            Source: {detail.source}
            {detail.published_at && ` · published ${formatDate(detail.published_at)}`}
          </p>
        )}
      </header>

      <ConvertPair from={f} to={t} initialRate={rate} initialInverse={inverse} />

      {rate != null && (
        <section className="rounded-xl border border-slate-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900">
          <h2 className="mb-4 text-lg font-semibold tracking-tight">
            {f} to {t} at common amounts
          </h2>
          <div className="grid grid-cols-1 gap-2 sm:grid-cols-2">
            {SNIPPET_AMOUNTS.map((amt) => (
              <div
                key={amt}
                className="flex items-baseline justify-between rounded-md bg-slate-50 px-3 py-2 dark:bg-slate-800/50"
              >
                <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                  {amt.toLocaleString()} {f}
                </span>
                <span className="font-mono tabular-nums font-medium text-slate-900 dark:text-slate-100">
                  {formatRate(amt * rate)} {t}
                </span>
              </div>
            ))}
          </div>
          <p className="mt-4 text-xs text-slate-500">
            All values calculated at the current mid-market rate of 1 {f} = {formatRate(rate)} {t}.
            Rates update on each {detail?.source ?? 'forex source'} refresh tick.
          </p>
        </section>
      )}

      <section className="flex flex-wrap gap-2 text-sm">
        <Link
          href={`/convert/${t}/${f}`}
          className="inline-flex items-center gap-1.5 rounded-md border border-slate-200 bg-white px-3 py-2 text-slate-700 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300"
        >
          <ArrowLeftRight className="h-3.5 w-3.5" />
          Convert {t} to {f} instead
        </Link>
        <Link
          href={`/currencies/${f}`}
          className="inline-flex items-center rounded-md border border-slate-200 bg-white px-3 py-2 text-slate-700 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300"
        >
          {f} cross-rates
        </Link>
        <Link
          href={`/currencies/${t}`}
          className="inline-flex items-center rounded-md border border-slate-200 bg-white px-3 py-2 text-slate-700 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300"
        >
          {t} cross-rates
        </Link>
      </section>
    </div>
  );
}

function formatRate(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (Math.abs(n) >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (Math.abs(n) >= 1) return n.toFixed(4);
  if (Math.abs(n) >= 0.01) return n.toFixed(6);
  return n.toFixed(8);
}

function formatRateForMeta(n: number): string {
  if (!Number.isFinite(n)) return '—';
  if (Math.abs(n) >= 100) return n.toFixed(2);
  if (Math.abs(n) >= 1) return n.toFixed(4);
  return n.toFixed(6);
}

function formatDate(iso: string): string {
  try {
    return new Date(iso).toLocaleDateString(undefined, {
      year: 'numeric', month: 'short', day: 'numeric',
    });
  } catch {
    return iso;
  }
}
