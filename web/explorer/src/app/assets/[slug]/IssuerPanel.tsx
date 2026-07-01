'use client';

import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useIssuer, type Issuer } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * IssuerPanel — backs the "Issuer" tab on /assets/[slug]. Fetches
 * the live issuer row + embedded assets from /v1/issuers/{g_strkey}
 * so users see the full directory of assets a single issuer has
 * minted (USDC issuer alone covers ~20 distinct codes).
 *
 * Auth flag pills surface the SEP-1 / on-chain account flags that
 * matter most for asset risk: `auth_required` (issuer can refuse
 * trustlines), `auth_revocable` (issuer can freeze), `auth_clawback`
 * (issuer can claw back balances).
 */
export function IssuerPanel({ gStrkey }: { gStrkey: string }) {
  const { data, isLoading, isError, error } = useIssuer(gStrkey);

  if (isError) {
    return (
      <Panel
        title="Issuer"
        source={asExample(`/v1/issuers/${gStrkey}`)}
        bodyClassName="text-sm text-ink-muted"
      >
        {is404(error)
          ? 'No issuer record yet — this G-strkey hasn’t been observed as an issuer.'
          : `Failed to load issuer: ${error instanceof Error ? error.message : 'unknown error'}`}
      </Panel>
    );
  }
  if (isLoading || !data) {
    return (
      <Panel
        title="Issuer"
        source={asExample(`/v1/issuers/${gStrkey}`)}
        bodyClassName="text-sm text-ink-muted"
      >
        Loading…
      </Panel>
    );
  }

  return (
    <div className="space-y-4">
      {data.scam_reason && (
        <div
          role="alert"
          className="rounded-md border border-bad-300 bg-bad-50 p-3 text-sm text-bad-700"
        >
          <strong className="font-semibold">Known scam issuer</strong> ·{' '}
          {data.scam_reason}. Every asset in the table below was
          minted by this account. Treat all of them with the same
          warning the page header surfaces — do not establish
          trustlines.
        </div>
      )}
      <Panel
        title="Issuer identity"
        hint={data.org_name ?? data.home_domain ?? '—'}
        source={asExample(`/v1/issuers/${gStrkey}`)}
      >
        <dl className="grid grid-cols-1 gap-3 text-sm sm:grid-cols-2">
          {data.org_name && (
            <Stat label="Organisation" value={data.org_name} />
          )}
          <Stat label="G-strkey" mono value={data.g_strkey} />
          {data.home_domain && (
            <Stat label="Home domain" mono value={data.home_domain} />
          )}
          {typeof data.creation_ledger === 'number' && (
            <Stat
              label="Creation ledger"
              mono
              value={`#${data.creation_ledger.toLocaleString()}`}
            />
          )}
          {data.sep1_resolved_at && (
            <Stat label="SEP-1 resolved" value={data.sep1_resolved_at} />
          )}
        </dl>
        <div className="mt-4 flex flex-wrap gap-1">
          <FlagPill on={data.auth_required} label="auth_required" />
          <FlagPill on={data.auth_revocable} label="auth_revocable" />
          <FlagPill on={data.auth_immutable} label="auth_immutable" />
          <FlagPill on={data.auth_clawback} label="auth_clawback" />
        </div>
      </Panel>

      <IssuedAssetsTable issuer={data} />
    </div>
  );
}

function IssuedAssetsTable({ issuer }: { issuer: Issuer }) {
  const assets = issuer.assets ?? [];
  if (assets.length === 0) {
    return (
      <Panel
        title="Issued assets"
        source={asExample(`/v1/issuers/${issuer.g_strkey}`)}
        bodyClassName="text-sm text-ink-muted"
      >
        No issued assets recorded.
      </Panel>
    );
  }

  return (
    <Panel
      title="Issued assets"
      hint={`${assets.length} asset${assets.length === 1 ? '' : 's'}`}
      source={asExample(`/v1/issuers/${issuer.g_strkey}`)}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <Th>Code</Th>
              <Th>Slug</Th>
              <Th align="right">Observations</Th>
              <Th align="right">First seen</Th>
              <Th align="right">Last seen</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {assets.map((a) => (
              <tr
                key={a.asset_id}
                className="hover:bg-surface-muted"
              >
                <Td>
                  <Link
                    href={`/assets/${a.slug}`}
                    className="font-medium hover:text-brand-600"
                  >
                    {a.code}
                  </Link>
                </Td>
                <Td>
                  <span className="font-mono text-xs text-ink-muted">
                    {a.slug}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums">
                    {formatCompact(a.observation_count)}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-xs text-ink-muted">
                    #{a.first_seen_ledger.toLocaleString()}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums text-xs text-ink-muted">
                    #{a.last_seen_ledger.toLocaleString()}
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

function FlagPill({ on, label }: { on?: boolean; label: string }) {
  if (on === undefined) {
    return (
      <span className="inline-block rounded-sm bg-surface-subtle px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-ink-muted">
        {label}: unknown
      </span>
    );
  }
  const cls = on
    ? 'bg-warn-50 text-warn-700'
    : 'bg-up-soft text-up-strong';
  return (
    <span
      className={`inline-block rounded-sm px-1.5 py-0.5 text-[10px] uppercase tracking-wider ${cls}`}
    >
      {label}: {on ? 'on' : 'off'}
    </span>
  );
}

function Stat({
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
      <dd className={mono ? 'break-all font-mono text-xs' : 'tabular-nums'}>
        {value}
      </dd>
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

function is404(err: unknown): boolean {
  if (!err) return false;
  const msg = err instanceof Error ? err.message : String(err);
  return /\b404\b/.test(msg);
}
