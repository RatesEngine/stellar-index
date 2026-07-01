'use client';

import Link from 'next/link';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { Container, PageHeader } from '@/components/ui';
import { formatTimestamp } from '../explorer-shared';

interface ContractRow {
  contract_id: string;
  events: number;
  last_ledger: number;
  last_seen: string;
  protocol?: string;
}

interface DirectoryResp {
  window_days: number;
  since_ledger: number;
  contracts: ContractRow[];
}

/**
 * ContractsView — the contracts directory: the most active Soroban contracts
 * over a recent window, each tagged with its owning protocol where the
 * factory-anchored registry knows it (the attribution hinge). Backed by
 * GET /v1/contracts. Click a contract to open its hub (events, code,
 * interaction map).
 */
export function ContractsView() {
  const { data, isLoading, isError, error } = useQuery<DirectoryResp>({
    queryKey: ['/v1/contracts', 30],
    staleTime: 60_000,
    retry: false,
    queryFn: async () => {
      const env = await apiGet<{ data: DirectoryResp }>('/v1/contracts', {
        days: 30,
        limit: 100,
      });
      return env.data;
    },
  });

  const rows = data?.contracts ?? [];

  return (
    <Container className="space-y-8 py-8 sm:py-10">
      <PageHeader
        eyebrow="Soroban"
        title="Contracts"
        description="The most active Soroban contracts over the last 30 days, ranked by emitted events. Contracts deployed by a known protocol's factory are tagged with their protocol — click any contract for its hub: events, decoded code, and cross-contract interaction map."
      />

      <Panel
        title={rows.length > 0 ? `Most active (${formatCompact(rows.length)})` : 'Most active'}
        source={asExample('/v1/contracts', { days: 30, limit: 100 })}
        bodyClassName="-mx-4"
      >
        {isError ? (
          <p className="px-4 text-sm text-down-strong">
            Failed to load contracts:{' '}
            {error instanceof Error ? error.message : 'unknown error'}
          </p>
        ) : isLoading ? (
          <p className="px-4 text-sm text-ink-muted">Loading…</p>
        ) : rows.length === 0 ? (
          <p className="px-4 text-sm text-ink-muted">
            No contract activity in the window.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-line text-sm">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
                  <th scope="col" className="px-4 py-2">Contract</th>
                  <th scope="col" className="px-4 py-2">Protocol</th>
                  <th scope="col" className="px-4 py-2 text-right">Events (30d)</th>
                  <th scope="col" className="px-4 py-2">Last seen</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line-subtle">
                {rows.map((c) => (
                  <tr key={c.contract_id} className="hover:bg-surface-muted">
                    <td className="px-4 py-3">
                      <Link
                        href={`/contracts/${encodeURIComponent(c.contract_id)}/`}
                        className="font-mono text-xs text-brand-600 hover:underline"
                        title={c.contract_id}
                      >
                        {c.contract_id.slice(0, 8)}…{c.contract_id.slice(-6)}
                      </Link>
                    </td>
                    <td className="px-4 py-3">
                      {c.protocol ? (
                        <Link
                          href={`/protocols/${encodeURIComponent(c.protocol)}`}
                          className="inline-flex items-center rounded-sm px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider bg-brand-100 text-brand-800 hover:bg-brand-200"
                        >
                          {c.protocol}
                        </Link>
                      ) : (
                        <span className="text-ink-faint">—</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-right font-mono tabular-nums text-ink-body">
                      {formatCompact(c.events)}
                    </td>
                    <td className="px-4 py-3 text-xs text-ink-muted">
                      {formatTimestamp(c.last_seen)}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>

      <p className="text-xs text-ink-muted">
        Looking for a specific contract? Paste its <code className="font-mono">C…</code>{' '}
        address into search, or open it directly at{' '}
        <code className="font-mono">/contracts/&lt;C…&gt;</code>.
      </p>
    </Container>
  );
}
