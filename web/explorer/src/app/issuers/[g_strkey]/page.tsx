import type { Metadata } from 'next';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { asExample, API_BASE_URL } from '@/api/client';
import { formatCompact } from '@/lib/format';

/**
 * /issuers/[g_strkey] — single-issuer detail page.
 *
 * Server component fetches /v1/issuers/{g_strkey} at request
 * time. Renders the identity, auth flags, SEP-1 status, and a
 * table of every asset minted by the issuer.
 *
 * G-strkeys are 56 chars (uppercase base32). Static export with
 * output:'export' needs a non-empty generateStaticParams; we
 * pre-render the top issuers (up to 100) so deep links resolve
 * without a build-time round trip per route.
 */
type Params = Promise<{ g_strkey: string }>;

interface IssuedAsset {
  asset_id: string;
  code: string;
  slug: string;
  first_seen_ledger: number;
  last_seen_ledger: number;
  observation_count: number;
}

interface IssuerDetail {
  g_strkey: string;
  home_domain?: string;
  org_name?: string;
  auth_required?: boolean;
  auth_revocable?: boolean;
  auth_immutable?: boolean;
  auth_clawback?: boolean;
  sep1_resolved_at?: string;
  creation_ledger?: number;
  assets?: IssuedAsset[];
}

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

export async function generateStaticParams() {
  const fallback = [
    { g_strkey: 'GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN' },
  ];
  if (isCIStub) return fallback;
  try {
    const res = await fetch(`${API_BASE_URL}/v1/issuers?limit=100`, {
      signal: AbortSignal.timeout(2_000),
    });
    if (!res.ok) throw new Error(`HTTP ${res.status}`);
    const env = (await res.json()) as { data: { g_strkey: string }[] };
    const keys = env.data?.map((i) => i.g_strkey) ?? [];
    return keys.length > 0 ? keys.map((g_strkey) => ({ g_strkey })) : fallback;
  } catch {
    return fallback;
  }
}

