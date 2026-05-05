'use client';

import { useMemo } from 'react';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useCoins, useHistory, type TradeRow } from '@/api/hooks';

const DEFAULT_QUOTE = 'native';
const HISTORY_LIMIT = 100;

/**
 * HistoryTabPanel — backs the "History" tab on /coins/[slug].
 *
 * Resolves slug → asset_id via /v1/coins (cache-shared with the
 * coin directory) then fetches recent trades from /v1/history with
 * `base=<asset_id>&quote=native` (XLM) — the most active on-chain
 * pair for any classic asset on Stellar.
 *
 * Fiat-quoted trades (e.g. against `fiat:USD`) don't surface here:
 * /v1/history serves raw on-chain trades only; aggregator-derived
 * pairs ship via /v1/vwap and /v1/twap.
 */
export function HistoryTabPanel({ slug }: { slug: string }) {
  const coins = useCoins(100);

  const assetID = useMemo(
    () => coins.data?.find((c) => c.slug === slug)?.asset_id,
    [coins.data, slug],
  );

  const history = useHistory(assetID, DEFAULT_QUOTE, HISTORY_LIMIT);

  if (coins.isError || history.isError) {
    return (
      <Panel
        title="Recent trades"
        source={asExample('/v1/history', {
          base: assetID ?? '<asset_id>',
          quote: DEFAULT_QUOTE,
          limit: HISTORY_LIMIT,
        })}
        bodyClassName="text-sm text-down-strong"
      >
        Failed to load history.
      </Panel>
    );
  }

  if (coins.isLoading || history.isLoading) {
    return (
      <Panel
        title="Recent trades"
        source={asExample('/v1/history', {
          base: '<asset_id>',
          quote: DEFAULT_QUOTE,
          limit: HISTORY_LIMIT,
        })}
        bodyClassName="text-sm text-slate-500"
      >
        Loading…
      </Panel>
    );
  }

  if (!assetID) {
    return (
      <Panel
        title="Recent trades"
        source={asExample('/v1/coins', { limit: 100 })}
        bodyClassName="text-sm text-slate-500"
      >
        Slug not found in the live coin directory.
      </Panel>
    );
  }

  const rows = history.data ?? [];

  if (rows.length === 0) {
    return (
      <Panel
        title="Recent trades"
        source={asExample('/v1/history', {
          base: assetID,
          quote: DEFAULT_QUOTE,
          limit: HISTORY_LIMIT,
        })}
        bodyClassName="text-sm text-slate-500"
      >
        No trades observed against XLM in the recent window. Try the
        Markets tab to see other quote pairs that have traded.
      </Panel>
    );
  }

  return (
    <Panel
      title={`Recent trades — last ${rows.length}`}
      source={asExample('/v1/history', {
        base: assetID,
        quote: DEFAULT_QUOTE,
        limit: HISTORY_LIMIT,
      })}
      bodyClassName="overflow-x-auto"
    >
      <table className="w-full min-w-[640px] text-sm">
        <thead className="text-left text-xs uppercase tracking-wider text-slate-500">
          <tr className="border-b border-slate-200 dark:border-slate-800">
            <th className="py-2 pr-3 font-medium">When</th>
            <th className="py-2 pr-3 font-medium">Source</th>
            <th className="py-2 pr-3 font-medium">Ledger</th>
            <th className="py-2 pr-3 text-right font-medium">Base amount</th>
            <th className="py-2 pr-3 text-right font-medium">Quote amount</th>
            <th className="py-2 pr-3 text-right font-medium">Price</th>
          </tr>
        </thead>
        <tbody>
          {rows.map((r) => (
            <tr
              key={`${r.tx_hash}-${r.op_index}`}
              className="border-b border-slate-100 last:border-0 dark:border-slate-800/60"
            >
              <td className="py-2 pr-3 font-mono text-xs text-slate-600 dark:text-slate-400">
                {ageOf(r.ts)}
              </td>
              <td className="py-2 pr-3">
                <span className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-[11px] text-slate-700 dark:bg-slate-800 dark:text-slate-300">
                  {r.source}
                </span>
              </td>
              <td className="py-2 pr-3 font-mono text-xs text-slate-500">
                {r.ledger}
              </td>
              <td className="py-2 pr-3 text-right font-mono text-xs">
                {formatStroopAmount(r.base_amount)}
              </td>
              <td className="py-2 pr-3 text-right font-mono text-xs">
                {formatStroopAmount(r.quote_amount)}
              </td>
              <td className="py-2 pr-3 text-right font-mono text-xs">
                {r.price ?? deriveAvgPrice(r.base_amount, r.quote_amount)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  );
}

function ageOf(iso: string): string {
  const ms = Date.now() - Date.parse(iso);
  if (Number.isNaN(ms)) return iso;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  const d = Math.floor(h / 24);
  return `${d}d ago`;
}

// Format a stroop string (10^7 scale) into a human-readable
// fractional. Bigger amounts use compact notation (k/M/B); small
// amounts show up to 4 decimals. Strings throughout per ADR-0003 —
// this is a display-time conversion only, never used for further
// arithmetic.
function formatStroopAmount(s: string): string {
  const n = Number(s);
  if (!Number.isFinite(n)) return s;
  const v = n / 1e7;
  if (Math.abs(v) >= 1_000_000) return `${(v / 1_000_000).toFixed(2)}M`;
  if (Math.abs(v) >= 1_000) return `${(v / 1_000).toFixed(2)}k`;
  if (Math.abs(v) >= 1) return v.toFixed(2);
  return v.toFixed(4);
}

function deriveAvgPrice(base: string, quote: string): string {
  const b = Number(base);
  const q = Number(quote);
  if (!b || !q || !Number.isFinite(b) || !Number.isFinite(q)) return '—';
  return (q / b).toFixed(7);
}

export type { TradeRow };
