'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { AssetText } from '@/components/AssetLink';
import { apiGet, asExample } from '@/api/client';

interface MevLeg {
  source: string;
  base: string;
  quote: string;
  base_amount: string;
  quote_amount: string;
  op_index: number;
}

interface MevDetail {
  assets?: string[];
  sources?: string[];
  legs?: MevLeg[];
  notional_usd?: string;
  note?: string;
}

interface MevEvent {
  event_id: string;
  detected_at: string;
  detected_at_ledger: number;
  kind: string;
  tx_hashes: string[];
  accounts: string[];
  detail: MevDetail;
  profit_usd: string | null;
}

const usdFmt = new Intl.NumberFormat('en-US', { style: 'currency', currency: 'USD', maximumFractionDigits: 0 });

export function MevFeed() {
  const q = useQuery<MevEvent[]>({
    queryKey: ['/v1/mev', 'arbitrage'],
    queryFn: async () => {
      const env = await apiGet<{ data: MevEvent[] }>('/v1/mev', { kind: 'arbitrage', limit: 50 });
      return env.data ?? [];
    },
    staleTime: 60_000,
  });

  const rows = q.data ?? [];

  return (
    <Panel
      title={`Detected arbitrage${rows.length > 0 ? ` (${rows.length})` : ''}`}
      hint="Atomic cyclic trades — one taker, one transaction, a closed asset cycle. Structural detection; profit is not estimated."
      source={asExample('/v1/mev', { kind: 'arbitrage', limit: 50 })}
      bodyClassName="space-y-3"
    >
      {q.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {q.isError && (
        <p className="text-sm text-ink-muted">
          The MEV feed is unavailable right now.
        </p>
      )}
      {!q.isLoading && !q.isError && rows.length === 0 && (
        <p className="text-sm text-ink-muted">
          No arbitrage cycles detected in the recent window yet. The detector
          scans the trade stream every few minutes.
        </p>
      )}
      {rows.length > 0 && (
        <ul className="divide-y divide-line-subtle">
          {rows.map((e) => {
            const assets = e.detail.assets ?? [];
            const sources = e.detail.sources ?? [];
            const taker = e.accounts[0] ?? '';
            const tx = e.tx_hashes[0] ?? '';
            return (
              <li key={e.event_id} className="py-3 text-sm">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
                  <span className="inline-block rounded-sm bg-down-subtle px-1.5 py-0.5 text-[11px] font-medium uppercase tracking-wider text-down-strong">
                    {e.kind}
                  </span>
                  <span className="inline-flex flex-wrap items-center gap-1 font-mono text-xs text-ink-body">
                    {assets.map((a, i) => (
                      <span key={`${a}-${i}`} className="inline-flex items-center gap-1">
                        <AssetText canonical={a} />
                        <span className="text-ink-faint">→</span>
                      </span>
                    ))}
                    {assets.length > 0 && <AssetText canonical={assets[0]} />}
                  </span>
                  {sources.length > 0 && (
                    <span className="text-[11px] text-ink-muted">
                      via{' '}
                      {sources.map((s, i) => (
                        <span key={s}>
                          {i > 0 && ', '}
                          <Link href={`/sources/${encodeURIComponent(s)}`} className="hover:text-brand-600 hover:underline">
                            {s}
                          </Link>
                        </span>
                      ))}
                    </span>
                  )}
                  {e.detail.notional_usd && (
                    <span className="font-mono text-xs text-ink-body">
                      {usdFmt.format(Number(e.detail.notional_usd))}
                    </span>
                  )}
                  <span className="ml-auto font-mono text-[11px] text-ink-muted">
                    ledger {e.detected_at_ledger.toLocaleString()}
                  </span>
                </div>
                <div className="mt-1 flex flex-wrap gap-x-4 text-[11px] text-ink-muted">
                  {taker && (
                    <Link
                      href={`/accounts/${encodeURIComponent(taker)}/`}
                      className="font-mono hover:text-brand-600 hover:underline"
                      title={taker}
                    >
                      taker {taker.slice(0, 6)}…{taker.slice(-4)}
                    </Link>
                  )}
                  {tx && (
                    <Link
                      href={`/transactions/${encodeURIComponent(tx)}/`}
                      className="font-mono hover:text-brand-600 hover:underline"
                    >
                      tx {tx.slice(0, 8)}…
                    </Link>
                  )}
                  <span>{e.detail.legs?.length ?? 0} legs</span>
                </div>
              </li>
            );
          })}
        </ul>
      )}
    </Panel>
  );
}
