'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import {
  Breadcrumbs,
  Table,
  TableWrap,
  TBody,
  Td,
  Th,
  THead,
  TR,
} from '@/components/ui';
import { apiGet, asExample } from '@/api/client';
import {
  type Envelope,
  type TxSummary,
  type TxOperation,
  type TxEvent,
  CopyHash,
  CopyValue,
  formatTimestamp,
} from '../explorer-shared';

const HASH_RE = /^[0-9a-fA-F]{64}$/;

/**
 * Client view for /operation?tx=H&i=N. Fetches the containing
 * transaction (/v1/tx/{hash}) and isolates the single operation at
 * index N plus the contract events that operation emitted, so an
 * individual op gets a first-class detail page rather than only ever
 * being a row inside the tx view.
 */
export function OperationView() {
  const params = useSearchParams();
  const hash = (params.get('tx') ?? '').trim();
  const idxRaw = params.get('i') ?? '';
  const idx = Number.parseInt(idxRaw, 10);
  const looksValid = HASH_RE.test(hash) && Number.isInteger(idx) && idx >= 0;

  const { data, isLoading, isError, error } = useQuery<TxSummary>({
    queryKey: ['/v1/tx/{hash}', hash],
    enabled: looksValid,
    retry: false,
    queryFn: async () => {
      const env = await apiGet<Envelope<TxSummary>>(`/v1/tx/${encodeURIComponent(hash)}`);
      return env.data;
    },
    staleTime: 5 * 60_000,
  });

  if (hash.length === 0 || idxRaw === '') {
    return (
      <Shell hash={null} idx={null}>
        <Panel title="No operation selected" bodyClassName="text-sm text-ink-body">
          <p>
            This page needs a <code className="font-mono">?tx=</code> (64-char
            transaction hash) and <code className="font-mono">?i=</code>{' '}
            (operation index) query parameter. Open an operation from the{' '}
            <Link href="/operations" className="text-brand-600 hover:underline">
              operations feed
            </Link>
            .
          </p>
        </Panel>
      </Shell>
    );
  }

  if (!looksValid) {
    return (
      <Shell hash={hash} idx={idx}>
        <Panel title="Invalid operation reference" bodyClassName="text-sm text-ink-body">
          <p>
            An operation is addressed by a 64-hex transaction hash and a
            non-negative index. <span className="break-all font-mono">{hash}</span>{' '}
            / <span className="font-mono">{idxRaw}</span> doesn&apos;t parse.
          </p>
        </Panel>
      </Shell>
    );
  }

  if (isError) {
    const status = errorStatus(error);
    return (
      <Shell hash={hash} idx={idx}>
        <Panel
          title={status === 404 ? 'Transaction not found' : 'Lookup failed'}
          source={asExample(`/v1/tx/${hash}`)}
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            {status === 404
              ? 'No transaction with that hash in the served tier.'
              : `Lookup failed: ${error instanceof Error ? error.message : 'unknown error'}.`}
          </p>
        </Panel>
      </Shell>
    );
  }

  if (isLoading || !data) {
    return (
      <Shell hash={hash} idx={idx}>
        <Panel title="Operation" source={asExample(`/v1/tx/${hash}`)} bodyClassName="text-sm text-ink-muted">
          Loading…
        </Panel>
      </Shell>
    );
  }

  const tx = data;
  const op = (tx.operations ?? []).find((o) => o.op_index === idx);
  const opEvents = (tx.events ?? []).filter((e) => e.op_index === idx);

  if (!op) {
    return (
      <Shell hash={tx.hash} idx={idx}>
        <Panel title="Operation not found" source={asExample(`/v1/tx/${tx.hash}`)} bodyClassName="text-sm text-ink-body">
          <p>
            Transaction <span className="font-mono">{tx.hash.slice(0, 12)}…</span>{' '}
            has {(tx.operations ?? []).length} operation(s); index {idx} is out of
            range.{' '}
            <Link href={`/transactions/${tx.hash}/`} className="text-brand-600 hover:underline">
              View the transaction →
            </Link>
          </p>
        </Panel>
      </Shell>
    );
  }

  return (
    <Shell hash={tx.hash} idx={idx} type={op.type}>
      <Panel
        title={op.type}
        hint={formatTimestamp(tx.close_time)}
        source={asExample(`/v1/tx/${tx.hash}`)}
      >
        <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-4">
          <Field label="Operation index" mono value={`#${op.op_index}`} />
          <Field label="Result">
            <OpResultBadge code={op.result_code} />
          </Field>
          <Field label="Ledger">
            <Link href={`/ledgers/${tx.ledger}/`} className="font-mono text-xs text-brand-600 hover:underline">
              #{tx.ledger.toLocaleString()}
            </Link>
          </Field>
          <Field label="Close time" value={formatTimestamp(tx.close_time)} />
          <FieldWide label="Transaction">
            <span className="inline-flex items-center gap-2">
              <Link href={`/transactions/${tx.hash}/`} className="font-mono text-xs text-brand-600 hover:underline" title={tx.hash}>
                {tx.hash.slice(0, 16)}…{tx.hash.slice(-16)}
              </Link>
              <CopyValue value={tx.hash} />
            </span>
          </FieldWide>
          <FieldWide label="Source account">
            {op.source_account ? (
              <span className="inline-flex items-center gap-2">
                <Link
                  href={`/accounts/${encodeURIComponent(op.source_account)}/`}
                  className="font-mono text-xs text-brand-600 hover:underline"
                  title={op.source_account}
                >
                  {op.source_account.slice(0, 12)}…{op.source_account.slice(-8)}
                </Link>
                <CopyValue value={op.source_account} />
              </span>
            ) : (
              <span className="inline-flex items-center gap-2 text-sm text-ink-muted">
                <span className="font-mono text-xs">inherits tx source</span>
                <Link href={`/accounts/${encodeURIComponent(tx.source_account)}/`} className="text-brand-600 hover:underline">
                  <CopyHash value={tx.source_account} head={6} tail={4} />
                </Link>
              </span>
            )}
          </FieldWide>
        </dl>
      </Panel>

      <DecodedFields op={op} />
      <OpEventsPanel hash={tx.hash} events={opEvents} />
    </Shell>
  );
}

