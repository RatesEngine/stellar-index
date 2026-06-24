'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { Breadcrumbs } from '@/components/ui';
import { apiGet, asExample } from '@/api/client';
import {
  type Envelope,
  type TxSummary,
  type TxOperation,
  type TxEvent,
  CopyHash,
  CopyValue,
  formatTimestamp,
  stroopsToXlm,
} from '../explorer-shared';

// A 64-char lowercase hex tx hash. The API may also accept other
// casings, but this is the canonical shape; anything else we treat
// as a likely-bad-hash 400 hint.
const HASH_RE = /^[0-9a-fA-F]{64}$/;

/**
 * Client view for /tx?hash=H. Fetches /v1/tx/{hash} (summary +
 * operations + events in one response) and renders the three
 * sections. Handles 404 (not found) and 400 (bad hash) distinctly.
 */
export function TxView({ hash: hashProp }: { hash?: string } = {}) {
  // The path route /transactions/[hash] passes `hash` as a prop; the legacy
  // /tx?hash= route (kept for redirect compatibility) reads it from the query
  // string. Prop wins when provided.
  const params = useSearchParams();
  const hash = (hashProp ?? params.get('hash') ?? '').trim();
  const looksValid = HASH_RE.test(hash);

  const { data, isLoading, isError, error } = useQuery<TxSummary>({
    queryKey: ['/v1/tx/{hash}', hash],
    enabled: hash.length > 0,
    retry: false,
    queryFn: async () => {
      const env = await apiGet<Envelope<TxSummary>>(
        `/v1/tx/${encodeURIComponent(hash)}`,
      );
      return env.data;
    },
    staleTime: 5 * 60_000,
  });

  if (hash.length === 0) {
    return (
      <Shell hash={null}>
        <Panel
          title="No transaction selected"
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            This page needs a <code className="font-mono">?hash=</code> query
            parameter — a 64-character transaction hash. Use the search box (
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
    // 400-class: the input doesn't look like a tx hash at all.
    return (
      <Shell hash={hash}>
        <Panel
          title="Invalid transaction hash"
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            <span className="break-all font-mono">{hash}</span> isn&apos;t a
            valid transaction hash. Stellar tx hashes are 64 hexadecimal
            characters.
          </p>
        </Panel>
      </Shell>
    );
  }

  if (isError) {
    const status = errorStatus(error);
    return (
      <Shell hash={hash}>
        <Panel
          title={
            status === 400
              ? 'Invalid transaction hash'
              : 'Transaction not found'
          }
          source={asExample(`/v1/tx/${hash}`)}
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            {status === 404
              ? 'No transaction with that hash in the served tier.'
              : status === 400
                ? 'The API rejected that hash as malformed.'
                : `Lookup failed: ${error instanceof Error ? error.message : 'unknown error'}.`}
          </p>
        </Panel>
      </Shell>
    );
  }

  if (isLoading || !data) {
    return (
      <Shell hash={hash}>
        <Panel
          title="Transaction"
          source={asExample(`/v1/tx/${hash}`)}
          bodyClassName="text-sm text-ink-muted"
        >
          Loading…
        </Panel>
      </Shell>
    );
  }

  const tx = data;

  return (
    <Shell hash={tx.hash}>
      <Panel
        title="Transaction"
        hint={formatTimestamp(tx.close_time)}
        source={asExample(`/v1/tx/${tx.hash}`)}
      >
        <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-4">
          <FieldWide label="Hash">
            <CopyHash value={tx.hash} head={16} tail={16} />
          </FieldWide>
          <Field label="Ledger">
            <Link
              href={`/ledger?seq=${tx.ledger}`}
              className="font-mono text-xs text-brand-600 hover:underline"
            >
              #{tx.ledger.toLocaleString()}
            </Link>
          </Field>
          <Field label="Close time" value={formatTimestamp(tx.close_time)} />
          <Field label="Result">
            <SuccessBadge ok={tx.successful} code={tx.result_code} />
          </Field>
          <Field
            label="Fee charged"
            mono
            value={
              tx.fee_charged != null
                ? `${stroopsToXlm(tx.fee_charged)} XLM`
                : '—'
            }
          />
          <Field
            label="Max fee"
            mono
            value={tx.max_fee != null ? `${stroopsToXlm(tx.max_fee)} XLM` : '—'}
          />
          <Field
            label="Memo"
            mono
            value={
              tx.memo_type && tx.memo_type !== 'none'
                ? `${tx.memo_type}${tx.memo ? `: ${tx.memo}` : ''}`
                : '—'
            }
          />
          <FieldWide label="Source account">
            <span className="inline-flex items-center gap-2">
              <Link
                href={`/accounts?id=${encodeURIComponent(tx.source_account)}`}
                className="font-mono text-xs text-brand-600 hover:underline"
                title={tx.source_account}
              >
                {tx.source_account.slice(0, 12)}…{tx.source_account.slice(-8)}
              </Link>
              <CopyValue value={tx.source_account} />
            </span>
          </FieldWide>
        </dl>
      </Panel>

      <OperationsPanel hash={tx.hash} operations={tx.operations ?? []} />
      <EventsPanel hash={tx.hash} events={tx.events ?? []} />
    </Shell>
  );
}

function Shell({
  hash,
  children,
}: {
  hash: string | null;
  children: React.ReactNode;
}) {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <Breadcrumbs
          items={[
            { label: 'Home', href: '/' },
            { label: 'Transactions', href: '/transactions' },
            { label: hash ? `${hash.slice(0, 8)}…${hash.slice(-6)}` : 'tx' },
          ]}
        />
        <h1 className="text-2xl font-semibold tracking-tight">Transaction</h1>
      </header>
      {children}
    </div>
  );
}

function OperationsPanel({
  hash,
  operations,
}: {
  hash: string;
  operations: TxOperation[];
}) {
  const source = asExample(`/v1/tx/${hash}`);
  if (operations.length === 0) {
    return (
      <Panel
        title="Operations"
        source={source}
        bodyClassName="text-sm text-ink-muted"
      >
        No operations on this transaction.
      </Panel>
    );
  }
  return (
    <Panel
      title={`Operations (${operations.length})`}
      source={source}
      bodyClassName="space-y-3"
    >
      {operations.map((op, i) => (
        <OperationCard key={`${op.op_index}-${i}`} hash={hash} op={op} />
      ))}
    </Panel>
  );
}

function OperationCard({ hash, op }: { hash: string; op: TxOperation }) {
  const fields = op.fields ?? {};
  const fieldKeys = Object.keys(fields);
  return (
    <div className="rounded-lg border border-line p-3">
      <div className="mb-2 flex flex-wrap items-center gap-2">
        <span className="rounded bg-surface-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-body">
          #{op.op_index}
        </span>
        <Link
          href={`/operation?tx=${hash}&i=${op.op_index}`}
          className="text-brand-700 rounded bg-brand-50 px-2 py-0.5 text-[11px] font-medium hover:bg-brand-100"
          title="Operation detail"
        >
          {op.type}
        </Link>
        {op.result_code != null && (
          <span
            // result_code is a numeric XDR code: 0 = opSUCCESS. Gate on
            // `!= null` (0 is falsy) and derive success from `=== 0`,
            // never from truthiness or a regex on the number.
            className={`rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider ${
              op.result_code === 0
                ? 'bg-up-subtle text-up'
                : 'bg-down-subtle text-down'
            }`}
            title={op.result_code === 0 ? 'success' : `code ${op.result_code}`}
          >
            {op.result_code === 0 ? 'success' : `code ${op.result_code}`}
          </span>
        )}
        {op.source_account && (
          <span
            className="font-mono text-[11px] text-ink-muted"
            title={op.source_account}
          >
            src {op.source_account.slice(0, 6)}…{op.source_account.slice(-4)}
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
  if (
    typeof v === 'string' ||
    typeof v === 'number' ||
    typeof v === 'boolean'
  ) {
    return String(v);
  }
  try {
    return JSON.stringify(v);
  } catch {
    return String(v);
  }
}

function EventsPanel({ hash, events }: { hash: string; events: TxEvent[] }) {
  const source = asExample(`/v1/tx/${hash}`);
  if (events.length === 0) {
    return (
      <Panel
        title="Events"
        source={source}
        bodyClassName="text-sm text-ink-muted"
      >
        This transaction emitted no Soroban contract events.
      </Panel>
    );
  }
  return (
    <Panel
      title={`Events (${events.length})`}
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <Th align="right">Op</Th>
              <Th>Contract</Th>
              <Th>Event type</Th>
              <Th>Topic 0</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {events.map((ev, i) => (
              <tr
                key={`${ev.op_index}-${ev.event_index ?? i}`}
                className="hover:bg-surface-muted"
              >
                <Td align="right">
                  <span className="font-mono tabular-nums text-ink-muted">
                    {ev.op_index}
                  </span>
                </Td>
                <Td>
                  <Link
                    href={`/contract?id=${ev.contract_id}`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                    title={ev.contract_id}
                  >
                    {ev.contract_id.slice(0, 8)}…{ev.contract_id.slice(-6)}
                  </Link>
                </Td>
                <Td>
                  <span className="font-mono text-xs text-ink-body">
                    {ev.event_type || '—'}
                  </span>
                </Td>
                <Td>
                  <span className="font-mono text-xs text-ink-muted">
                    {ev.topic_0 || '—'}
                  </span>
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

// SuccessBadge renders the transaction-level result. Success is driven
// by the `successful` bool (the authoritative tx-level signal); the
// numeric XDR `code` is shown as detail on failure / hover.
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

// errorStatus pulls the HTTP status out of the apiGet error message
// ("<status> <statusText> on <path>"). Returns null when it can't
// parse one.
function errorStatus(err: unknown): number | null {
  if (!(err instanceof Error)) return null;
  const m = err.message.match(/^(\d{3})\b/);
  return m ? Number(m[1]) : null;
}

function Field({
  label,
  value,
  mono,
  children,
}: {
  label: string;
  value?: string;
  mono?: boolean;
  children?: React.ReactNode;
}) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-ink-muted">
        {label}
      </dt>
      <dd className={mono ? 'mt-0.5 break-all font-mono text-xs' : 'mt-0.5'}>
        {children ?? value ?? '—'}
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
