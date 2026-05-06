'use client';

import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useIssuers } from '@/api/hooks';
import { formatCompact } from '@/lib/format';

/**
 * Live issuer directory backed by `/v1/issuers`. Ranked by total
 * observation count across the issuer's classic assets — the
 * proxy-for-activity ordering the API serves.
 *
 * G-strkey column deep-links to /issuers/[g_strkey] — the
 * dedicated detail view. /assets?issuer=... is also reachable
 * via "View assets" from there.
 */
export function IssuersTable() {
  const { data, isLoading, isError, error } = useIssuers(100);

  if (isError) {
    return (
      <Panel
        title="Issuers"
        source={asExample('/v1/issuers', { limit: 100 })}
        bodyClassName="text-sm text-down-strong"
      >
        Failed to load issuers:{' '}
        {error instanceof Error ? error.message : 'unknown error'}
      </Panel>
    );
  }
  if (isLoading || !data) {
    return (
      <Panel
        title="Issuers"
        source={asExample('/v1/issuers', { limit: 100 })}
        bodyClassName="text-sm text-slate-500"
      >
        Loading…
      </Panel>
    );
  }
  if (data.length === 0) {
    return (
      <Panel
        title="Issuers"
        source={asExample('/v1/issuers', { limit: 100 })}
        bodyClassName="text-sm text-slate-500"
      >
        No issuers recorded yet.
      </Panel>
    );
  }

  return (
    <Panel
      title={`${data.length} top issuers`}
      hint="Ranked by total observation count across each issuer's assets"
      source={asExample('/v1/issuers', { limit: 100 })}
      bodyClassName="-mx-4"
    >
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
              <Th>#</Th>
              <Th>Organisation</Th>
              <Th>G-strkey</Th>
              <Th>Home domain</Th>
              <Th align="right">Assets</Th>
              <Th align="right">Total observations</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
            {data.map((row, i) => (
              <tr
                key={row.g_strkey}
                className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
              >
                <Td>
                  <span className="text-slate-400">{i + 1}</span>
                </Td>
                <Td>
                  {row.org_name ? (
                    <Link
                      href={`/issuers/${row.g_strkey}`}
                      className="font-medium hover:text-brand-600"
                    >
                      {row.org_name}
                    </Link>
                  ) : (
                    <span className="text-xs text-slate-400">—</span>
                  )}
                </Td>
                <Td>
                  <Link
                    href={`/issuers/${row.g_strkey}`}
                    className="font-mono text-xs hover:text-brand-600"
                    title={row.g_strkey}
                  >
                    {row.g_strkey.slice(0, 8)}…{row.g_strkey.slice(-4)}
                  </Link>
                </Td>
                <Td>
                  {row.home_domain ? (
                    <a
                      href={`https://${row.home_domain}`}
                      target="_blank"
                      rel="noreferrer"
                      className="text-xs hover:text-brand-600 hover:underline"
                    >
                      {row.home_domain}
                    </a>
                  ) : (
                    <span className="text-xs text-slate-400">—</span>
                  )}
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums">
                    {row.asset_count}
                  </span>
                </Td>
                <Td align="right">
                  <span className="font-mono tabular-nums">
                    {formatCompact(row.total_observation_count)}
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
