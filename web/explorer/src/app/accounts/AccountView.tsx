'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample } from '@/api/client';
import {
  type Envelope,
  type AccountTransactionsResp,
  type AccountOperationsResp,
  type LedgerTransaction,
  type TxOperation,
  CopyHash,
  formatTimestamp,
  relativeAge,
  stroopsToXlm,
} from '../explorer-shared';

// Stellar account IDs are 56 chars: 'G' + 55 base32 alphanumerics.
const ACCOUNT_RE = /^G[A-Z2-7]{55}$/;

const PAGE_SIZE = 50;

/**
 * Client view for /accounts?id=G…. Fetches the account's sourced
 * transactions and sourced operations in parallel and renders them.
 *
 * SCOPE: this is "sourced/submitted" activity only — what the account
 * itself submitted (its source), NOT incoming transfers or full
 * participant history. The backend stamps a `scope` field saying so;
 * full balances + incoming history land in Phase C.
 */
export function AccountView() {
  const params = useSearchParams();
  const id = (params.get('id') ?? '').trim();
  const looksValid = ACCOUNT_RE.test(id);

  const txQ = useQuery<AccountTransactionsResp>({
    queryKey: ['/v1/accounts/{id}/transactions', id],
    enabled: id.length > 0 && looksValid,
    retry: false,
    queryFn: async () => {
      const env = await apiGet<Envelope<AccountTransactionsResp>>(
        `/v1/accounts/${encodeURIComponent(id)}/transactions`,
        { limit: PAGE_SIZE },
      );
      return env.data;
    },
    staleTime: 30_000,
  });

  const opsQ = useQuery<AccountOperationsResp>({
    queryKey: ['/v1/accounts/{id}/operations', id],
    enabled: id.length > 0 && looksValid,
    retry: false,
    queryFn: async () => {
      const env = await apiGet<Envelope<AccountOperationsResp>>(
        `/v1/accounts/${encodeURIComponent(id)}/operations`,
        { limit: PAGE_SIZE },
      );
      return env.data;
    },
    staleTime: 30_000,
  });

  if (id.length === 0) {
    return (
      <Shell id={null}>
        <Panel
          title="No account selected"
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            This page needs an <code className="font-mono">?id=</code> query
            parameter — a 56-character Stellar account ID (starts with{' '}
            <code className="font-mono">G</code>). Use the search box (
            <kbd className="rounded border border-line-strong px-1 text-[10px]">
              ⌘K
            </kbd>
            ) to look one up.
          </p>
        </Panel>
      </Shell>
    );
  }

  if (!looksValid) {
    return (
      <Shell id={id}>
        <Panel
          title="Invalid account ID"
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            <span className="break-all font-mono">{id}</span> isn&apos;t a valid
            Stellar account ID. Account IDs are 56 characters, starting with{' '}
            <code className="font-mono">G</code>.
          </p>
        </Panel>
      </Shell>
    );
  }

  return (
    <Shell id={id}>
      <Panel
        title="Account"
        source={asExample(`/v1/accounts/${id}/transactions`, {
          limit: PAGE_SIZE,
        })}
        bodyClassName="space-y-3"
      >
        <div>
          <div className="text-[11px] uppercase tracking-wider text-ink-muted">
            Account ID
          </div>
          <div className="mt-0.5">
            <CopyHash value={id} head={16} tail={16} />
          </div>
        </div>
        <ul className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-ink-body">
          <li>
            <a
              href={`https://stellar.expert/explorer/public/account/${encodeURIComponent(id)}`}
              target="_blank"
              rel="noreferrer noopener"
              className="hover:text-brand-600 hover:underline"
            >
              stellar.expert ↗
            </a>
          </li>
        </ul>
        <p className="rounded-md border border-warn-300 bg-warn-50 px-3 py-2 text-xs text-warn-700">
          Showing <strong>sourced/submitted</strong> activity only — the
          transactions this account submitted and the operations it sourced
          (the <code className="font-mono">scope</code> the API reports). Full
          balances and incoming/participant history are coming in Phase C.
        </p>
      </Panel>

      <TransactionsPanel
        id={id}
        isLoading={txQ.isLoading}
        isError={txQ.isError}
        error={txQ.error}
        data={txQ.data}
      />
      <OperationsPanel
        id={id}
        isLoading={opsQ.isLoading}
        isError={opsQ.isError}
        error={opsQ.error}
        data={opsQ.data}
      />
    </Shell>
  );
}

function Shell({
  id,
  children,
}: {
  id: string | null;
  children: React.ReactNode;
}) {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <nav className="text-xs text-ink-muted">
          <Link href="/ledgers" className="hover:text-brand-600">
            Ledgers
          </Link>{' '}
          /{' '}
          <span className="font-mono text-ink-body">
            {id ? `${id.slice(0, 8)}…${id.slice(-6)}` : 'account'}
          </span>
        </nav>
        <h1 className="text-2xl font-semibold tracking-tight">Account</h1>
      </header>
      {children}
    </div>
  );
}

