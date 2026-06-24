'use client';

import { useState } from 'react';
import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQuery, keepPreviousData } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { Breadcrumbs } from '@/components/ui';
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
export function ContractView({ id: idProp }: { id?: string } = {}) {
  // Path route /contracts/[id] passes `id` as a prop; legacy /contract?id= reads
  // it from the query string (kept for redirect compatibility). Prop wins.
  const params = useSearchParams();
  const id = (idProp ?? params.get('id') ?? '').trim();
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
          bodyClassName="text-sm text-ink-body"
        >
          <p>
            This page needs an <code className="font-mono">?id=</code> query
            parameter — a 56-character Soroban contract ID (starts with{' '}
            <code className="font-mono">C</code>). Use the search box (
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
          title="Invalid contract ID"
          bodyClassName="text-sm text-ink-body"
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
          bodyClassName="text-sm text-ink-body"
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
          bodyClassName="text-sm text-ink-muted"
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
          <div className="text-[11px] uppercase tracking-wider text-ink-muted">
            Contract ID
          </div>
          <div className="mt-0.5">
            <CopyHash value={data.contract_id || id} head={16} tail={16} />
          </div>
        </div>
        <ul className="flex flex-wrap gap-x-6 gap-y-1 text-xs text-ink-body">
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

      <WasmPanel id={data.contract_id || id} />

      <CodeHistoryPanel id={data.contract_id || id} />

      <InteractionsPanel id={data.contract_id || id} />

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

// ── On-chain WASM ("see the code") ──────────────────────────────────────
// Mirrors api/v1.ContractWasmView (GET /v1/contracts/{id}/wasm): the
// contract's resolved wasm hash + size, its exported function table (always
// present — parsed natively), and best-effort WAT + wasm-decompile pseudocode
// (present once the wabt toolchain is installed server-side).
interface WasmExport {
  name: string;
  params: string[];
  results: string[];
}
interface ContractWasmResp {
  contract_id: string;
  wasm_hash: string;
  size_bytes: number;
  exports: WasmExport[];
  wat?: string;
  decompiled?: string;
  source_note: string;
}

function exportSignature(e: WasmExport): string {
  const p = e.params.length ? e.params.join(', ') : '';
  const r = e.results.length ? e.results.join(', ') : '()';
  return `(${p}) → ${r}`;
}

function formatBytes(n: number): string {
  if (n < 1024) return `${n} B`;
  if (n < 1024 * 1024) return `${(n / 1024).toFixed(1)} KiB`;
  return `${(n / (1024 * 1024)).toFixed(2)} MiB`;
}

/**
 * WasmPanel — the contract's on-chain code, read on demand from the certified
 * lake. Exports always render; WAT + decompiled pseudocode are collapsible and
 * only shown when the server produced them (wabt present). A clean 404 (the
 * contract's instance/code entry isn't in the captured ledger window) degrades
 * to a short, non-alarming note rather than an error card.
 */
function WasmPanel({ id }: { id: string }) {
  const { data, isLoading, isError, error } = useQuery<ContractWasmResp>({
    queryKey: ['/v1/contracts/{id}/wasm', id],
    enabled: CONTRACT_RE.test(id),
    retry: false,
    staleTime: 3_600_000, // content-addressed wasm is immutable
    queryFn: async () => {
      const env = await apiGet<Envelope<ContractWasmResp>>(
        `/v1/contracts/${encodeURIComponent(id)}/wasm`,
      );
      return env.data;
    },
  });

  const source = asExample(`/v1/contracts/${id}/wasm`);

  if (isLoading) {
    return (
      <Panel title="Code (WASM)" source={source} bodyClassName="text-sm text-ink-muted">
        Loading code…
      </Panel>
    );
  }

  if (isError || !data) {
    const msg = error instanceof Error ? error.message : '';
    const isSac = msg.includes('Stellar Asset Contract');
    const notCaptured = !isSac && msg.includes('404');
    let body: string;
    if (isSac) {
      body =
        'This is a Stellar Asset Contract — the built-in SAC host logic behind a classic asset (e.g. XLM or USDC). It runs no user-uploaded WASM module, so there’s no bytecode to show.';
    } else if (notCaptured) {
      body =
        'This contract’s on-chain WASM isn’t in the captured ledger window yet — its deploy-time code/instance entry predates live capture. It resolves automatically once a Phase-C backfill lands.';
    } else {
      body = `Couldn’t load this contract’s WASM: ${msg || 'unknown error'}.`;
    }
    return (
      <Panel
        title="Code (WASM)"
        source={source}
        bodyClassName="text-sm text-ink-body"
      >
        <p>{body}</p>
      </Panel>
    );
  }

  return (
    <Panel
      title="Code (WASM)"
      hint={`${data.exports.length} exports · ${formatBytes(data.size_bytes)}`}
      source={source}
      bodyClassName="space-y-4"
    >
      <div className="flex flex-wrap gap-x-8 gap-y-2 text-xs">
        <div>
          <div className="text-[11px] uppercase tracking-wider text-ink-muted">
            WASM hash
          </div>
          <div className="mt-0.5">
            <CopyHash value={data.wasm_hash} head={12} tail={10} />
          </div>
        </div>
        <div>
          <div className="text-[11px] uppercase tracking-wider text-ink-muted">
            Size
          </div>
          <div className="mt-0.5 font-mono tabular-nums text-ink-body">
            {formatBytes(data.size_bytes)}
          </div>
        </div>
      </div>

      {/* Exported entry points — the contract's real API surface. */}
      {data.exports.length > 0 && (
        <div className="overflow-x-auto">
          <table className="min-w-full divide-y divide-line text-sm">
            <thead>
              <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
                <th scope="col" className="py-2 pr-4">
                  Export
                </th>
                <th scope="col" className="py-2">
                  Signature (wasm ABI)
                </th>
              </tr>
            </thead>
            <tbody className="divide-y divide-line-subtle">
              {data.exports.map((e) => (
                <tr key={e.name}>
                  <td className="py-1.5 pr-4 font-mono text-brand-700">
                    {e.name}
                  </td>
                  <td className="py-1.5 font-mono text-xs text-ink-muted">
                    {exportSignature(e)}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {data.wat && <CodeDisclosure label="WAT disassembly" code={data.wat} />}
      {data.decompiled && (
        <CodeDisclosure label="Decompiled pseudocode" code={data.decompiled} />
      )}

      <p className="text-[11px] leading-snug text-ink-faint">{data.source_note}</p>
    </Panel>
  );
}

// CodeDisclosure — a collapsed <details> block of monospace source, scrollable
// and height-capped so a large module doesn't dominate the page.
function CodeDisclosure({ label, code }: { label: string; code: string }) {
  return (
    <details className="group rounded-lg border border-line">
      <summary className="cursor-pointer select-none px-3 py-2 text-xs font-medium text-ink-body marker:text-ink-faint hover:text-brand-600">
        {label}{' '}
        <span className="text-ink-faint">
          ({code.split('\n').length.toLocaleString()} lines)
        </span>
      </summary>
      <pre className="max-h-96 overflow-auto border-t border-line bg-surface-muted p-3 text-[11px] leading-relaxed text-ink-body">
        {code}
      </pre>
    </details>
  );
}

// ── Code history (change over time) ─────────────────────────────────────
// Mirrors api/v1.ContractCodeHistoryView (GET /v1/contracts/{id}/code-history):
// the contract's WASM-hash timeline — each in-place upgrade as a new version.
interface CodeVersion {
  ledger: number;
  close_time: string;
  wasm_hash: string;
}
interface CodeHistoryResp {
  contract_id: string;
  versions: CodeVersion[];
}

function CodeHistoryPanel({ id }: { id: string }) {
  const { data, isLoading, isError } = useQuery<CodeHistoryResp>({
    queryKey: ['/v1/contracts/{id}/code-history', id],
    enabled: CONTRACT_RE.test(id),
    retry: false,
    staleTime: 600_000,
    queryFn: async () => {
      const env = await apiGet<Envelope<CodeHistoryResp>>(
        `/v1/contracts/${encodeURIComponent(id)}/code-history`,
      );
      return env.data;
    },
  });

  const source = asExample(`/v1/contracts/${id}/code-history`);
  const versions = data?.versions ?? [];

  if (isLoading) {
    return (
      <Panel title="Code history" source={source} bodyClassName="text-sm text-ink-muted">
        Loading upgrade history…
      </Panel>
    );
  }
  if (isError || versions.length === 0) {
    return (
      <Panel title="Code history" source={source} bodyClassName="text-sm text-ink-muted">
        No code changes in the captured ledger window — the contract’s
        instance/upgrade entries aren’t captured yet (fills in with the
        Phase-C backfill).
      </Panel>
    );
  }
  return (
    <Panel
      title={`Code history (${versions.length} version${versions.length === 1 ? '' : 's'})`}
      hint={versions.length > 1 ? 'in-place upgrades over time' : 'deployed executable'}
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <th scope="col" className="px-4 py-2">#</th>
              <th scope="col" className="px-4 py-2">From ledger</th>
              <th scope="col" className="px-4 py-2">When</th>
              <th scope="col" className="px-4 py-2">WASM hash</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {versions.map((v, i) => (
              <tr key={`${v.ledger}-${v.wasm_hash}`} className="hover:bg-surface-muted">
                <td className="px-4 py-3 font-mono text-xs text-ink-faint">{i + 1}</td>
                <td className="px-4 py-3">
                  <Link href={`/ledger?seq=${v.ledger}`} className="font-mono text-xs text-brand-600 hover:underline">
                    #{v.ledger.toLocaleString()}
                  </Link>
                </td>
                <td className="px-4 py-3 text-xs text-ink-muted" title={formatTimestamp(v.close_time)}>
                  {relativeAge(v.close_time)}
                </td>
                <td className="px-4 py-3 font-mono text-xs text-ink-body" title={v.wasm_hash}>
                  {v.wasm_hash.slice(0, 12)}…{v.wasm_hash.slice(-8)}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
  );
}

// ── Interaction map ─────────────────────────────────────────────────────
// Mirrors api/v1.ContractInteractionsView (GET /v1/contracts/{id}/interactions):
// the contracts that emitted events in the same transactions as this one — a
// proxy for cross-contract calls (Soroban sub-invocations nest within one tx).
interface InteractionEdge {
  contract_id: string;
  shared_txs: number;
  protocol?: string;
}
interface InteractionsResp {
  contract_id: string;
  window_days: number;
  since_ledger: number;
  interactions: InteractionEdge[];
}

/**
 * InteractionsPanel — the contract's cross-contract interaction map: other
 * contracts that co-occur in its transactions, ranked by shared-tx count and
 * tagged with their protocol where known. The 90-day window keeps it scoped to
 * current behaviour.
 */
function InteractionsPanel({ id }: { id: string }) {
  const { data, isLoading, isError } = useQuery<InteractionsResp>({
    queryKey: ['/v1/contracts/{id}/interactions', id],
    enabled: CONTRACT_RE.test(id),
    retry: false,
    staleTime: 60_000,
    queryFn: async () => {
      const env = await apiGet<Envelope<InteractionsResp>>(
        `/v1/contracts/${encodeURIComponent(id)}/interactions`,
        { days: 90, limit: 50 },
      );
      return env.data;
    },
  });

  const source = asExample(`/v1/contracts/${id}/interactions`, { days: 90, limit: 50 });
  const edges = data?.interactions ?? [];

  if (isLoading) {
    return (
      <Panel title="Interaction map" source={source} bodyClassName="text-sm text-ink-muted">
        Loading interactions…
      </Panel>
    );
  }
  if (isError || edges.length === 0) {
    return (
      <Panel title="Interaction map" source={source} bodyClassName="text-sm text-ink-muted">
        No cross-contract interactions observed in the last 90 days — this
        contract didn’t share transactions with other contracts in the window.
      </Panel>
    );
  }

  return (
    <Panel
      title={`Interaction map (${edges.length})`}
      hint="contracts sharing transactions · last 90d"
      source={source}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <th scope="col" className="px-4 py-2">Contract</th>
              <th scope="col" className="px-4 py-2">Protocol</th>
              <th scope="col" className="px-4 py-2 text-right">Shared txs</th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {edges.map((e) => (
              <tr key={e.contract_id} className="hover:bg-surface-muted">
                <td className="px-4 py-3">
                  <Link
                    href={`/contract?id=${encodeURIComponent(e.contract_id)}`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                    title={e.contract_id}
                  >
                    {e.contract_id.slice(0, 8)}…{e.contract_id.slice(-6)}
                  </Link>
                </td>
                <td className="px-4 py-3">
                  {e.protocol ? (
                    <Link
                      href={`/protocols/${encodeURIComponent(e.protocol)}`}
                      className="inline-flex items-center rounded bg-brand-100 px-1.5 py-0.5 font-mono text-[10px] uppercase tracking-wider text-brand-800 hover:bg-brand-200"
                    >
                      {e.protocol}
                    </Link>
                  ) : (
                    <span className="text-ink-faint">—</span>
                  )}
                </td>
                <td className="px-4 py-3 text-right font-mono tabular-nums text-ink-body">
                  {e.shared_txs.toLocaleString()}
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
    </Panel>
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
        <Breadcrumbs
          items={[
            { label: 'Home', href: '/' },
            { label: 'Contracts', href: '/contracts' },
            { label: id ? `${id.slice(0, 8)}…${id.slice(-6)}` : 'contract' },
          ]}
        />
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
        bodyClassName="text-sm text-ink-muted"
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
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <Th>Ledger</Th>
              <Th>Close time</Th>
              <Th>Tx</Th>
              <Th>Event type</Th>
              <Th>Topic 0</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {events.map((ev, i) => (
              <tr
                key={`${ev.tx_hash}-${ev.op_index}-${ev.event_index ?? i}`}
                className="hover:bg-surface-muted"
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
                    className="font-mono text-xs text-ink-muted"
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

      <div className="flex items-center justify-between px-4 pb-1 pt-4 text-xs">
        <button
          type="button"
          onClick={onNewest}
          disabled={cursor === undefined || isFetching}
          className="rounded-md border border-line px-3 py-1.5 text-ink-body hover:border-brand-500 hover:text-brand-600 disabled:cursor-not-allowed disabled:opacity-40"
        >
          ← Newest
        </button>
        <span className="font-mono text-[11px] text-ink-faint">
          {isFetching ? 'Loading…' : ''}
        </span>
        <button
          type="button"
          onClick={onOlder}
          disabled={nextCursor == null || isFetching}
          className="rounded-md border border-line px-3 py-1.5 text-ink-body hover:border-brand-500 hover:text-brand-600 disabled:cursor-not-allowed disabled:opacity-40"
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
