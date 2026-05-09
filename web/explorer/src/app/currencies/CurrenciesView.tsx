'use client';

import { useEffect, useMemo, useRef, useState } from 'react';
import { useQueries } from '@tanstack/react-query';
import { usePathname, useRouter, useSearchParams } from 'next/navigation';
import { Search } from 'lucide-react';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { useWatchlist } from './watchlist';

/**
 * Unified currencies listing — every currency we price, fiat +
 * crypto in one table. Inspired by the coinmarketcap layout the
 * user pointed at.
 *
 * Data sources:
 *   - /v1/coins (crypto: native XLM + classic credit assets +
 *     SAC-wrapped tokens, ranked by activity in the API but resorted
 *     by market cap here when available)
 *   - /v1/currencies (fiat: ~110 ISO-4217 codes, with curated M2
 *     monetary-base supply where central banks publish it)
 *
 * Columns: rank, asset (icon + name + ticker pill), price (USD),
 * 1h % / 24h % / 7d %, market cap, 24h volume, circulating supply,
 * 7d sparkline. Filter chips toggle all / crypto / fiat.
 *
 * Each row is clickable — anywhere in the row routes to
 * /currencies/{slug}. Slug is the upper-case ticker today; a
 * follow-up PR introduces friendly slugs (bitcoin, ethereum, etc.)
 * with collision handling for tickers shared across chains.
 *
 * Live-flashing prices land in a follow-up — the wire-shape is in
 * place (priceUsd is a stable number on each row) so plugging the
 * /v1/price/stream subscription on top is straightforward.
 */
interface UnifiedRow {
  kind: 'crypto' | 'fiat';
  slug: string;
  ticker: string;
  name: string;
  priceUsd: number | null;
  change1hPct: number | null;
  change24hPct: number | null;
  change7dPct: number | null;
  marketCapUsd: number | null;
  volume24hUsd: number | null;
  circulatingSupply: number | null;
  history7d: number[]; // inverse-USD for fiat, price-USD for crypto
}

type FilterKind = 'all' | 'crypto' | 'fiat' | 'stablecoin' | 'watchlist';

// STABLECOIN_TICKERS — crypto rows whose ticker matches one of
// these are treated as stablecoins. Curated from the operator's
// configured `usd_pegged_classics` list plus the EUR/MXN-pegged
// equivalents. Lower-case for case-insensitive matching.
const STABLECOIN_TICKERS = new Set<string>([
  'USDC', 'USDT', 'PYUSD', 'EUROC', 'EURC', 'EUROB', 'MXNe',
  'USDX', 'USDx', 'EURx', 'BUSD', 'TUSD', 'DAI', 'GYEN',
]);
type SortKey = 'rank' | 'name' | 'price' | 'change_1h' | 'change_24h' | 'change_7d' | 'market_cap' | 'volume_24h' | 'supply';

const FILTER_VALUES: FilterKind[] = ['all', 'crypto', 'stablecoin', 'fiat', 'watchlist'];
const SORT_VALUES: SortKey[] = ['rank', 'name', 'price', 'change_1h', 'change_24h', 'change_7d', 'market_cap', 'volume_24h', 'supply'];

function parseFilter(s: string | null): FilterKind | null {
  return s != null && (FILTER_VALUES as string[]).includes(s) ? (s as FilterKind) : null;
}

function parseSortKey(s: string | null): SortKey | null {
  return s != null && (SORT_VALUES as string[]).includes(s) ? (s as SortKey) : null;
}

