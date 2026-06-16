'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import { formatCompact } from '@/lib/format';
import {
  type Envelope,
  type Ledger,
  type LedgerTransactionsResp,
  CopyHash,
  formatTimestamp,
  stroopsToXlm,
} from '../explorer-shared';

/**
 * Client view for /ledger?seq=N. Fetches the ledger header and its
 * transactions in parallel and renders them as cards + a table.
 */
export function LedgerView() {
  const params = useSearchParams();
  const seqRaw = params.get('seq') ?? '';
  const seq = /^\d+$/.test(seqRaw.trim()) ? Number(seqRaw.trim()) : null;

  const ledgerQ = useQuery<Ledger>({
    queryKey: ['/v1/ledgers/{seq}', seq],
    enabled: seq != null,
    queryFn: async () => {
      const env = await apiGet<Envelope<Ledger>>(`/v1/ledgers/${seq}`);
      return env.data;
    },
    staleTime: 60_000,
    retry: false,
  });

  const txQ = useQuery<LedgerTransactionsResp>({
    queryKey: ['/v1/ledgers/{seq}/transactions', seq],
    enabled: seq != null,
    queryFn: async () => {
      const env = await apiGet<Envelope<LedgerTransactionsResp>>(
        `/v1/ledgers/${seq}/transactions`,
      );
      return env.data;
    },
    staleTime: 60_000,
    retry: false,
  });

  if (seq == null) {
    return (
      <Shell seq={null}>
        <Panel
          title="No ledger selected"
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            This page needs a <code className="font-mono">?seq=</code> query
            parameter — e.g.{' '}
            <Link href="/ledgers" className="text-brand-600 hover:underline">
              browse recent ledgers
            </Link>{' '}
            and click one.
          </p>
        </Panel>
      </Shell>
    );
  }

  if (ledgerQ.isError) {
    return (
      <Shell seq={seq}>
        <Panel
          title="Ledger not found"
          source={asExample(`/v1/ledgers/${seq}`)}
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            No ledger <span className="font-mono">#{seq.toLocaleString()}</span>{' '}
            in the served tier, or the lookup failed:{' '}
            {ledgerQ.error instanceof Error
              ? ledgerQ.error.message
              : 'unknown error'}
            .
          </p>
        </Panel>
      </Shell>
    );
  }

  if (ledgerQ.isLoading || !ledgerQ.data) {
    return (
      <Shell seq={seq}>
        <Panel
          title={`Ledger #${seq.toLocaleString()}`}
          source={asExample(`/v1/ledgers/${seq}`)}
          bodyClassName="text-sm text-ink-muted"
        >
          Loading…
        </Panel>
      </Shell>
    );
  }

  const l = ledgerQ.data;

  return (
    <Shell seq={seq}>
      <Panel
        title={`Ledger #${l.sequence.toLocaleString()}`}
        hint={formatTimestamp(l.close_time)}
        source={asExample(`/v1/ledgers/${seq}`)}
      >
        <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-4">
          <Field
            label="Sequence"
            mono
            value={`#${l.sequence.toLocaleString()}`}
          />
          <Field label="Close time" value={formatTimestamp(l.close_time)} />
          <Field label="Protocol" mono value={String(l.protocol_version)} />
          <Field label="Transactions" value={formatCompact(l.tx_count)} />
          <Field label="Operations" value={formatCompact(l.op_count)} />
          <Field
            label="Soroban events"
            value={
              l.soroban_event_count > 0
                ? formatCompact(l.soroban_event_count)
                : '0'
            }
          />
          <Field
            label="Total coins"
            mono
            value={l.total_coins ? `${stroopsToXlm(l.total_coins)} XLM` : '—'}
          />
          <Field
            label="Fee pool"
            mono
            value={l.fee_pool ? `${stroopsToXlm(l.fee_pool)} XLM` : '—'}
          />
          <Field
            label="Base fee"
            mono
            value={l.base_fee != null ? `${l.base_fee} stroops` : '—'}
          />
          <Field
            label="Base reserve"
            mono
            value={
              l.base_reserve != null
                ? `${stroopsToXlm(l.base_reserve)} XLM`
                : '—'
            }
          />
          <FieldWide label="Hash">
            <CopyHash value={l.hash} head={12} tail={12} />
          </FieldWide>
          <FieldWide label="Previous hash">
            {l.prev_hash ? (
              <span className="inline-flex items-center gap-2">
                <Link
                  href={`/ledger?seq=${l.sequence - 1}`}
                  className="font-mono text-xs text-brand-600 hover:underline"
                  title={`Ledger #${(l.sequence - 1).toLocaleString()}`}
                >
                  ← #{(l.sequence - 1).toLocaleString()}
                </Link>
                <CopyHash value={l.prev_hash} head={12} tail={12} />
              </span>
            ) : (
              <span className="text-ink-faint">—</span>
            )}
          </FieldWide>
        </dl>
      </Panel>

      <TransactionsPanel
        seq={seq}
        isLoading={txQ.isLoading}
        isError={txQ.isError}
        error={txQ.error}
        data={txQ.data}
      />
    </Shell>
  );
}

