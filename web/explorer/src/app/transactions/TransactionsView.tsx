'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';
import {
  type Envelope,
  type LedgersPage,
  type LedgerTransaction,
  type LedgerTransactionsResp,
  formatTimestamp,
  stroopsToXlm,
} from '../explorer-shared';

/**
 * TransactionsView — recent network transactions. Defaults to the latest
 * ledger's transactions and lets you page backward/forward by ledger
 * (the served tier is ledger-partitioned, so a ledger-scoped scan is
 * cheap and needs no cross-ledger tx index). `?seq=N` pins a ledger.
 *
 * A true global recent-tx feed (cross-ledger cursor) is a follow-up
 * endpoint; this gives a working transactions browser on today's API.
 */
export function TransactionsView() {
  const params = useSearchParams();
  const seqRaw = params.get('seq') ?? '';
  const explicitSeq = /^\d+$/.test(seqRaw.trim()) ? Number(seqRaw.trim()) : null;

  // No ?seq → resolve the latest ledger so the page opens on live activity.
  const tipQ = useQuery<LedgersPage>({
    queryKey: ['/v1/ledgers', 'tip-1'],
    enabled: explicitSeq == null,
    queryFn: async () => {
      const env = await apiGet<Envelope<LedgersPage>>('/v1/ledgers', { limit: 1 });
      return env.data;
    },
    staleTime: 15_000,
    retry: false,
  });

  const seq = explicitSeq ?? tipQ.data?.ledgers?.[0]?.sequence ?? null;

  const txQ = useQuery<LedgerTransactionsResp>({
    queryKey: ['/v1/ledgers/{seq}/transactions', seq],
    enabled: seq != null,
    queryFn: async () => {
      const env = await apiGet<Envelope<LedgerTransactionsResp>>(
        `/v1/ledgers/${seq}/transactions`,
      );
      return env.data;
    },
    staleTime: 30_000,
    retry: false,
  });

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-3">
        <div>
          <p className="text-xs uppercase tracking-wider text-ink-muted">Explorer</p>
          <h1 className="text-2xl font-semibold tracking-tight text-ink">Transactions</h1>
          <p className="mt-1 max-w-2xl text-sm text-ink-muted">
            Recent transactions, newest ledger first. Click a hash for the full
            decoded transaction — operations, events, and result codes.
          </p>
        </div>
        {seq != null && (
          <div className="flex items-center gap-3 text-xs">
            <Link
              href={`/transactions?seq=${seq + 1}`}
              className="rounded-md border border-line px-2.5 py-1 text-ink-body hover:border-brand-500 hover:text-brand-600"
            >
              ← Newer
            </Link>
            <span className="font-mono text-ink-body">
              Ledger #{seq.toLocaleString()}
            </span>
            <Link
              href={`/transactions?seq=${seq - 1}`}
              className="rounded-md border border-line px-2.5 py-1 text-ink-body hover:border-brand-500 hover:text-brand-600"
            >
              Older →
            </Link>
            <Link href="/ledgers" className="ml-auto text-brand-600 hover:underline">
              Browse ledgers
            </Link>
          </div>
        )}
      </header>

      <TxTable
        seq={seq}
        isLoading={txQ.isLoading || (explicitSeq == null && tipQ.isLoading)}
        isError={txQ.isError || tipQ.isError}
        error={txQ.error ?? tipQ.error}
        rows={txQ.data?.transactions}
      />
    </div>
  );
}

function TxTable({
  seq,
  isLoading,
  isError,
  error,
  rows,
}: {
  seq: number | null;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
  rows: LedgerTransaction[] | undefined;
}) {
  const source = seq != null ? asExample(`/v1/ledgers/${seq}/transactions`) : undefined;
  if (isError) {
    return (
      <Panel title="Transactions" source={source} bodyClassName="text-sm text-down-strong">
        Failed to load transactions:{' '}
        {error instanceof Error ? error.message : 'unknown error'}
      </Panel>
    );
  }
  if (isLoading || rows == null) {
    return (
      <Panel title="Transactions" source={source} bodyClassName="text-sm text-ink-muted">
        Loading…
      </Panel>
    );
  }
  if (rows.length === 0) {
    return (
      <Panel title="Transactions" source={source} bodyClassName="text-sm text-ink-muted">
        Ledger #{seq?.toLocaleString()} closed no transactions. Try an older ledger.
      </Panel>
    );
  }
  return (
    <Panel
      title={`Transactions (${formatCompact(rows.length)})`}
      hint={rows[0] ? formatTimestamp(rows[0].close_time) : undefined}
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <th scope="col" className="px-4 py-2">Hash</th>
              <th scope="col" className="px-4 py-2">Source</th>
              <th scope="col" className="px-4 py-2 text-right">Ops</th>
              <th scope="col" className="px-4 py-2">Result</th>
              <th scope="col" className="px-4 py-2 text-right">Fee</th>
              <th scope="col" className="px-4 py-2">Memo</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {rows.map((t) => (
              <tr key={t.hash} className="hover:bg-surface-muted">
                <td className="px-4 py-3">
                  <Link
                    href={`/tx?hash=${t.hash}`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                    title={t.hash}
                  >
                    {t.hash.slice(0, 10)}…{t.hash.slice(-6)}
                  </Link>
                </td>
                <td className="px-4 py-3">
                  <Link
                    href={`/accounts?id=${encodeURIComponent(t.source_account)}`}
                    className="font-mono text-xs text-ink-body hover:text-brand-600"
                    title={t.source_account}
                  >
                    {t.source_account.slice(0, 6)}…{t.source_account.slice(-4)}
                  </Link>
                </td>
                <td className="px-4 py-3 text-right">
                  <span className="font-mono tabular-nums text-ink-body">
                    {t.operation_count}
                  </span>
                </td>
                <td className="px-4 py-3">
                  <span
                    className={`inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider ${
                      t.successful ? 'bg-up-subtle text-up' : 'bg-down-subtle text-down'
                    }`}
                    title={t.result_code != null ? `code ${t.result_code}` : t.successful ? 'success' : 'failed'}
                  >
                    {t.successful ? 'success' : (t.result_code != null ? `code ${t.result_code}` : 'failed')}
                  </span>
                </td>
                <td className="px-4 py-3 text-right">
                  <span className="font-mono text-xs tabular-nums text-ink-muted">
                    {t.fee_charged != null ? stroopsToXlm(t.fee_charged) : '—'}
                  </span>
                </td>
                <td className="px-4 py-3">
                  {t.memo_type && t.memo_type !== 'none' ? (
                    <span className="font-mono text-[11px] text-ink-muted" title={t.memo ?? ''}>
                      {t.memo_type}
                      {t.memo ? `: ${t.memo.length > 18 ? `${t.memo.slice(0, 18)}…` : t.memo}` : ''}
                    </span>
                  ) : (
                    <span className="text-ink-faint">—</span>
                  )}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}