function DecodedFields({ op }: { op: TxOperation }) {
  const fields = op.fields ?? {};
  const keys = Object.keys(fields);
  return (
    <Panel title="Decoded body" bodyClassName={keys.length > 0 ? 'space-y-3' : 'text-sm text-ink-muted'}>
      {keys.length === 0 ? (
        'No decoded fields for this operation type.'
      ) : (
        <dl className="grid grid-cols-1 gap-x-6 gap-y-2 sm:grid-cols-2">
          {keys.map((k) => (
            <div key={k} className="flex items-baseline gap-2">
              <dt className="shrink-0 text-[11px] uppercase tracking-wider text-ink-muted">{k}</dt>
              <dd className="break-all font-mono text-xs text-ink-body">{renderFieldValue(fields[k])}</dd>
            </div>
          ))}
        </dl>
      )}
      {op.raw_xdr && (
        <details className="mt-2 rounded-sm border border-line">
          <summary className="cursor-pointer px-2 py-1 text-[11px] font-medium text-ink-muted hover:text-brand-600">
            Raw XDR
          </summary>
          <pre className="overflow-x-auto whitespace-pre-wrap break-all border-t border-line px-2 py-2 font-mono text-[10px] leading-relaxed text-ink-body">
            {op.raw_xdr}
          </pre>
        </details>
      )}
    </Panel>
  );
}

function OpEventsPanel({ hash, events }: { hash: string; events: TxEvent[] }) {
  if (events.length === 0) {
    return (
      <Panel title="Events" source={asExample(`/v1/tx/${hash}`)} bodyClassName="text-sm text-ink-muted">
        This operation emitted no Soroban contract events.
      </Panel>
    );
  }
  return (
    <Panel title={`Events (${events.length})`} source={asExample(`/v1/tx/${hash}`)} bodyClassName="-mx-4 -mb-4">
      <TableWrap className="rounded-none border-0">
        <Table>
          <THead>
            <TR className="hover:bg-transparent">
              <Th>Contract</Th>
              <Th>Event type</Th>
              <Th>Topic 0</Th>
            </TR>
          </THead>
          <TBody>
            {events.map((ev, i) => (
              <TR key={`${ev.event_index ?? i}`}>
                <Td>
                  <Link
                    href={`/contracts/${ev.contract_id}/`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                    title={ev.contract_id}
                  >
                    {ev.contract_id.slice(0, 8)}…{ev.contract_id.slice(-6)}
                  </Link>
                </Td>
                <Td className="font-mono text-xs text-ink-body">{ev.event_type || '—'}</Td>
                <Td className="font-mono text-xs text-ink-muted">{ev.topic_0 || '—'}</Td>
              </TR>
            ))}
          </TBody>
        </Table>
      </TableWrap>
    </Panel>
  );
}

function Shell({
  hash,
  idx,
  type,
  children,
}: {
  hash: string | null;
  idx: number | null;
  type?: string;
  children: React.ReactNode;
}) {
  const last =
    hash && idx != null
      ? `${hash.slice(0, 8)}…#${idx}`
      : 'operation';
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <Breadcrumbs
          items={[
            { label: 'Home', href: '/' },
            { label: 'Operations', href: '/operations' },
            ...(hash ? [{ label: 'Transaction', href: `/transactions/${hash}/` }] : []),
            { label: last },
          ]}
        />
        <h1 className="text-2xl font-semibold tracking-tight">
          {type ? type : 'Operation'}
        </h1>
      </header>
      {children}
    </div>
  );
}

function OpResultBadge({ code }: { code?: number }) {
  if (code == null) {
    return <span className="text-sm text-ink-muted">—</span>;
  }
  const ok = code === 0;
  return (
    <span
      className={`inline-flex items-center rounded px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider ${
        ok ? 'bg-up-subtle text-up' : 'bg-down-subtle text-down'
      }`}
      title={ok ? 'success' : `code ${code}`}
    >
      {ok ? 'success' : `code ${code}`}
    </span>
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
      <dt className="text-[11px] uppercase tracking-wider text-ink-muted">{label}</dt>
      <dd className={mono ? 'mt-0.5 break-all font-mono text-xs' : 'mt-0.5'}>{children ?? value ?? '—'}</dd>
    </div>
  );
}

function FieldWide({ label, children }: { label: string; children: React.ReactNode }) {
  return (
    <div className="col-span-2 sm:col-span-3 lg:col-span-4">
      <dt className="text-[11px] uppercase tracking-wider text-ink-muted">{label}</dt>
      <dd className="mt-0.5">{children}</dd>
    </div>
  );
}