function Shell({
  seq,
  children,
}: {
  seq: number | null;
  children: React.ReactNode;
}) {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-3">
        <nav className="text-xs text-ink-muted">
          <Link href="/ledgers" className="hover:text-brand-600">
            Ledgers
          </Link>{' '}
          /{' '}
          <span className="font-mono text-ink-body">
            {seq != null ? `#${seq.toLocaleString()}` : '—'}
          </span>
        </nav>
        {seq != null && (
          <div className="flex items-center gap-3 text-xs">
            <Link
              href={`/ledger?seq=${seq - 1}`}
              className="rounded-md border border-line px-2.5 py-1 text-ink-body hover:border-brand-500 hover:text-brand-600"
            >
              ← Prev ledger
            </Link>
            <Link
              href={`/ledger?seq=${seq + 1}`}
              className="rounded-md border border-line px-2.5 py-1 text-ink-body hover:border-brand-500 hover:text-brand-600"
            >
              Next ledger →
            </Link>
          </div>
        )}
      </header>
      {children}
    </div>
  );
}

function TransactionsPanel({
  seq,
  isLoading,
  isError,
  error,
  data,
}: {
  seq: number;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
  data: LedgerTransactionsResp | undefined;
}) {
  const source = asExample(`/v1/ledgers/${seq}/transactions`);
  if (isError) {
    return (
      <Panel
        title="Transactions"
        source={source}
        bodyClassName="text-sm text-down-strong"
      >
        Failed to load transactions:{' '}
        {error instanceof Error ? error.message : 'unknown error'}
      </Panel>
    );
  }
  if (isLoading || !data) {
    return (
      <Panel
        title="Transactions"
        source={source}
        bodyClassName="text-sm text-ink-muted"
      >
        Loading…
      </Panel>
    );
  }
  if (data.transactions.length === 0) {
    return (
      <Panel
        title="Transactions"
        source={source}
        bodyClassName="text-sm text-ink-muted"
      >
        This ledger closed no transactions.
      </Panel>
    );
  }
  return (
    <Panel
      title={`Transactions (${data.transactions.length})`}
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <Th>Hash</Th>
              <Th>Source</Th>
              <Th align="right">Ops</Th>
              <Th>Result</Th>
              <Th align="right">Fee</Th>
              <Th>Memo</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {data.transactions.map((t) => (
              <tr
                key={t.hash}
                className="hover:bg-surface-muted"
              >
                <Td>
                  <Link
                    href={`/tx?hash=${t.hash}`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                    title={t.hash}
                  >
                    {t.hash.slice(0, 10)}…{t.hash.slice(-6)}
                  </Link>
                </Td>
                <Td>
                  <Link
                    href={`/accounts?id=${encodeURIComponent(t.source_account)}`}
                    className="font-mono text-xs text-ink-body hover:text-brand-600"
                    title={t.source_account}
                  >
                    {t.source_account.slice(0, 6)}…{t.source_account.slice(-4)}
                  </Link>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-ink-body">
                    {t.operation_count}
                  </span>
                </Td>
                <Td>
                  <SuccessBadge ok={t.successful} code={t.result_code} />
                </Td>
                <Td align="right">
                  <span className="font-mono text-xs tabular-nums text-ink-muted">
                    {t.fee_charged != null ? stroopsToXlm(t.fee_charged) : '—'}
                  </span>
                </Td>
                <Td>
                  {t.memo_type && t.memo_type !== 'none' ? (
                    <span
                      className="font-mono text-[11px] text-ink-muted"
                      title={t.memo ?? ''}
                    >
                      {t.memo_type}
                      {t.memo ? `: ${truncate(t.memo, 18)}` : ''}
                    </span>
                  ) : (
                    <span className="text-ink-faint">
                      —
                    </span>
                  )}
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

// SuccessBadge renders a transaction's result. Success comes from the
// `successful` bool (the authoritative tx-level signal); the numeric
// XDR `code` (int32, 0 = txSUCCESS) is shown as detail on failure.
function SuccessBadge({ ok, code }: { ok: boolean; code?: number }) {
  const codeLabel = code != null ? `code ${code}` : undefined;
  return (
    <span
      className={`inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider ${
        ok
          ? 'bg-up-subtle text-up'
          : 'bg-down-subtle text-down'
      }`}
      title={codeLabel ?? (ok ? 'success' : 'failed')}
    >
      {ok ? 'success' : (codeLabel ?? 'failed')}
    </span>
  );
}

function truncate(s: string, n: number): string {
  return s.length > n ? `${s.slice(0, n)}…` : s;
}

function Field({
  label,
  value,
  mono,
}: {
  label: string;
  value: string;
  mono?: boolean;
}) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-ink-muted">
        {label}
      </dt>
      <dd className={mono ? 'mt-0.5 font-mono text-xs' : 'mt-0.5 tabular-nums'}>
        {value}
      </dd>
    </div>
  );
}

function FieldWide({
  label,
  children,
}: {
  label: string;
  children: React.ReactNode;
}) {
  return (
    <div className="col-span-2 sm:col-span-3 lg:col-span-4">
      <dt className="text-[11px] uppercase tracking-wider text-ink-muted">
        {label}
      </dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  );
}

function Th({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  return (
    <th
      className={`px-4 py-2 ${align === 'right' ? 'text-right' : 'text-left'}`}
      scope="col"
    >
      {children}
    </th>
  );
}

function Td({
  children,
  align,
}: {
  children: React.ReactNode;
  align?: 'left' | 'right';
}) {
  return (
    <td
      className={`px-4 py-3 ${align === 'right' ? 'text-right' : 'text-left'}`}
    >
      {children}
    </td>
  );
}
