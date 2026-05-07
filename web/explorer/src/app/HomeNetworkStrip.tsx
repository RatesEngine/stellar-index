'use client';

import { useCoins, useNetworkStats } from '@/api/hooks';
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
 * Volume / markets / assets / sources / ledger come from
 * /v1/network/stats — server-aggregated across the full corpus,
 * NOT capped at any pagination limit. Earlier versions of this
 * strip summed `useMarkets(500, ...)` + counted `useCoins(50)`
 * and so silently undercounted; the rewrite uses the dedicated
 * aggregate endpoint shipped in PR #840.
 *
 * XLM price is the only field that still piggybacks on /v1/coins
 * — `useCoins(1)` is enough since native XLM is always the first
 * row.
 */
export function HomeNetworkStrip() {
  const stats = useNetworkStats();
  const coins = useCoins(1);

  const volume = stats.data?.volume_24h_usd
    ? Number(stats.data.volume_24h_usd)
    : null;
  const activeMarkets = stats.data?.markets_count_24h ?? null;
  const assetsIndexed = stats.data?.assets_indexed ?? null;
  const exchangeSources = stats.data?.exchange_sources ?? null;
  const tipLedger = stats.data?.latest_ledger ?? null;

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
          volume != null && Number.isFinite(volume) && volume > 0
            ? `$${formatCompact(volume)}`
            : '—'
        }
        sub="across all markets"
      />
      <Cell
        label="Active markets"
        value={
          activeMarkets != null ? activeMarkets.toLocaleString() : '—'
        }
        sub="trading in last 24h"
      />
      <Cell
        label="Assets indexed"
        value={
          assetsIndexed != null ? formatCompact(assetsIndexed) : '—'
        }
        sub="classic + native"
      />
      <Cell
        label="Sources online"
        value={
          exchangeSources != null ? `${exchangeSources}` : '—'
        }
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
