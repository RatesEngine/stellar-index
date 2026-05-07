import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { asExample, API_BASE_URL } from '@/api/client';
import { formatCompact } from '@/lib/format';

interface PoolRow {
  source: string;
  base: string;
  quote: string;
  last_trade_at: string;
  trade_count_24h: number;
  volume_24h_usd?: string | null;
  last_price?: string | null;
}

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');
const BUILD_FETCH_TIMEOUT_MS = 8_000;

async function fetchPoolsForAsset(side: 'base' | 'quote', assetID: string): Promise<PoolRow[]> {
  if (isCIStub) return [];
  try {
    const url = `${API_BASE_URL}/v1/pools?${side}=${encodeURIComponent(assetID)}&limit=100&order_by=volume_24h_usd_desc`;
    const res = await fetch(url, { signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS) });
    if (!res.ok) return [];
    const env = (await res.json()) as { data?: PoolRow[] };
    return env.data ?? [];
  } catch {
    return [];
  }
}

/**
 * LiquidityTabPanel — every DEX pool that touches this asset on
 * either side, ranked by 24h USD volume. Two parallel fetches
 * to /v1/pools (one with ?base=, one with ?quote=) merged + deduped
 * into a single table — pairs only ever land on one side per pool
 * but the asset itself can be base in one pool and quote in another.
 *
 * Server component; fetched at request time. Empty-state when the
 * asset has no DEX activity in the recency window.
 */
export async function LiquidityTabPanel({
  assetID,
  code,
}: {
  assetID: string;
  code: string;
}) {
  const [asBase, asQuote] = await Promise.all([
    fetchPoolsForAsset('base', assetID),
    fetchPoolsForAsset('quote', assetID),
  ]);
  const merged = [
    ...asBase.map((p) => ({ ...p, side: 'base' as const })),
    ...asQuote.map((p) => ({ ...p, side: 'quote' as const })),
  ].sort((a, b) => {
    const av = Number(a.volume_24h_usd ?? '0');
    const bv = Number(b.volume_24h_usd ?? '0');
    return (Number.isFinite(bv) ? bv : 0) - (Number.isFinite(av) ? av : 0);
  });

  return (
    <Panel
      title={`Liquidity — every DEX pool that touches ${code}`}
      hint="Per-source breakdown across DEXes. Backed by /v1/pools?base= and ?quote=."
      source={asExample('/v1/pools', { base: assetID })}
      bodyClassName="-mx-4"
    >
      {merged.length === 0 ? (
        <p className="px-4 py-3 text-sm text-slate-500">
          No DEX pools observed touching {code} in the trailing 14 days.
          Either the asset only trades on CEX feeds or the dispatcher
          hasn&apos;t decoded a swap involving it yet.
        </p>
      ) : (
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-slate-500">
                <th className="px-4 py-2 font-medium">Venue</th>
                <th className="px-4 py-2 font-medium">Pair</th>
                <th className="px-4 py-2 font-medium">Side</th>
                <th className="px-4 py-2 text-right font-medium">Last price</th>
                <th className="px-4 py-2 text-right font-medium">24h volume</th>
                <th className="px-4 py-2 text-right font-medium">24h trades</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
              {merged.map((p) => {
                const slug = encodeURIComponent(`${p.base}~${p.quote}`);
                const lp = p.last_price ? Number(p.last_price) : null;
                const lpFixed =
                  lp == null
                    ? null
                    : lp >= 1000
                      ? lp.toFixed(2)
                      : lp >= 1
                        ? lp.toFixed(4)
                        : lp >= 0.0001
                          ? lp.toFixed(6)
                          : lp.toExponential(3);
                return (
                  <tr
                    key={`${p.source}|${p.base}|${p.quote}|${p.side}`}
                    className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
                  >
                    <td className="px-4 py-2">
                      <Link
                        href={`/sources/${p.source}`}
                        className="font-mono text-xs uppercase tracking-wider text-slate-700 hover:text-brand-600 dark:text-slate-300"
                      >
                        {p.source}
                      </Link>
                    </td>
                    <td className="px-4 py-2">
                      <Link
                        href={`/markets/${slug}`}
                        className="font-mono text-xs hover:text-brand-600"
                      >
                        {shortAsset(p.base)} / {shortAsset(p.quote)}
                      </Link>
                    </td>
                    <td className="px-4 py-2">
                      <span className="rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                        {p.side}
                      </span>
                    </td>
                    <td className="px-4 py-2 text-right">
                      {lpFixed ? (
                        <span className="font-mono tabular-nums text-slate-700 dark:text-slate-300">
                          {lpFixed}
                        </span>
                      ) : (
                        <span className="text-slate-300 dark:text-slate-700">—</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right">
                      {p.volume_24h_usd ? (
                        <span className="font-mono tabular-nums">
                          ${formatCompact(Number(p.volume_24h_usd))}
                        </span>
                      ) : (
                        <span className="text-slate-300 dark:text-slate-700">—</span>
                      )}
                    </td>
                    <td className="px-4 py-2 text-right">
                      <span className="font-mono tabular-nums text-slate-500">
                        {formatCompact(p.trade_count_24h)}
                      </span>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      )}
    </Panel>
  );
}

function shortAsset(canonical: string): string {
  if (canonical === 'native') return 'XLM';
  if (canonical.startsWith('fiat:')) return canonical.replace('fiat:', '');
  if (canonical.startsWith('crypto:')) return canonical;
  if (/^\d+$/.test(canonical)) return 'XLM';
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) return canonical;
  return canonical.slice(0, dashIx);
}
