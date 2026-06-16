'use client';

import Link from 'next/link';

import { useCoins, useNetworkStats } from '@/api/hooks';
import { Stat } from '@/components/ui';
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
        href="/markets"
      />
      <Cell
        label="Active markets"
        value={
          activeMarkets != null ? activeMarkets.toLocaleString() : '—'
        }
        sub="trading in last 24h"
        href="/markets"
      />
      <Cell
        label="Assets indexed"
        value={
          assetsIndexed != null ? formatCompact(assetsIndexed) : '—'
        }
        sub="classic + native"
        href="/assets"
      />
      <Cell
        label="Sources online"
        value={
          exchangeSources != null ? `${exchangeSources}` : '—'
        }
        sub="Class = exchange"
        href="/sources"
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
          href="/assets/XLM"
        />
      ) : (
        <Cell
          label="Ledger tip"
          value={tipLedger != null ? `#${tipLedger.toLocaleString()}` : '—'}
          sub="ingest cursor"
          mono
          href="/diagnostics"
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
  href,
}: {
  label: string;
  value: string;
  sub?: string;
  tone?: 'up' | 'down';
  mono?: boolean;
  href?: string;
}) {
  const subNode = sub ? (
    <span
      className={
        tone === 'up' ? 'text-up' : tone === 'down' ? 'text-down' : 'text-ink-muted'
      }
    >
      {sub}
    </span>
  ) : undefined;
  const inner = (
    <Stat
      label={label}
      value={
        <span className={`truncate ${mono ? 'font-mono' : ''}`} title={value}>
          {value}
        </span>
      }
      sub={subNode}
    />
  );
  const baseClass = 'block rounded-card border border-line bg-surface p-4';
  if (href) {
    return (
      <Link
        href={href}
        className={`${baseClass} shadow-card transition-all hover:border-line-strong hover:shadow-elevated`}
      >
        {inner}
      </Link>
    );
  }
  return <div className={`${baseClass} shadow-card`}>{inner}</div>;
}