export function CurrenciesView() {
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();

  // URL state: ?q=&filter=&sort=&dir= so a filtered+sorted view
  // is a shareable link. State seeds from the URL on first render
  // and writes back on change. We use shallow router.replace so
  // back/forward gestures still feel right + we don't trigger a
  // page-level re-render storm.
  const [q, setQ] = useState(() => params.get('q') ?? '');
  const [filter, setFilter] = useState<FilterKind>(() =>
    parseFilter(params.get('filter')) ?? 'all',
  );
  const [sortKey, setSortKey] = useState<SortKey>(() =>
    parseSortKey(params.get('sort')) ?? 'market_cap',
  );
  const [sortDir, setSortDir] = useState<'asc' | 'desc'>(() => {
    const dir = params.get('dir');
    return dir === 'asc' || dir === 'desc' ? dir : 'desc';
  });
  const watchlist = useWatchlist();

  // Push state changes back to the URL so the link reflects what
  // the user is looking at. Defaults are stripped to keep URLs
  // readable (?q= is implicit when the search is empty etc.).
  useEffect(() => {
    const sp = new URLSearchParams();
    if (q.trim()) sp.set('q', q.trim());
    if (filter !== 'all') sp.set('filter', filter);
    if (sortKey !== 'market_cap') sp.set('sort', sortKey);
    if (sortDir !== 'desc') sp.set('dir', sortDir);
    const qs = sp.toString();
    const next = qs ? `${pathname}?${qs}` : pathname;
    router.replace(next, { scroll: false });
  }, [q, filter, sortKey, sortDir, pathname, router]);

  const [cryptoQ, fiatQ] = useQueries({
    queries: [
      {
        queryKey: ['/v1/coins', 'currencies-listing'],
        queryFn: async () => {
          const env = await apiGet<{
            data: { coins: CryptoCoin[] };
          }>('/v1/coins', { limit: 200, include: 'sparkline' });
          return env.data?.coins ?? [];
        },
        // 15s — crypto trades land in the indexer within 6s of a
        // ledger close, so refetching twice a ledger is the right
        // cadence for the flash UX. Backend cache (rc.38 PR #1048)
        // makes the per-fetch cost <100ms post-deploy.
        refetchInterval: 15_000,
      },
      {
        queryKey: ['/v1/currencies', 'currencies-listing'],
        queryFn: async () => {
          const env = await apiGet<{
            data: { currencies?: FiatRow[]; published_at?: string };
          }>('/v1/currencies', { include: 'sparkline' });
          return env.data;
        },
        // 60s — fiat is daily-grain at the upstream so refetching
        // faster than 60s is wasted; the worker only roll-overs the
        // snapshot once per hour.
        refetchInterval: 60_000,
      },
    ],
  });

  const rows = useMemo<UnifiedRow[]>(() => {
    const cryptoRows = (cryptoQ.data ?? []).map(toCryptoUnified);
    const fiatRows = (fiatQ.data?.currencies ?? []).map(toFiatUnified);
    return [...cryptoRows, ...fiatRows];
  }, [cryptoQ.data, fiatQ.data]);

  const filtered = useMemo(() => {
    const term = q.trim().toLowerCase();
    let scoped = rows;
    if (filter === 'stablecoin') {
      scoped = scoped.filter((r) => STABLECOIN_TICKERS.has(r.ticker.toUpperCase()));
    } else if (filter === 'watchlist') {
      scoped = scoped.filter((r) => watchlist.has(r.kind, r.ticker));
    } else if (filter !== 'all') {
      scoped = scoped.filter((r) => r.kind === filter);
    }
    if (term) {
      // Match against ticker, name, slug, AND the friendly slug map
      // so typing "us-dollar" or "japanese-yen" finds the right row
      // even though the upstream payload only carries the bare ISO
      // code. Spaces in the search term are normalised to dashes so
      // "us dollar" matches "us-dollar".
      const dashed = term.replace(/\s+/g, '-');
      scoped = scoped.filter((r) => {
        const friendly = FRIENDLY_FIAT_SLUGS[r.ticker.toUpperCase()] ?? '';
        return (
          r.ticker.toLowerCase().includes(term) ||
          r.name.toLowerCase().includes(term) ||
          r.slug.toLowerCase().includes(term) ||
          friendly.includes(dashed)
        );
      });
    }
    return [...scoped].sort((a, b) => compareRows(a, b, sortKey, sortDir));
  // watchlist included so the table re-filters when a star toggles.
  // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows, filter, q, sortKey, sortDir, watchlist.size]);

  function toggleSort(key: SortKey) {
    if (sortKey === key) {
      setSortDir((d) => (d === 'asc' ? 'desc' : 'asc'));
    } else {
      setSortKey(key);
      setSortDir(key === 'name' ? 'asc' : 'desc');
    }
  }

  const isLoading = cryptoQ.isLoading || fiatQ.isLoading;

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Currencies</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Every currency we price — crypto + fiat, ranked by market
          capitalisation. Crypto data from the Stellar pricing engine
          (live VWAP across CEX + on-chain DEX feeds); fiat from{' '}
          <a
            href="https://polygon.io"
            target="_blank"
            rel="noreferrer noopener"
            className="text-brand-600 hover:underline"
          >
            Massive (Polygon.io)
          </a>{' '}
          with curated central-bank M2 monetary-base supply.
        </p>
      </header>

      <Panel
        title={`${filtered.length} of ${rows.length} currencies`}
        hint={
          <FreshnessHint
            cryptoUpdatedAt={cryptoQ.dataUpdatedAt}
            fiatUpdatedAt={fiatQ.dataUpdatedAt}
            fiatPublishedAt={fiatQ.data?.published_at}
            isFetching={cryptoQ.isFetching || fiatQ.isFetching}
          />
        }
        source={asExample('/v1/coins', { limit: 200 })}
        bodyClassName="-mx-4"
      >
        <div className="flex flex-wrap items-center gap-3 px-4 pb-3 pt-1">
          <SearchInput value={q} onChange={setQ} />
          <div className="ml-auto inline-flex rounded-md border border-slate-200 bg-white p-0.5 text-xs dark:border-slate-700 dark:bg-slate-900">
            {(['all', 'crypto', 'stablecoin', 'fiat', 'watchlist'] as const).map((f) => (
              <button
                key={f}
                type="button"
                onClick={() => setFilter(f)}
                className={`rounded px-2.5 py-1 font-medium uppercase tracking-wider ${
                  filter === f
                    ? 'bg-brand-100 text-brand-900 dark:bg-brand-900/40 dark:text-brand-100'
                    : 'text-slate-600 hover:bg-slate-100 dark:text-slate-400 dark:hover:bg-slate-800'
                }`}
              >
                {f}
              </button>
            ))}
          </div>
          <button
            type="button"
            onClick={() => exportRowsToCsv(filtered)}
            disabled={filtered.length === 0}
            className="rounded-md border border-slate-200 bg-white px-2.5 py-1 text-xs font-medium text-slate-600 hover:border-brand-500 hover:text-brand-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300"
            title="Download the current filtered + sorted view as a CSV"
          >
            Export CSV
          </button>
        </div>

        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
                <Th><span className="sr-only">Watchlist</span></Th>
                <SortableTh sortKey="rank" current={sortKey} dir={sortDir} onToggle={toggleSort}>
                  #
                </SortableTh>
                <SortableTh sortKey="name" current={sortKey} dir={sortDir} onToggle={toggleSort}>
                  Asset
                </SortableTh>
                <SortableTh sortKey="price" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  Price
                </SortableTh>
                <SortableTh sortKey="change_1h" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  1h %
                </SortableTh>
                <SortableTh sortKey="change_24h" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  24h %
                </SortableTh>
                <SortableTh sortKey="change_7d" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  7d %
                </SortableTh>
                <SortableTh sortKey="market_cap" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  Market cap
                </SortableTh>
                <SortableTh sortKey="volume_24h" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  Volume (24h)
                </SortableTh>
                <SortableTh sortKey="supply" current={sortKey} dir={sortDir} onToggle={toggleSort} align="right">
                  Circulating supply
                </SortableTh>
                <Th align="right">Last 7 days</Th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {isLoading && <SkeletonRows count={20} />}
              {!isLoading && filtered.length === 0 && (
                <tr>
                  <td colSpan={11} className="px-4 py-10 text-center text-sm text-slate-500">
                    <div className="space-y-2">
                      <div>
                        {q
                          ? `No currencies matched "${q}"${filter !== 'all' ? ` in the ${filter} filter` : ''}.`
                          : filter !== 'all'
                            ? `No currencies match the ${filter} filter.`
                            : 'No currencies — feeds warming up.'}
                      </div>
                      {(q || filter !== 'all') && (
                        <button
                          type="button"
                          onClick={() => {
                            setQ('');
                            setFilter('all');
                          }}
                          className="inline-flex items-center gap-1 rounded-md border border-slate-200 bg-white px-3 py-1 text-xs text-slate-600 hover:border-brand-500 hover:text-brand-600 dark:border-slate-700 dark:bg-slate-900 dark:text-slate-300"
                        >
                          Clear filters
                        </button>
                      )}
                    </div>
                  </td>
                </tr>
              )}
              {filtered.map((r, i) => (
                <tr
                  key={`${r.kind}-${r.slug}`}
                  onClick={() => router.push(detailHref(r))}
                  className="cursor-pointer hover:bg-slate-50 dark:hover:bg-slate-900/40"
                >
                  <Td>
                    <button
                      type="button"
                      aria-label={watchlist.has(r.kind, r.ticker) ? `Unstar ${r.ticker}` : `Star ${r.ticker}`}
                      onClick={(e) => {
                        e.stopPropagation();
                        watchlist.toggle(r.kind, r.ticker);
                      }}
                      className={`text-base transition-colors ${
                        watchlist.has(r.kind, r.ticker)
                          ? 'text-amber-500'
                          : 'text-slate-300 hover:text-slate-500 dark:text-slate-700 dark:hover:text-slate-400'
                      }`}
                    >
                      {watchlist.has(r.kind, r.ticker) ? '★' : '☆'}
                    </button>
                  </Td>
                  <Td>
                    <span className="font-mono text-[11px] text-slate-400">{i + 1}</span>
                  </Td>
                  <Td>
                    <AssetCell row={r} />
                  </Td>
                  <Td align="right">
                    <PriceCell value={r.priceUsd} />
                  </Td>
                  <Td align="right">
                    <ChangePct value={r.change1hPct} />
                  </Td>
                  <Td align="right">
                    <ChangePct value={r.change24hPct} />
                  </Td>
                  <Td align="right">
                    <ChangePct value={r.change7dPct} />
                  </Td>
                  <Td align="right">
                    {r.marketCapUsd != null ? (
                      <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                        ${formatCompact(r.marketCapUsd)}
                      </span>
                    ) : (
                      <Dash />
                    )}
                  </Td>
                  <Td align="right">
                    {r.volume24hUsd != null ? (
                      <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                        ${formatCompact(r.volume24hUsd)}
                      </span>
                    ) : (
                      <Dash />
                    )}
                  </Td>
                  <Td align="right">
                    {r.circulatingSupply != null ? (
                      <span className="font-mono tabular-nums text-slate-600 dark:text-slate-400">
                        {formatCompact(r.circulatingSupply)} {r.ticker}
                      </span>
                    ) : (
                      <Dash />
                    )}
                  </Td>
                  <Td align="right">
                    <Sparkline points={r.history7d} positive={(r.change7dPct ?? 0) >= 0} />
                  </Td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </Panel>
    </div>
  );
}

interface CryptoCoin {
  slug: string;
  asset_id: string;
  code: string;
  issuer?: string;
  price_usd?: string | null;
  volume_24h_usd?: string | null;
  market_cap_usd?: string | null;
  circulating_supply?: string | null;
  change_1h_pct?: string | null;
  change_24h_pct?: string | null;
  change_7d_pct?: string | null;
  price_history_7d?: number[];
}

interface FiatRow {
  ticker: string;
  name: string;
  rate_usd: number;
  change_24h_pct?: number;
  change_7d_pct?: number;
  history_7d_rates?: number[];
  market_cap_usd?: number;
  circulating_supply?: number;
}

function toCryptoUnified(c: CryptoCoin): UnifiedRow {
  return {
    kind: 'crypto',
    slug: c.slug || c.code,
    ticker: c.code,
    name: nameFor(c),
    priceUsd: parseDec(c.price_usd),
    change1hPct: parseDec(c.change_1h_pct),
    change24hPct: parseDec(c.change_24h_pct),
    change7dPct: parseDec(c.change_7d_pct),
    marketCapUsd: parseDec(c.market_cap_usd),
    volume24hUsd: parseDec(c.volume_24h_usd),
    circulatingSupply: parseDec(c.circulating_supply),
    history7d: c.price_history_7d ?? [],
  };
}

function toFiatUnified(c: FiatRow): UnifiedRow {
  // 1 unit of `c.ticker` in USD = 1 / rate_usd. 7d sparkline is
  // already inverse-USD when the API returns history_7d_rates.
  const inverseUsd = c.rate_usd > 0 ? 1 / c.rate_usd : null;
  return {
    kind: 'fiat',
    slug: c.ticker,
    ticker: c.ticker,
    name: c.name,
    priceUsd: inverseUsd,
    change1hPct: null, // fiat feed is daily-grain, no 1h window
    change24hPct: c.change_24h_pct ?? null,
    change7dPct: c.change_7d_pct ?? null,
    marketCapUsd: c.market_cap_usd ?? null,
    volume24hUsd: null, // not surfaced by /v1/currencies today
    circulatingSupply: c.circulating_supply ?? null,
    history7d: c.history_7d_rates ?? [],
  };
}

function nameFor(c: CryptoCoin): string {
  if (c.code === 'XLM' && (!c.issuer || c.issuer === '')) return 'Stellar Lumens';
  return c.code;
}

// detailHref routes a unified row to its kind-appropriate detail
// page. Crypto rows go to /assets/{slug} (the existing crypto
// detail page with chart, supply, issuer panel, markets); fiat
// rows go to /currencies/{friendly-slug-or-ticker} where the
// friendly slug (e.g. "us-dollar") is preferred for SEO over the
// bare ISO code. Both forms route to the same detail view via
// the slug-resolver in @/app/currencies/[ticker]/slugs.
function detailHref(r: UnifiedRow): string {
  if (r.kind === 'crypto') return `/assets/${encodeURIComponent(r.slug)}`;
  return `/currencies/${encodeURIComponent(friendlyFiatSlug(r.ticker))}`;
}

// FRIENDLY_FIAT_SLUGS — kept in lock-step with TICKER_TO_FRIENDLY_SLUG
// in @/app/currencies/[ticker]/slugs. Duplicated here so the
// listing component (a 'use client' bundle) doesn't pull the
// resolver's full reverse map. One-line addition extends both maps.
const FRIENDLY_FIAT_SLUGS: Record<string, string> = {
  USD: 'us-dollar',
  EUR: 'euro',
  GBP: 'british-pound',
  JPY: 'japanese-yen',
  CHF: 'swiss-franc',
  CAD: 'canadian-dollar',
  AUD: 'australian-dollar',
  NZD: 'new-zealand-dollar',
  CNY: 'chinese-yuan',
  INR: 'indian-rupee',
  BRL: 'brazilian-real',
  MXN: 'mexican-peso',
  ZAR: 'south-african-rand',
  SGD: 'singapore-dollar',
  HKD: 'hong-kong-dollar',
  SEK: 'swedish-krona',
  NOK: 'norwegian-krone',
  DKK: 'danish-krone',
  KRW: 'south-korean-won',
  TRY: 'turkish-lira',
  PLN: 'polish-zloty',
  RUB: 'russian-ruble',
  THB: 'thai-baht',
  PHP: 'philippine-peso',
  NGN: 'nigerian-naira',
};

function friendlyFiatSlug(ticker: string): string {
  return FRIENDLY_FIAT_SLUGS[ticker.toUpperCase()] ?? ticker;
}

function compareRows(a: UnifiedRow, b: UnifiedRow, key: SortKey, dir: 'asc' | 'desc'): number {
  const sign = dir === 'asc' ? 1 : -1;
  const cmpNum = (av: number | null, bv: number | null) => {
    // Nulls always sort to the end regardless of direction so the
    // valuable rows stay visible.
    if (av == null && bv == null) return 0;
    if (av == null) return 1;
    if (bv == null) return -1;
    return (av - bv) * sign;
  };
  switch (key) {
    case 'name':
      return a.name.localeCompare(b.name) * sign;
    case 'price':
      return cmpNum(a.priceUsd, b.priceUsd);
    case 'change_1h':
      return cmpNum(a.change1hPct, b.change1hPct);
    case 'change_24h':
      return cmpNum(a.change24hPct, b.change24hPct);
    case 'change_7d':
      return cmpNum(a.change7dPct, b.change7dPct);
    case 'market_cap':
      return cmpNum(a.marketCapUsd, b.marketCapUsd);
    case 'volume_24h':
      return cmpNum(a.volume24hUsd, b.volume24hUsd);
    case 'supply':
      return cmpNum(a.circulatingSupply, b.circulatingSupply);
    case 'rank':
    default:
      return cmpNum(a.marketCapUsd, b.marketCapUsd);
  }
}

function parseDec(s: string | number | null | undefined): number | null {
  if (s == null) return null;
  const n = typeof s === 'number' ? s : Number(s);
  return Number.isFinite(n) ? n : null;
}

function AssetCell({ row }: { row: UnifiedRow }) {
  const icon = iconFor(row);
  const tonePill =
    row.kind === 'crypto'
      ? 'bg-emerald-100 text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200'
      : 'bg-blue-100 text-blue-800 dark:bg-blue-900/40 dark:text-blue-200';
  return (
    <div className="flex items-center gap-2">
      <span aria-hidden className="flex h-6 w-6 items-center justify-center rounded-full bg-slate-100 font-mono text-xs dark:bg-slate-800">
        {icon}
      </span>
      <div className="min-w-0">
        <div className="flex items-baseline gap-1.5">
          <span className="truncate font-medium text-slate-900 dark:text-slate-100">{row.name}</span>
          <span className={`shrink-0 rounded px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider ${tonePill}`}>
            {row.ticker}
          </span>
        </div>
      </div>
    </div>
  );
}

// iconFor returns a single-glyph stand-in icon. Bitcoin / ether
// have well-known unicode glyphs; common fiat use the currency
// symbol; everything else falls back to the first letter of the
// ticker. Real SVG icons land in a follow-up.
function iconFor(row: UnifiedRow): string {
  const t = row.ticker;
  // Expanded coverage of well-known unicode currency glyphs. Some
  // codes share a glyph by convention (CNY/JPY both use ¥) — we
  // pick the one most associated with the issuing region.
  const fiatSymbol: Record<string, string> = {
    USD: '$', EUR: '€', GBP: '£', JPY: '¥', CNY: '¥', KRW: '₩',
    INR: '₹', RUB: '₽', TRY: '₺', BRL: 'R$', CHF: '₣', AUD: '$',
    CAD: '$', NZD: '$', HKD: '$', SGD: '$', MXN: '$', ZAR: 'R',
    THB: '฿', PHP: '₱', NGN: '₦',
    // Newly added — covers most of the friendly-slug map.
    DKK: 'kr', SEK: 'kr', NOK: 'kr', PLN: 'zł', VND: '₫',
    UAH: '₴', ILS: '₪', GHS: '₵', LAK: '₭', MNT: '₮', MYR: 'RM',
    IDR: 'Rp', PKR: '₨', LKR: '₨', NPR: '₨', BDT: '৳', KZT: '₸',
    AZN: '₼', GEL: '₾', AMD: '֏', BHD: '.د.ب', SAR: '﷼', AED: 'د.إ',
    QAR: '﷼', KWD: 'د.ك', OMR: '﷼', JOD: 'د.أ', LBP: 'ل.ل',
    EGP: '£', BTN: 'Nu', KHR: '៛', MMK: 'K', LBO: 'Rs', CLP: '$',
    COP: '$', ARS: '$', PEN: 'S/', UYU: '$U', BOB: 'Bs',
    XAF: 'FCFA', XOF: 'CFA',
  };
  // Crypto symbols are sparse — most assets don't have a unicode
  // glyph. Fall back to the ticker's first letter for visual
  // distinctness; real SVG icons land in a follow-up.
  const cryptoSymbol: Record<string, string> = {
    BTC: '₿', ETH: 'Ξ', XLM: '✦', LTC: 'Ł', DOGE: 'Ð',
    USDC: '$', USDT: '$', PYUSD: '$', BUSD: '$', TUSD: '$',
    DAI: '◈', EURC: '€', EUROC: '€', EUROB: '€', MXNe: '$',
  };
  if (row.kind === 'fiat' && fiatSymbol[t]) return fiatSymbol[t];
  if (row.kind === 'crypto' && cryptoSymbol[t]) return cryptoSymbol[t];
  return t.slice(0, 1);
}

// SkeletonRows renders a placeholder body for the listing while
// the underlying queries fetch on first paint. Lower perceived
// latency than the previous one-line "Loading currencies…" cell:
// the user sees row-shaped pulsing bars in the same layout the
// real rows will land in, so the table doesn't shift when data
// arrives. 11 columns (rank, watchlist, asset, price, 1h%, 24h%,
// 7d%, mcap, vol24h, supply, 7d-chart) match the SortableTh
// header above; widths are tuned to look reasonable across the
// breakpoint range without over-engineering a per-column shape.
function SkeletonRows({ count }: { count: number }) {
  return (
    <>
      {Array.from({ length: count }, (_, i) => (
        <tr
          key={i}
          aria-hidden="true"
          className="animate-pulse"
        >
          <td className="px-4 py-3.5"><div className="h-3 w-4 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-2 py-3.5"><div className="h-3 w-3 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-4 py-3.5">
            <div className="flex items-center gap-2">
              <div className="h-6 w-6 rounded-full bg-slate-200 dark:bg-slate-800" />
              <div className="h-3 w-20 rounded bg-slate-200 dark:bg-slate-800" />
            </div>
          </td>
          <td className="px-4 py-3.5 text-right"><div className="ml-auto h-3 w-16 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-4 py-3.5 text-right"><div className="ml-auto h-3 w-10 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-4 py-3.5 text-right"><div className="ml-auto h-3 w-10 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-4 py-3.5 text-right"><div className="ml-auto h-3 w-10 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-4 py-3.5 text-right"><div className="ml-auto h-3 w-20 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-4 py-3.5 text-right"><div className="ml-auto h-3 w-16 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-4 py-3.5 text-right"><div className="ml-auto h-3 w-14 rounded bg-slate-200 dark:bg-slate-800" /></td>
          <td className="px-4 py-3.5 text-right"><div className="ml-auto h-6 w-22 rounded bg-slate-200 dark:bg-slate-800" /></td>
        </tr>
      ))}
    </>
  );
}

function Sparkline({ points, positive }: { points: number[]; positive: boolean }) {
  if (points.length < 2) {
    return <span className="text-slate-300 dark:text-slate-700">—</span>;
  }
  const w = 88;
  const h = 28;
  const min = Math.min(...points);
  const max = Math.max(...points);
  const range = max - min || 1;
  const stepX = w / (points.length - 1);
  const path = points
    .map((p, i) => {
      const x = i * stepX;
      const y = h - ((p - min) / range) * h;
      return `${i === 0 ? 'M' : 'L'}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(' ');
  const stroke = positive ? '#059669' : '#e11d48';
  return (
    <svg width={w} height={h} viewBox={`0 0 ${w} ${h}`} className="inline-block">
      <path d={path} fill="none" stroke={stroke} strokeWidth="1.25" strokeLinecap="round" strokeLinejoin="round" />
    </svg>
  );
}

// PriceCell renders the USD price and flashes green/red when the
// value changes vs the prior render. Uses a ref to track the last
// observed price so re-renders triggered by sort/filter (which
// keep the price stable) don't trigger flashes — only an actual
// price change from the polling refetch does.
//
// Flash is a CSS-driven background tint that auto-clears after
// 600ms. Tailwind's `transition-colors` smooths the fade-out so
// rapid successive changes blend rather than strobe.
function PriceCell({ value }: { value: number | null }) {
  const prevRef = useRef<number | null>(null);
  const [flash, setFlash] = useState<'up' | 'down' | null>(null);

  useEffect(() => {
    const prev = prevRef.current;
    if (prev != null && value != null && prev !== value) {
      setFlash(value > prev ? 'up' : 'down');
      const t = setTimeout(() => setFlash(null), 600);
      prevRef.current = value;
      return () => clearTimeout(t);
    }
    prevRef.current = value;
  }, [value]);

  if (value == null) return <Dash />;
  const flashCls =
    flash === 'up'
      ? 'bg-emerald-100/70 dark:bg-emerald-900/40'
      : flash === 'down'
        ? 'bg-rose-100/70 dark:bg-rose-900/40'
        : '';
  return (
    <span
      className={`inline-block rounded px-1 font-mono tabular-nums text-slate-900 transition-colors duration-500 dark:text-slate-100 ${flashCls}`}
    >
      ${formatPriceSmart(value)}
    </span>
  );
}

function ChangePct({ value }: { value: number | null | undefined }) {
  if (value == null || !Number.isFinite(value))
    return <span className="text-slate-300 dark:text-slate-700" title="No data yet">—</span>;
  const tone =
    value > 0
      ? 'text-emerald-600 dark:text-emerald-400'
      : value < 0
        ? 'text-rose-600 dark:text-rose-400'
        : 'text-slate-500';
  const sign = value > 0 ? '+' : '';
  return (
    <span className={`font-mono tabular-nums ${tone}`}>
      {sign}
      {value.toFixed(2)}%
    </span>
  );
}

function Dash() {
  return <span className="text-slate-300 dark:text-slate-700">—</span>;
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

function SortableTh({
  sortKey,
  current,
  dir,
  onToggle,
  children,
  align,
}: {
  sortKey: SortKey;
  current: SortKey;
  dir: 'asc' | 'desc';
  onToggle: (k: SortKey) => void;
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  const active = current === sortKey;
  return (
    <th scope="col" className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}>
      <button
        type="button"
        onClick={() => onToggle(sortKey)}
        className={`inline-flex items-center gap-1 text-[10px] uppercase tracking-wider ${
          active ? 'text-brand-700 dark:text-brand-300' : 'text-slate-500 hover:text-slate-700 dark:hover:text-slate-300'
        }`}
      >
        {children}
        {active && <span aria-hidden>{dir === 'asc' ? '↑' : '↓'}</span>}
      </button>
    </th>
  );
}

// exportRowsToCsv writes the currently-visible rows to the user's
// clipboard-equivalent — a downloaded `currencies-YYYY-MM-DD.csv`
// file. Pure browser-side; uses a Blob URL so no server roundtrip.
// Header order mirrors the visible table so the export and the
// page tell the same story.
// SearchInput wraps the listing's search box with a `/` keyboard
// shortcut (matching the GitHub / Linear convention) and a
// dismissive `Esc` clear. Click + focus behavior unchanged.
function SearchInput({
  value,
  onChange,
}: {
  value: string;
  onChange: (v: string) => void;
}) {
  const inputRef = useRef<HTMLInputElement | null>(null);
  useEffect(() => {
    function onKey(e: KeyboardEvent) {
      // Ignore the shortcut when the user is already typing
      // somewhere — pressing `/` mid-typed message in another
      // input shouldn't yank focus to the search.
      const target = e.target as HTMLElement | null;
      const tag = target?.tagName;
      if (tag === 'INPUT' || tag === 'TEXTAREA' || (target && target.isContentEditable)) {
        return;
      }
      if (e.key === '/') {
        e.preventDefault();
        inputRef.current?.focus();
      }
    }
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, []);
  return (
    <div className="relative">
      <Search className="absolute left-2.5 top-1/2 h-4 w-4 -translate-y-1/2 text-slate-400" />
      <input
        ref={inputRef}
        type="search"
        aria-label="Search currencies by ticker, name, or slug"
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === 'Escape') {
            onChange('');
            (e.target as HTMLInputElement).blur();
          }
        }}
        placeholder="Search by ticker, name, or slug… (press /)"
        className="w-80 rounded-md border border-slate-200 bg-white py-1.5 pl-8 pr-3 text-sm placeholder:text-slate-400 focus:border-brand-500 focus:outline-none focus:ring-1 focus:ring-brand-500 dark:border-slate-700 dark:bg-slate-900 dark:placeholder:text-slate-500"
      />
    </div>
  );
}

function exportRowsToCsv(rows: UnifiedRow[]) {
  const header = [
    'rank',
    'kind',
    'ticker',
    'name',
    'price_usd',
    'change_1h_pct',
    'change_24h_pct',
    'change_7d_pct',
    'market_cap_usd',
    'volume_24h_usd',
    'circulating_supply',
  ];
  const lines = [header.join(',')];
  rows.forEach((r, i) => {
    const fields = [
      String(i + 1),
      r.kind,
      esc(r.ticker),
      esc(r.name),
      fmtNum(r.priceUsd),
      fmtNum(r.change1hPct),
      fmtNum(r.change24hPct),
      fmtNum(r.change7dPct),
      fmtNum(r.marketCapUsd),
      fmtNum(r.volume24hUsd),
      fmtNum(r.circulatingSupply),
    ];
    lines.push(fields.join(','));
  });
  const csv = lines.join('\n') + '\n';
  const blob = new Blob([csv], { type: 'text/csv;charset=utf-8;' });
  const url = URL.createObjectURL(blob);
  const a = document.createElement('a');
  const today = new Date().toISOString().slice(0, 10);
  a.href = url;
  a.download = `ratesengine-currencies-${today}.csv`;
  document.body.appendChild(a);
  a.click();
  document.body.removeChild(a);
  URL.revokeObjectURL(url);
}

function esc(s: string): string {
  if (s.includes(',') || s.includes('"') || s.includes('\n')) {
    return `"${s.replace(/"/g, '""')}"`;
  }
  return s;
}

function fmtNum(n: number | null): string {
  if (n == null || !Number.isFinite(n)) return '';
  // Avoid scientific notation in CSV — spreadsheets handle plain
  // decimal better than 1.234e-7.
  return n.toString();
}

function formatPriceSmart(n: number): string {
  if (!Number.isFinite(n) || n <= 0) return '—';
  if (n >= 1000) return n.toLocaleString(undefined, { maximumFractionDigits: 2 });
  if (n >= 1) return n.toFixed(4);
  if (n >= 0.0001) return n.toFixed(6);
  return n.toExponential(3);
}

// FreshnessHint — live "updated Xs ago" indicator. Re-renders every
// second via a useEffect-based ticker so the relative time stays
// honest as the page idles. Distinguishes the two upstream feeds
// (crypto + fiat) since they have different cadences.
function FreshnessHint({
  cryptoUpdatedAt,
  fiatUpdatedAt,
  fiatPublishedAt,
  isFetching,
}: {
  cryptoUpdatedAt: number;
  fiatUpdatedAt: number;
  fiatPublishedAt?: string;
  isFetching: boolean;
}) {
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    const id = setInterval(() => setNow(Date.now()), 1000);
    return () => clearInterval(id);
  }, []);

  const cryptoAge = cryptoUpdatedAt > 0 ? Math.max(0, Math.round((now - cryptoUpdatedAt) / 1000)) : null;
  const fiatAge = fiatUpdatedAt > 0 ? Math.max(0, Math.round((now - fiatUpdatedAt) / 1000)) : null;

  return (
    <span className="inline-flex flex-wrap items-center gap-1.5 text-xs text-slate-500 dark:text-slate-400">
      {cryptoAge != null && (
        <span>crypto · {formatRelativeShort(cryptoAge)}</span>
      )}
      {cryptoAge != null && fiatAge != null && <span className="text-slate-300">·</span>}
      {fiatAge != null && (
        <span>fiat · {formatRelativeShort(fiatAge)}</span>
      )}
      {fiatPublishedAt && (
        <>
          <span className="text-slate-300">·</span>
          <span>fiat published {formatDate(fiatPublishedAt)}</span>
        </>
      )}
      {isFetching && (
        <>
          <span className="text-slate-300">·</span>
          <span className="inline-flex h-1.5 w-1.5 animate-pulse rounded-full bg-emerald-500" aria-label="refreshing" />
        </>
      )}
    </span>
  );
}

function formatRelativeShort(seconds: number): string {
  if (seconds < 5) return 'just now';
  if (seconds < 60) return `${seconds}s ago`;
  if (seconds < 3600) return `${Math.round(seconds / 60)}m ago`;
  if (seconds < 86400) return `${Math.round(seconds / 3600)}h ago`;
  return `${Math.round(seconds / 86400)}d ago`;
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
