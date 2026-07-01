'use client';

import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { AssetText } from '@/components/AssetLink';
import { apiGet, asExample } from '@/api/client';

interface ReasonCount {
  reason: string;
  count: number;
}

interface FreezeEvent {
  asset_id: string;
  quote_id: string;
  frozen_at: string;
  frozen_at_ledger: number;
  reason: string;
  frozen_value: string;
  recovered_at: string | null;
  recovered_at_ledger: number | null;
  firing: boolean;
}

interface AnomaliesResp {
  firing_count: number;
  reason_tally: ReasonCount[];
  events: FreezeEvent[];
}

function fmtTs(iso: string): string {
  const d = new Date(iso);
  return Number.isNaN(d.getTime()) ? iso : d.toISOString().replace('T', ' ').slice(0, 19) + 'Z';
}

function duration(from: string, to: string | null): string {
  const start = new Date(from).getTime();
  const end = to ? new Date(to).getTime() : Date.now();
  const s = Math.max(0, Math.round((end - start) / 1000));
  if (s < 90) return `${s}s`;
  if (s < 5400) return `${Math.round(s / 60)}m`;
  if (s < 129600) return `${Math.round(s / 3600)}h`;
  return `${Math.round(s / 86400)}d`;
}

export function AnomaliesFeed() {
  const q = useQuery<AnomaliesResp>({
    queryKey: ['/v1/anomalies'],
    queryFn: async () => {
      const env = await apiGet<{ data: AnomaliesResp }>('/v1/anomalies', { limit: 100 });
      return env.data;
    },
    staleTime: 30_000,
  });

  const data = q.data;
  const events = data?.events ?? [];

  return (
    <>
      <Panel
        title="Freeze timeline"
        hint="Every clear→firing transition from the durable freeze_events mirror, newest first."
        source={asExample('/v1/anomalies', { limit: 100 })}
        bodyClassName="space-y-3"
      >
        {data && (
          <div className="flex flex-wrap gap-2 text-xs">
            <span
              className={`inline-flex items-center gap-1 rounded px-2 py-1 font-medium ${
                data.firing_count > 0
                  ? 'bg-down-subtle text-down-strong'
                  : 'bg-up-subtle text-up-strong'
              }`}
            >
              {data.firing_count > 0 ? `${data.firing_count} firing now` : 'Nothing firing'}
            </span>
            {data.reason_tally.map((t) => (
              <span key={t.reason} className="inline-flex items-center gap-1 rounded-sm bg-surface-muted px-2 py-1 text-ink-body">
                <code className="font-mono">{t.reason}</code>
                <span className="text-ink-muted">×{t.count}</span>
              </span>
            ))}
            <span className="self-center text-ink-muted">(reasons: trailing 30d)</span>
          </div>
        )}
        {q.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
        {q.isError && <p className="text-sm text-ink-muted">The anomalies feed is unavailable right now.</p>}
        {data && events.length === 0 && (
          <p className="text-sm text-ink-muted">
            No freeze events recorded yet — every served price has cleared the anomaly checks.
          </p>
        )}
        {events.length > 0 && (
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-line text-left text-[11px] uppercase tracking-wider text-ink-muted">
                <th className="py-1.5 pr-4 font-normal">Pair</th>
                <th className="py-1.5 pr-4 font-normal">Reason</th>
                <th className="py-1.5 pr-4 font-normal">Frozen at</th>
                <th className="py-1.5 pr-4 font-normal">Duration</th>
                <th className="py-1.5 pr-4 text-right font-normal">Frozen value</th>
                <th className="py-1.5 font-normal">State</th>
              </tr>
            </thead>
            <tbody>
              {events.map((e) => (
                <tr
                  key={`${e.asset_id}:${e.quote_id}:${e.frozen_at}`}
                  className="border-b border-line/60 last:border-0 hover:bg-surface-muted"
                >
                  <td className="py-1.5 pr-4 font-mono">
                    <AssetText canonical={e.asset_id} />
                    <span className="text-ink-faint">/</span>
                    <AssetText canonical={e.quote_id} />
                  </td>
                  <td className="py-1.5 pr-4">
                    <code className="text-[11px]">{e.reason}</code>
                  </td>
                  <td className="py-1.5 pr-4 font-mono text-[11px] text-ink-muted">{fmtTs(e.frozen_at)}</td>
                  <td className="py-1.5 pr-4 font-mono tabular-nums text-ink-muted">
                    {duration(e.frozen_at, e.recovered_at)}
                  </td>
                  <td className="py-1.5 pr-4 text-right font-mono tabular-nums">{e.frozen_value}</td>
                  <td className="py-1.5">
                    {e.firing ? (
                      <span className="rounded-sm bg-down-subtle px-1.5 py-0.5 text-[10px] font-medium uppercase text-down-strong">
                        firing
                      </span>
                    ) : (
                      <span className="rounded-sm bg-up-subtle px-1.5 py-0.5 text-[10px] font-medium uppercase text-up-strong">
                        recovered
                      </span>
                    )}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        )}
      </Panel>
    </>
  );
}