function TransactionsPanel({
  id,
  isLoading,
  isError,
  error,
  data,
}: {
  id: string;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
  data: AccountTransactionsResp | undefined;
}) {
  const source = asExample(`/v1/accounts/${id}/transactions`, {
    limit: PAGE_SIZE,
  });
  if (isError) {
    return (
      <Panel
        title="Transactions"
        source={source}
        bodyClassName="text-sm text-ink-body"
      >
        No transactions for that account in the served tier, or the lookup
        failed: {error instanceof Error ? error.message : 'unknown error'}.
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
        No sourced transactions observed for this account.
      </Panel>
    );
  }
  return (
    <Panel
      title={`Sourced transactions (${data.transactions.length})`}
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <Th>Hash</Th>
              <Th>Ledger</Th>
              <Th align="right">Ops</Th>
              <Th>Result</Th>
              <Th align="right">Fee</Th>
              <Th>Memo</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {data.transactions.map((t: LedgerTransaction) => (
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
                    href={`/ledger?seq=${t.ledger}`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                  >
                    #{t.ledger.toLocaleString()}
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
                    <span className="text-ink-faint">—</span>
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

function OperationsPanel({
  id,
  isLoading,
  isError,
  error,
  data,
}: {
  id: string;
  isLoading: boolean;
  isError: boolean;
  error: unknown;
  data: AccountOperationsResp | undefined;
}) {
  const source = asExample(`/v1/accounts/${id}/operations`, {
    limit: PAGE_SIZE,
  });
  if (isError) {
    return (
      <Panel
        title="Operations"
        source={source}
        bodyClassName="text-sm text-ink-body"
      >
        No operations for that account in the served tier, or the lookup
        failed: {error instanceof Error ? error.message : 'unknown error'}.
      </Panel>
    );
  }
  if (isLoading || !data) {
    return (
      <Panel
        title="Operations"
        source={source}
        bodyClassName="text-sm text-ink-muted"
      >
        Loading…
      </Panel>
    );
  }
  if (data.operations.length === 0) {
    return (
      <Panel
        title="Operations"
        source={source}
        bodyClassName="text-sm text-ink-muted"
      >
        No sourced operations observed for this account.
      </Panel>
    );
  }
  return (
    <Panel
      title={`Sourced operations (${data.operations.length})`}
      source={source}
      bodyClassName="space-y-3"
    >
      {data.operations.map((op: TxOperation, i: number) => (
        <OperationCard key={`${op.tx_hash ?? ''}-${op.op_index}-${i}`} op={op} />
      ))}
    </Panel>
  );
}

function OperationCard({ op }: { op: TxOperation }) {
  const fields = op.fields ?? {};
  const fieldKeys = Object.keys(fields);
  return (
    <div className="rounded-lg border border-line p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="rounded bg-surface-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-body">
          #{op.op_index}
        </span>
        <span className="text-brand-700 rounded bg-brand-50 px-2 py-0.5 text-[11px] font-medium">
          {op.type}
        </span>
        {op.tx_hash && (
          <Link
            href={`/tx?hash=${op.tx_hash}`}
            className="font-mono text-[11px] text-brand-600 hover:underline"
            title={op.tx_hash}
          >
            tx {op.tx_hash.slice(0, 8)}…{op.tx_hash.slice(-6)}
          </Link>
        )}
        {op.ledger != null && (
          <Link
            href={`/ledger?seq=${op.ledger}`}
            className="font-mono text-[11px] text-ink-muted hover:text-brand-600"
          >
            #{op.ledger.toLocaleString()}
          </Link>
        )}
        {op.close_time && (
          <span
            className="font-mono text-[11px] text-ink-faint"
            title={formatTimestamp(op.close_time)}
          >
            {relativeAge(op.close_time)}
          </span>
        )}
      </div>
      {fieldKeys.length > 0 ? (
        <dl className="grid grid-cols-1 gap-x-6 gap-y-1.5 sm:grid-cols-2">
          {fieldKeys.map((k) => (
            <div key={k} className="flex items-baseline gap-2">
              <dt className="shrink-0 text-[11px] uppercase tracking-wider text-ink-muted">
                {k}
              </dt>
              <dd className="break-all font-mono text-xs text-ink-body">
                {renderFieldValue(fields[k])}
              </dd>
            </div>
          ))}
        </dl>
      ) : (
        <p className="text-xs text-ink-faint">No decoded fields.</p>
      )}
      {op.raw_xdr && (
        <details className="mt-2 rounded border border-line">
          <summary className="cursor-pointer px-2 py-1 text-[11px] font-medium text-ink-muted hover:text-brand-600">
            Raw XDR
          </summary>
          <pre className="overflow-x-auto whitespace-pre-wrap break-all border-t border-line px-2 py-2 font-mono text-[10px] leading-relaxed text-ink-body">
            {op.raw_xdr}
          </pre>
        </details>
      )}
    </div>
  );
}

function renderFieldValue(v: unknown): string {
  if (v == null) return '—';
  if (typeof v === 'string' || typeof v === 'number' || typeof v === 'boolean') {
    return String(v);
  }
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
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
