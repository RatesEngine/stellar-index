'use client';

import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { AssetText } from '@/components/AssetLink';
import { apiGet, asExample } from '@/api/client';

interface DivergenceObs {
  asset_id: string;
  quote_id: string;
  reference: string;
  observed_at: string;
  observed_at_ledger: number;
  our_price: string;
  ref_price: string;
  delta_pct: string;
  status: string;
}

interface DivergenceResp {
  observations: DivergenceObs[];
}

function fmtTs(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toISOString().replace('T', ' ').slice(0, 19) + 'Z';
}

function fmtDelta(s: string): string {
  const n = Number(s);
  if (!Number.isFinite(n)) return s;
  return `${n > 0 ? '+' : ''}${n.toFixed(2)}%`;
}

export function DivergenceFeed() {
  const q = useQuery<DivergenceResp>({
    queryKey: ['/v1/divergence'],
    queryFn: async () => {
      const env = await apiGet<{ data: DivergenceResp }>('/v1/divergence', { limit: 100, window_days: 7 });
      return env.data;
    },
    staleTime: 30_000,
  });

  const rows = q.data?.observations ?? [];

  return (
    <Panel
      title="Divergence board"
      hint="Latest comparison per (pair, reference) over the trailing 7 days — our VWAP vs each external reference, widest gap first."
      source={asExample('/v1/divergence', { limit: 100, window_days: 7 })}
      bodyClassName="space-y-3"
    >
      {q.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
      {q.isError && <p className="text-sm text-ink-muted">The divergence board is unavailable right now.</p>}
      {q.data && rows.length === 0 && (
        <p className="text-sm text-ink-muted">
          No cross-reference comparisons recorded in the last 7 days (the divergence worker writes one row per
          configured (pair, reference) per tick).
        </p>
      )}
      {rows.length > 0 && (
        <table className="w-full text-sm">
          <thead>
            <tr className="border-b border-line text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <th className="py-1.5 pr-4 font-normal">Pair</th>
              <th className="py-1.5 pr-4 font-normal">Reference</th>
              <th className="py-1.5 pr-4 text-right font-normal">Our price</th>
              <th className="py-1.5 pr-4 text-right font-normal">Reference</th>
              <th className="py-1.5 pr-4 text-right font-normal">Δ%</th>
              <th className="py-1.5 pr-4 font-normal">Observed</th>
              <th className="py-1.5 font-normal">State</th>
            </tr>
          </thead>
          <tbody>
            {rows.map((d) => {
              const firing = d.status === 'firing';
              return (
                <tr
                  key={`${d.asset_id}:${d.quote_id}:${d.reference}`}
                  className="border-b border-line/60 last:border-0 hover:bg-surface-muted"
                >
                  <td className="py-1.5 pr-4 font-mono">
                    <AssetText canonical={d.asset_id} />
                    <span className="text-ink-faint">/</span>
                    <AssetText canonical={d.quote_id} />
                  </td>
                  <td className="py-1.5 pr-4">
                    <code className="text-[11px]">{d.reference}</code>
                  </td>
                  <td className="py-1.5 pr-4 text-right font-mono tabular-nums">{d.our_price}</td>
                  <td className="py-1.5 pr-4 text-right font-mono tabular-nums">{d.ref_price}</td>
                  <td
                    className={`py-1.5 pr-4 text-right font-mono tabular-nums ${
                      firing ? 'text-down-strong' : 'text-ink-body'
                    }`}
                  >
                    {fmtDelta(d.delta_pct)}
                  </td>
                  <td className="py-1.5 pr-4 font-mono text-[11px] text-ink-muted">{fmtTs(d.observed_at)}</td>
                  <td className="py-1.5">
                    {firing ? (
                      <span className="rounded-sm bg-down-subtle px-1.5 py-0.5 text-[10px] font-medium uppercase text-down-strong">
                        firing
                      </span>
                    ) : (
                      <span className="rounded-sm bg-up-subtle px-1.5 py-0.5 text-[10px] font-medium uppercase text-up-strong">
                        clear
                      </span>
                    )}
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      )}
    </Panel>
  );
}
