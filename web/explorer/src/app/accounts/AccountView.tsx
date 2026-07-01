'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';
import { useQuery } from '@tanstack/react-query';

import { Panel } from '@/components/reveal';
import { Breadcrumbs } from '@/components/ui';
import { AccountPositions } from './AccountPositions';
import { useIssuers } from '@/api/hooks';
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
 * Client view for /accounts?id=G…. Fetches the account's transactions
 * and operations in parallel and renders them.
 *
 * SCOPE: "all" (ADR-0038 Phase B) — both what the account sourced and
 * where it's a non-source participant (incoming payments, trustlines,
 * merges, …). The backend stamps a `scope` field; incoming coverage
 * tracks the participant-index capture + backfill.
 */
export function AccountView({ id: idProp }: { id?: string } = {}) {
  // Path route /accounts/[g] passes `id` as a prop; legacy /accounts?id= reads
  // it from the query string (kept for redirect compatibility). Prop wins.
  const params = useSearchParams();
  const id = (idProp ?? params.get('id') ?? '').trim();
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

  const stateQ = useQuery<AccountStateResp>({
    queryKey: ['/v1/accounts/{id}', id],
    enabled: id.length > 0 && looksValid,
    retry: false,
    queryFn: async () => {
      const env = await apiGet<Envelope<AccountStateResp>>(
        `/v1/accounts/${encodeURIComponent(id)}`,
      );
      return env.data;
    },
    staleTime: 30_000,
  });

  // Is this account a known asset issuer? We match against the top
  // issuers list — the SAME set /issuers/[g_strkey] pre-renders via
  // generateStaticParams — so an internal link is guaranteed to
  // resolve under static export (no 404 on an un-prerendered route).
  const issuersQ = useIssuers(100);
  const isKnownIssuer = (issuersQ.data ?? []).some((iss) => iss.g_strkey === id);

  if (id.length === 0) {
    return <AccountsDirectory />;
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
        <ul className="flex flex-wrap items-center gap-x-6 gap-y-1 text-xs text-ink-body">
          {isKnownIssuer && (
            <li>
              <Link
                href={`/issuers/${encodeURIComponent(id)}`}
                className="font-medium text-brand-600 hover:underline"
              >
                View as issuer — issued assets &amp; auth flags →
              </Link>
            </li>
          )}
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
        <p className="rounded-md border border-line bg-surface-muted px-3 py-2 text-xs text-ink-muted">
          Balances + trustlines + offers below reflect the lake&apos;s captured
          ledger-entry window; the activity tables show{' '}
          <strong>all</strong> history — both what the account sourced and where
          it&apos;s a participant (incoming payments, trustlines, merges).
          Incoming coverage tracks the participant-index backfill.
        </p>
      </Panel>

      <AccountPositions id={id} />

      <AccountStatePanel id={id} state={stateQ.data} isLoading={stateQ.isLoading} isError={stateQ.isError} />

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

// ── Accounts directory (ranked by USD wealth) ───────────────────────────
// Mirrors api/v1.AccountsListView (GET /v1/accounts). Lists accounts ranked
// by the total USD value of their holdings — native XLM plus every trustline
// asset we hold a verified price for, summed over the current-state projection.
interface AccountsListResp {
  priced_assets: number;
  accounts: { account_id: string; usd_value: string }[];
}

const DIRECTORY_SIZE = 100;

const usdFmt = new Intl.NumberFormat('en-US', {
  style: 'currency',
  currency: 'USD',
  maximumFractionDigits: 0,
});

function AccountsDirectory() {
  const q = useQuery<AccountsListResp>({
    queryKey: ['/v1/accounts', DIRECTORY_SIZE],
    retry: false,
    queryFn: async () => {
      const env = await apiGet<Envelope<AccountsListResp>>('/v1/accounts', {
        limit: DIRECTORY_SIZE,
      });
      return env.data;
    },
    staleTime: 60_000,
  });

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">Accounts</h1>
        <p className="max-w-3xl text-sm text-ink-body">
          The richest accounts on Stellar, ranked by the total USD value of
          their holdings — native XLM plus every trustline asset we hold a
          verified price for, summed straight from the certified lake&apos;s
          current-state projection.
        </p>
      </header>

      <Panel
        title="Ranked by USD wealth"
        source={asExample('/v1/accounts', { limit: DIRECTORY_SIZE })}
        bodyClassName="space-y-3"
      >
        {q.isLoading && <p className="text-sm text-ink-muted">Loading…</p>}
        {q.isError && (
          <p className="text-sm text-ink-muted">
            The accounts directory is unavailable right now (the current-state
            projection is still backfilling, or pricing is offline).
          </p>
        )}
        {q.data && q.data.accounts.length === 0 && (
          <p className="text-sm text-ink-muted">No priced accounts yet.</p>
        )}
        {q.data && q.data.accounts.length > 0 && (
          <>
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-line text-left text-[11px] uppercase tracking-wider text-ink-muted">
                  <th className="w-12 py-1.5 pr-4 text-right font-normal">#</th>
                  <th className="py-1.5 pr-4 font-normal">Account</th>
                  <th className="py-1.5 text-right font-normal">USD value</th>
                </tr>
              </thead>
              <tbody>
                {q.data.accounts.map((a, i) => (
                  <tr
                    key={a.account_id}
                    className="border-b border-line/60 last:border-0 hover:bg-surface-muted"
                  >
                    <td className="py-1.5 pr-4 text-right font-mono tabular-nums text-ink-muted">
                      {i + 1}
                    </td>
                    <td className="py-1.5 pr-4 font-mono">
                      <Link
                        href={`/accounts/${encodeURIComponent(a.account_id)}/`}
                        className="hover:text-brand-600 hover:underline"
                      >
                        {a.account_id.slice(0, 10)}…{a.account_id.slice(-8)}
                      </Link>
                    </td>
                    <td className="py-1.5 text-right font-mono tabular-nums">
                      {usdFmt.format(Number(a.usd_value))}
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
            <p className="text-xs text-ink-muted">
              Summed across {q.data.priced_assets} priced asset
              {q.data.priced_assets === 1 ? '' : 's'}. Wealth not yet captured
              in the lake&apos;s ledger-entry window (Phase-C backfill in
              progress) is excluded.
            </p>
          </>
        )}
      </Panel>
    </div>
  );
}

// ── Account state (balances / signers / trustlines / offers) ────────────
// Mirrors api/v1.AccountStateView (GET /v1/accounts/{g}).
interface AccountStateResp {
  account_id: string;
  exists: boolean;
  balance?: string;
  seq_num?: string;
  num_subentries?: number;
  flags?: number;
  home_domain?: string;
  thresholds?: { master: number; low: number; med: number; high: number };
  signers?: { key: string; weight: number }[];
  trustlines?: { asset: string; balance: string; limit: string; flags: number }[];
  offers?: { offer_id: number; selling: string; buying: string; amount: string; price_n: number; price_d: number }[];
  last_modified_ledger?: number;
}

function AccountStatePanel({
  id,
  state,
  isLoading,
  isError,
}: {
  id: string;
  state: AccountStateResp | undefined;
  isLoading: boolean;
  isError: boolean;
}) {
  const source = asExample(`/v1/accounts/${id}`);
  if (isLoading) {
    return (
      <Panel title="State" source={source} bodyClassName="text-sm text-ink-muted">
        Loading account state…
      </Panel>
    );
  }
  if (isError || !state || !state.exists) {
    return (
      <Panel title="State" source={source} bodyClassName="text-sm text-ink-muted">
        No live account state in the captured ledger window yet — the account
        wasn’t touched since entry-change capture began (resolves once the
        Phase-C backfill lands). Sourced activity still shows below.
      </Panel>
    );
  }
  return (
    <Panel title="State" source={source} bodyClassName="space-y-5">
      <dl className="grid grid-cols-2 gap-x-6 gap-y-4 sm:grid-cols-3 lg:grid-cols-4">
        <Stat label="Native balance" value={`${stroopsToXlm(state.balance ?? '0')} XLM`} mono />
        <Stat label="Sequence" value={state.seq_num ?? '—'} mono />
        <Stat label="Sub-entries" value={String(state.num_subentries ?? 0)} />
        <Stat label="Home domain" value={state.home_domain || '—'} />
        {state.thresholds && (
          <Stat
            label="Thresholds (L/M/H)"
            mono
            value={`${state.thresholds.low}/${state.thresholds.med}/${state.thresholds.high}`}
          />
        )}
        <Stat label="Master weight" value={String(state.thresholds?.master ?? '—')} />
      </dl>

      {state.signers && state.signers.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-wider text-ink-muted">Signers</div>
          <ul className="space-y-1 text-xs">
            {state.signers.map((s) => (
              <li key={s.key} className="flex items-center gap-2">
                <span className="font-mono text-ink-body" title={s.key}>
                  {s.key.slice(0, 8)}…{s.key.slice(-6)}
                </span>
                <span className="text-ink-faint">weight {s.weight}</span>
              </li>
            ))}
          </ul>
        </div>
      )}

      {state.trustlines && state.trustlines.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-wider text-ink-muted">
            Trustlines ({state.trustlines.length})
          </div>
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-line text-sm">
              <thead>
                <tr className="text-left text-[10px] uppercase tracking-wider text-ink-muted">
                  <th className="py-1.5 pr-4">Asset</th>
                  <th className="py-1.5 pr-4 text-right">Balance</th>
                  <th className="py-1.5 text-right">Limit</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line-subtle">
                {state.trustlines.map((t) => (
                  <tr key={t.asset}>
                    <td className="py-1.5 pr-4 font-mono text-xs text-ink-body">{t.asset}</td>
                    <td className="py-1.5 pr-4 text-right font-mono tabular-nums">{stroopsToXlm(t.balance)}</td>
                    <td className="py-1.5 text-right font-mono tabular-nums text-ink-muted">{stroopsToXlm(t.limit)}</td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        </div>
      )}

      {state.offers && state.offers.length > 0 && (
        <div>
          <div className="mb-1 text-[11px] uppercase tracking-wider text-ink-muted">
            Open offers ({state.offers.length})
          </div>
          <ul className="space-y-1 text-xs font-mono text-ink-body">
            {state.offers.map((o) => (
              <li key={o.offer_id}>
                #{o.offer_id}: {stroopsToXlm(o.amount)} {o.selling} → {o.buying} @ {o.price_n}/{o.price_d}
              </li>
            ))}
          </ul>
        </div>
      )}
    </Panel>
  );
}

function Stat({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div>
      <dt className="text-[11px] uppercase tracking-wider text-ink-muted">{label}</dt>
      <dd className={mono ? 'mt-0.5 break-all font-mono text-xs' : 'mt-0.5 text-sm'}>{value}</dd>
    </div>
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
            { label: 'Accounts', href: '/accounts' },
            { label: id ? `${id.slice(0, 8)}…${id.slice(-6)}` : 'account' },
          ]}
        />
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
        No transactions observed for this account yet.
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
                    href={`/transactions/${t.hash}/`}
                    className="font-mono text-xs text-brand-600 hover:underline"
                    title={t.hash}
                  >
                    {t.hash.slice(0, 10)}…{t.hash.slice(-6)}
                  </Link>
                </Td>
                <Td>
                  <Link
                    href={`/ledgers/${t.ledger}/`}
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
        No operations observed for this account yet.
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
        <span className="rounded-sm bg-surface-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-body">
          #{op.op_index}
        </span>
        <span className="text-brand-700 rounded-sm bg-brand-50 px-2 py-0.5 text-[11px] font-medium">
          {op.type}
        </span>
        {op.tx_hash && (
          <Link
            href={`/transactions/${op.tx_hash}/`}
            className="font-mono text-[11px] text-brand-600 hover:underline"
            title={op.tx_hash}
          >
            tx {op.tx_hash.slice(0, 8)}…{op.tx_hash.slice(-6)}
          </Link>
        )}
        {op.ledger != null && (
          <Link
            href={`/ledgers/${op.ledger}/`}
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
        <details className="mt-2 rounded-sm border border-line">
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
