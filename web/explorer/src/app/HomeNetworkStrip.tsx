'use client';

import { useCoins, useMarkets, useSources, useCursors } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * HomeNetworkStrip — 5-card network-level stats hero on the home
 * page. Sits above the existing NetworkLivePanel /
 * SystemHealthLivePanel grid, giving the user an immediate
 * scale-of-the-network read at the top of the page:
 *
 *   - Total 24h USD volume across every pair we observe
 *     (Stellar on-chain + the CEX/FX feeds we ingest)
 *   - Active markets in the trailing 24h
 *   - Asset directory size (classic assets indexed)
 *   - Sources online (Class=exchange contributors to VWAP)
 *   - XLM price (with 24h change pill)
 *
 * Every cell is fed by an existing API endpoint — no synthesised
 * data, no estimates. Cells render `—` while loading or when the
 * underlying call fails.
 */
export function HomeNetworkStrip() {
  const markets = useMarkets(500, 'volume_24h_usd_desc');
  const coins = useCoins(50);
  const sources = useSources();
  const cursors = useCursors();

  const totalVolume = (markets.data?.markets ?? []).reduce(
    (sum, m) => sum + (m.volume_24h_usd ? Number(m.volume_24h_usd) : 0),
    0,
  );
  const activeMarketsCount = markets.data?.markets?.length ?? null;
  const assetsCount = coins.data?.coins?.length ?? null;
  const exchangeSourcesCount =
    (sources.data ?? []).filter((s) => s.class === 'exchange').length || null;
  const tipLedger = cursors.data ? maxLiveLedger(cursors.data) : null;

  const xlm = (coins.data?.coins ?? []).find(
    (c) => c.code === 'XLM' || c.asset_id === 'native',
  );
  const xlmPrice = xlm?.price_usd ? Number(xlm.price_usd) : null;
  const xlmChange = xlm?.change_24h_pct ? Number(xlm.change_24h_pct) : null;

  return (
    <section className="grid grid-cols-2 gap-3 md:grid-cols-5">
      <Cell
        label="24h volume"
        value={
          markets.isError || totalVolume === 0
            ? '—'
            : `$${formatCompact(totalVolume)}`
        }
        sub="across all markets"
      />
      <Cell
        label="Active markets"
        value={activeMarketsCount != null ? activeMarketsCount.toLocaleString() : '—'}
        sub="trading in last 14d"
      />
      <Cell
        label="Assets indexed"
        value={assetsCount != null ? formatCompact(assetsCount) : '—'}
        sub="classic + native"
      />
      <Cell
        label="Sources online"
        value={exchangeSourcesCount != null ? `${exchangeSourcesCount}` : '—'}
        sub="Class = exchange"
      />
      {xlmPrice != null ? (
        <Cell
          label="XLM"
          value={`$${xlmPrice.toFixed(xlmPrice >= 1 ? 4 : 6)}`}
          sub={
            xlmChange != null && Number.isFinite(xlmChange)
              ? `${xlmChange > 0 ? '+' : ''}${xlmChange.toFixed(2)}% 24h`
              : 'no 24h baseline'
          }
          tone={
            xlmChange != null && Number.isFinite(xlmChange)
              ? xlmChange > 0
                ? 'up'
                : xlmChange < 0
                  ? 'down'
                  : undefined
              : undefined
          }
        />
      ) : (
        <Cell
          label="Ledger tip"
          value={tipLedger != null ? `#${tipLedger.toLocaleString()}` : '—'}
          sub="ingest cursor"
          mono
        />
      )}
    </section>
  );
}

function Cell({
  label,
  value,
  sub,
  tone,
  mono,
}: {
  label: string;
  value: string;
  sub?: string;
  tone?: 'up' | 'down';
  mono?: boolean;
}) {
  const subTone =
    tone === 'up'
      ? 'text-emerald-600 dark:text-emerald-400'
      : tone === 'down'
        ? 'text-rose-600 dark:text-rose-400'
        : 'text-slate-500';
  return (
    <div className="rounded-md border border-slate-200 bg-white p-3 dark:border-slate-800 dark:bg-slate-900">
      <div className="text-[10px] uppercase tracking-wider text-slate-500">
        {label}
      </div>
      <div
        className={`mt-1 truncate ${mono ? 'font-mono' : 'font-semibold'} text-lg tabular-nums`}
        title={value}
      >
        {value}
      </div>
      {sub && (
        <div className={`mt-0.5 text-[11px] ${subTone}`}>{sub}</div>
      )}
    </div>
  );
}

function maxLiveLedger(
  cursors: { source: string; last_ledger: number }[],
): number | null {
  let max = -1;
  for (const c of cursors) {
    if (c.source === 'backfill') continue;
    if (Number.isFinite(c.last_ledger) && c.last_ledger > max) {
      max = c.last_ledger;
    }
  }
  return max >= 0 ? max : null;
}