async function fetchIssuer(gStrkey: string): Promise<IssuerDetail | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/issuers/${encodeURIComponent(gStrkey)}`,
      { signal: AbortSignal.timeout(2_000) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as { data: IssuerDetail };
    return env.data ?? null;
  } catch {
    return null;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { g_strkey } = await params;
  const short = `${g_strkey.slice(0, 8)}…${g_strkey.slice(-4)}`;
  return {
    title: `Issuer ${short} — Stellar`,
    description: `Identity, auth flags, and issued assets for Stellar issuer ${short}.`,
  };
}

export default async function IssuerDetailPage({ params }: { params: Params }) {
  const { g_strkey } = await params;
  const detail = await fetchIssuer(g_strkey);

  if (!detail) {
    return (
      <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
        <header className="space-y-3">
          <nav className="text-xs text-slate-500">
            <Link href="/issuers" className="hover:text-brand-600">
              Issuers
            </Link>{' '}
            / <span className="font-mono">{shortKey(g_strkey)}</span>
          </nav>
          <h1 className="font-mono text-2xl font-semibold tracking-tight">
            {shortKey(g_strkey)}
          </h1>
        </header>
        <Panel
          title="Issuer not found"
          bodyClassName="text-sm text-slate-600 dark:text-slate-400"
        >
          <p>
            No row found for that G-strkey, or the issuer hasn&apos;t
            issued a classic asset that&apos;s appeared in any trade or
            ChangeTrust op the indexer has seen.
          </p>
        </Panel>
      </div>
    );
  }

  const totalObs =
    detail.assets?.reduce((sum, a) => sum + a.observation_count, 0) ?? 0;

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-3">
        <nav className="text-xs text-slate-500">
          <Link href="/issuers" className="hover:text-brand-600">
            Issuers
          </Link>{' '}
          /{' '}
          <span className="font-mono text-slate-700 dark:text-slate-300">
            {detail.org_name || shortKey(g_strkey)}
          </span>
        </nav>
        {detail.org_name ? (
          <>
            <h1 className="text-2xl font-semibold tracking-tight">
              {detail.org_name}
            </h1>
            <p className="font-mono text-xs text-slate-500 break-all">
              {g_strkey}
            </p>
          </>
        ) : (
          <h1 className="font-mono text-2xl font-semibold tracking-tight break-all">
            {g_strkey}
          </h1>
        )}
        {detail.home_domain && (
          <p className="text-sm text-slate-600 dark:text-slate-400">
            <a
              href={`https://${detail.home_domain}`}
              target="_blank"
              rel="noreferrer noopener"
              className="font-mono hover:text-brand-600 hover:underline"
            >
              {detail.home_domain}
            </a>
          </p>
        )}
      </header>

      <div className="grid grid-cols-1 gap-4 lg:grid-cols-3">
        <Panel
          title="Activity"
          source={asExample('/v1/issuers/{g_strkey}', { g_strkey })}
          panelId="activity-card"
          className="lg:col-span-2"
        >
          <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-4">
            <Stat label="Assets" value={String(detail.assets?.length ?? 0)} />
            <Stat
              label="Total observations"
              value={formatCompact(totalObs)}
            />
            <Stat
              label="Creation ledger"
              mono
              value={
                detail.creation_ledger != null
                  ? `#${detail.creation_ledger.toLocaleString()}`
                  : '—'
              }
            />
            <Stat
              label="SEP-1 resolved"
              value={
                detail.sep1_resolved_at
                  ? relativeAge(detail.sep1_resolved_at)
                  : '—'
              }
            />
          </dl>
        </Panel>

        <Panel title="Auth flags" panelId="auth-flags-card">
          <ul className="space-y-1.5 text-xs">
            <FlagRow label="auth_required" v={detail.auth_required} />
            <FlagRow label="auth_revocable" v={detail.auth_revocable} />
            <FlagRow label="auth_immutable" v={detail.auth_immutable} />
            <FlagRow label="auth_clawback" v={detail.auth_clawback} />
          </ul>
        </Panel>
      </div>

      <Panel
        title={`Issued assets (${detail.assets?.length ?? 0})`}
        hint="All classic assets we've observed minted by this G-strkey"
        source={asExample('/v1/issuers/{g_strkey}', { g_strkey })}
        bodyClassName="-mx-4"
      >
        {!detail.assets || detail.assets.length === 0 ? (
          <p className="px-4 py-3 text-sm text-slate-500">
            No issued assets observed.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-slate-200 text-sm dark:divide-slate-800">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
                  <Th>Code</Th>
                  <Th>Slug</Th>
                  <Th align="right">First ledger</Th>
                  <Th align="right">Last ledger</Th>
                  <Th align="right">Observations</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-slate-100 dark:divide-slate-800">
                {detail.assets.map((a) => (
                  <tr
                    key={a.asset_id}
                    className="hover:bg-slate-50 dark:hover:bg-slate-900/40"
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
                      <span className="font-mono text-xs text-slate-500">
                        {a.slug}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono text-xs">
                        #{a.first_seen_ledger.toLocaleString()}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono text-xs">
                        #{a.last_seen_ledger.toLocaleString()}
                      </span>
                    </Td>
                    <Td align="right">
                      <span className="font-mono tabular-nums">
                        {formatCompact(a.observation_count)}
                      </span>
                    </Td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </Panel>
    </div>
  );
}

function shortKey(g: string): string {
  return `${g.slice(0, 8)}…${g.slice(-4)}`;
}

function relativeAge(iso: string): string {
  const ms = Date.now() - Date.parse(iso);
  if (!Number.isFinite(ms)) return iso;
  const s = Math.floor(ms / 1000);
  if (s < 60) return `${s}s ago`;
  const m = Math.floor(s / 60);
  if (m < 60) return `${m}m ago`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h}h ago`;
  return `${Math.floor(h / 24)}d ago`;
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
      <dt className="text-[11px] uppercase tracking-wider text-slate-500">
        {label}
      </dt>
      <dd className={mono ? 'font-mono text-xs' : 'tabular-nums'}>{value}</dd>
    </div>
  );
}

function FlagRow({ label, v }: { label: string; v: boolean | undefined }) {
  let tone: string;
  let text: string;
  if (v === true) {
    tone = 'bg-amber-500';
    text = 'true';
  } else if (v === false) {
    tone = 'bg-slate-300 dark:bg-slate-700';
    text = 'false';
  } else {
    tone = 'bg-slate-200 dark:bg-slate-800';
    text = 'unknown';
  }
  return (
    <li className="flex items-center justify-between gap-2 font-mono">
      <span className="text-slate-600 dark:text-slate-400">{label}</span>
      <span className="flex items-center gap-1.5">
        <span className={`inline-block h-2 w-2 rounded-full ${tone}`} />
        <span className="text-slate-700 dark:text-slate-300">{text}</span>
      </span>
    </li>
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
