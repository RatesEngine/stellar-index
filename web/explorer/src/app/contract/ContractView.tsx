'use client';

import { useState } from 'react';
import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQuery, keepPreviousData } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { apiGet, asExample, API_BASE_URL } from '@/api/client';
import {
  type Envelope,
  type ContractResp,
  CopyHash,
  formatTimestamp,
  relativeAge,
} from '../explorer-shared';

// Stellar contract IDs are 56 chars: 'C' + 55 base32 alphanumerics.
const CONTRACT_RE = /^C[A-Z2-7]{55}$/;

const PAGE_SIZE = 50;

/**
 * Client view for /contract?id=C…. Fetches /v1/contracts/{id} (the
 * contract's recent events) with "Load older" walking the opaque
 * next_cursor backwards. The cursor is the composite
 * (ledger, op_index, event_index) keyset — a ledger-only cursor would
 * drop the rest of a ledger that a busy contract straddles across a page.
 */
export function ContractView() {
  const params = useSearchParams();
  const id = (params.get('id') ?? '').trim();
  const looksValid = CONTRACT_RE.test(id);

  const [cursor, setCursor] = useState<string | undefined>(undefined);

  const { data, isLoading, isError, error, isFetching } =
    useQuery<ContractResp>({
      queryKey: ['/v1/contracts/{id}', id, cursor ?? 'tip'],
      enabled: id.length > 0 && looksValid,
      retry: false,
      placeholderData: keepPreviousData,
      queryFn: async () => {
        const env = await apiGet<Envelope<ContractResp>>(
          `/v1/contracts/${encodeURIComponent(id)}`,
          {
            limit: PAGE_SIZE,
            ...(cursor !== undefined ? { cursor } : {}),
          },
        );
        return env.data;
      },
      staleTime: 30_000,
    });

  if (id.length === 0) {
    return (
      <Shell id={null}>
        <Panel
          title="No contract selected"
          bodyClassName="text-sm text-slate-600 dark:text-slate-400"
        >
          <p>
            This page needs an <code className="font-mono">?id=</code> query
            parameter — a 56-character Soroban contract ID (starts with{' '}
            <code className="font-mono">C</code>). Use the search box (
            <kbd className="rounded border border-slate-300 px-1 text-[10px] dark:border-slate-700">
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
          title="Invalid contract ID"
          bodyClassName="text-sm text-slate-600 dark:text-slate-400"
        >
          <p>
            <span className="break-all font-mono">{id}</span> isn&apos;t a valid
            Soroban contract ID. Contract IDs are 56 characters, starting with{' '}
            <code className="font-mono">C</code>.
          </p>
        </Panel>
      </Shell>
    );
  }

  const source = asExample(`/v1/contracts/${id}`, {
    limit: PAGE_SIZE,
    ...(cursor !== undefined ? { cursor } : {}),
  });

  if (isError) {
    return (
      <Shell id={id}>
        <Panel
          title="Contract not found"
          source={source}
          bodyClassName="text-sm text-slate-600 dark:text-slate-400"
        >
          <p>
            No events for that contract in the served tier, or the lookup
            failed: {error instanceof Error ? error.message : 'unknown error'}.
          </p>
        </Panel>
      </Shell>
    );
  }

  if (isLoading || !data) {
    return (
      <Shell id={id}>
        <Panel
          title="Contract"
          source={source}
          bodyClassName="text-sm text-slate-500"
        >
          Loading…
        </Panel>
      </Shell>
    );
  }

  return (
    <Shell id={data.contract_id || id}>
      <Panel
        title="Contract"
        source={asExample(`/v1/contracts/${id}`)}
        bodyClassName="space-y-3"
      >
        <div>
          <div className="text-[11px] uppercase tracking-wider text-slate-500">
            Contract ID
          </div>
          <div className="mt-0.5">
            <CopyHash value={data.contract_id || id} head={16} tail={16} />
          </div>
        </div>
        <ul className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-slate-600 dark:text-slate-400">
          <li>
            <a
              href={`https://stellar.expert/explorer/public/contract/${data.contract_id || id}`}
              target="_blank"
              rel="noreferrer noopener"
              className="hover:text-brand-600 hover:underline"
            >
              stellar.expert ↗
            </a>
          </li>
          <li>
            <a
              href={`${API_BASE_URL}/v1/contracts/${encodeURIComponent(data.contract_id || id)}/transfers`}
              target="_blank"
              rel="noreferrer noopener"
              className="hover:text-brand-600 hover:underline"
              title="SEP-41 transfer/mint/burn flows for this contract"
            >
              Transfers (API) ↗
            </a>
          </li>
        </ul>
      </Panel>

      <EventsPanel
        id={id}
        events={data.events}
        nextCursor={data.next_cursor}
        cursor={cursor}
        isFetching={isFetching}
        onNewest={() => setCursor(undefined)}
        onOlder={() => {
          if (data.next_cursor != null) setCursor(data.next_cursor);
        }}
        source={source}
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
        <nav className="text-xs text-slate-500">
          <Link href="/dexes" className="hover:text-brand-600">
            Contracts
          </Link>{' '}
          /{' '}
          <span className="font-mono text-slate-700 dark:text-slate-300">
            {id ? `${id.slice(0, 8)}…${id.slice(-6)}` : 'contract'}
          </span>
        </nav>
        <h1 className="text-2xl font-semibold tracking-tight">Contract</h1>
      </header>
      {children}
    </div>
  );
}

function EventsPanel({
  events,
  nextCursor,
  cursor,
  isFetching,
  onNewest,
  onOlder,
  source,
}: {
  id: string;
  events: ContractResp['events'];
  nextCursor?: string;
  cursor?: string;
  isFetching: boolean;
  onNewest: () => void;
  onOlder: () => void;
  source: ReturnType<typeof asExample>;
}) {
  if (events.length === 0) {
    return (
      <Panel
        title="Recent events"
        source={source}
        bodyClassName="text-sm text-slate-500"
      >
        No events observed for this contract.
      </Panel>
    );
  }
  return (
    <Panel
      title={`Recent events (${events.length})`}
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
              <Th>Ledger</Th>
              <Th>Close time</Th>
              <Th>Tx</Th>
              <Th>Event type</Th>
              <Th>Topic 0</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {events.map((ev, i) => (
              <tr
                key={`${ev.tx_hash}-${ev.op_index}-${ev.event_index ?? i}`}
                className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
              >
                <Td>
                  <Link
                    href={`/ledger?seq=${ev.ledger}`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                  >
                    #{ev.ledger.toLocaleString()}
                  </Link>
                </Td>
                <Td>
                  <span
                    className="font-mono text-xs text-slate-500"
                    title={formatTimestamp(ev.close_time)}
                  >
                    {relativeAge(ev.close_time)}
                  </span>
                </Td>
                <Td>
                  <Link
                    href={`/tx?hash=${ev.tx_hash}`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                    title={ev.tx_hash}
                  >
                    {ev.tx_hash.slice(0, 8)}…{ev.tx_hash.slice(-6)}
                  </Link>
                </Td>
                <Td>
                  <span className="font-mono text-xs text-slate-700 dark:text-slate-300">
                    {ev.event_type || '—'}
                  </span>
                </Td>
                <Td>
                  <span className="font-mono text-xs text-slate-500">
                    {ev.topic_0 || '—'}
                  </span>
                </Td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>

      <div className="flex items-center justify-between px-4 pb-1 pt-4 text-xs">
        <button
          type="button"
          onClick={onNewest}
          disabled={cursor === undefined || isFetching}
          className="rounded-md border border-slate-200 px-3 py-1.5 text-slate-600 hover:border-brand-500 hover:text-brand-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-300"
        >
          ← Newest
        </button>
        <span className="font-mono text-[11px] text-slate-400">
          {isFetching ? 'Loading…' : ''}
        </span>
        <button
          type="button"
          onClick={onOlder}
          disabled={nextCursor == null || isFetching}
          className="rounded-md border border-slate-200 px-3 py-1.5 text-slate-600 hover:border-brand-500 hover:text-brand-600 disabled:cursor-not-allowed disabled:opacity-40 dark:border-slate-700 dark:text-slate-300"
        >
          Load older →
        </button>
      </div>
    </Panel>
  );
}

function Th({ children }: { children: React.ReactNode }) {
  return (
    <th className="px-4 py-2 text-left" scope="col">
      {children}
    </th>
  );
}

function Td({ children }: { children: React.ReactNode }) {
  return <td className="px-4 py-3 text-left">{children}</td>;
}
