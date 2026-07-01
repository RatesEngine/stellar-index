'use client';

import { useMemo, useState } from 'react';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { asExample } from '@/api/client';
import { useIssuers } from '@/api/hooks';
import { formatCompact } from '@/lib/format';
import { isSafeHomeDomain } from '@/lib/safe-domain';

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
  const [filter, setFilter] = useState('');

  const filtered = useMemo(() => {
    const q = filter.trim().toLowerCase();
    if (!q) return data ?? [];
    return (data ?? []).filter((row) => {
      const hay = `${row.org_name ?? ''} ${row.home_domain ?? ''} ${row.g_strkey}`.toLowerCase();
      return hay.includes(q);
    });
  }, [data, filter]);

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
        bodyClassName="text-sm text-ink-muted"
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
        bodyClassName="text-sm text-ink-muted"
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
      <div className="px-4 pb-3 pt-1">
        <div className="flex flex-wrap items-center gap-3 text-xs">
          <input
            type="search"
            aria-label="Filter issuers by name, domain, or G-strkey"
            placeholder="Filter by name, domain, or G-strkey…"
            value={filter}
            onChange={(e) => setFilter(e.target.value)}
            className="w-72 rounded-md border border-line bg-surface px-2.5 py-1 text-xs placeholder:text-ink-faint focus:border-brand-500 focus:outline-hidden focus:ring-1 focus:ring-brand-500"
          />
          <span className="font-mono text-[11px] text-ink-muted">
            {filtered.length} of {data.length} rows
            {filter && (
              <button
                type="button"
                onClick={() => setFilter('')}
                className="ml-2 text-brand-600 hover:underline"
              >
                clear
              </button>
            )}
          </span>
        </div>
      </div>
      <div className="overflow-x-auto">
        <table className="min-w-full divide-y divide-line text-sm">
          <thead>
            <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
              <Th>#</Th>
              <Th>Organisation</Th>
              <Th>G-strkey</Th>
              <Th>Home domain</Th>
              <Th align="right">Assets</Th>
              <Th align="right">Total observations</Th>
            </tr>
          </thead>
          <tbody className="divide-y divide-line-subtle">
            {filtered.length === 0 && filter && (
              <tr>
                <td colSpan={6} className="px-4 py-8 text-center text-sm text-ink-muted">
                  No issuers match &quot;{filter}&quot;.
                </td>
              </tr>
            )}
            {filtered.map((row, i) => (
              <tr
                key={row.g_strkey}
                className="hover:bg-surface-muted"
              >
                <Td>
                  <span className="text-ink-faint">{i + 1}</span>
                </Td>
                <Td>
                  <div className="flex items-center gap-1.5">
                    {row.org_name ? (
                      <Link
                        href={`/issuers/${row.g_strkey}`}
                        className="font-medium hover:text-brand-600"
                      >
                        {row.org_name}
                      </Link>
                    ) : (
                      <span className="text-xs text-ink-faint">—</span>
                    )}
                    {row.org_verified && row.org_name && (
                      <span
                        title="SEP-1 verified — the organisation's stellar.toml lists this issuer back (bidirectional)"
                        className="rounded-sm bg-up-subtle px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-up-strong"
                      >
                        ✓ Verified
                      </span>
                    )}
                    {row.scam_reason && (
                      <span
                        title={row.scam_reason}
                        className="rounded-sm bg-down-subtle px-1.5 py-0.5 text-[9px] font-medium uppercase tracking-wider text-down"
                      >
                        SCAM
                      </span>
                    )}
                  </div>
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
                  {isSafeHomeDomain(row.home_domain) ? (
                    <a
                      href={`https://${row.home_domain}`}
                      target="_blank"
                      rel="noreferrer noopener nofollow"
                      className="text-xs hover:text-brand-600 hover:underline"
                    >
                      {row.home_domain}
                    </a>
                  ) : row.home_domain ? (
                    // Attacker-controlled on-chain value that doesn't
                    // parse as a strict hostname — render as plain
                    // text, never a clickable link (phishing guard).
                    <span className="text-xs text-ink-muted" title="Unverified issuer-supplied domain">
                      {row.home_domain}
                    </span>
                  ) : (
                    <span className="text-xs text-ink-faint">—</span>
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
