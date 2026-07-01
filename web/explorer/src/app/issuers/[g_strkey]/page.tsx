import type { Metadata } from 'next';
import Link from 'next/link';

import { Panel } from '@/components/reveal';
import { Breadcrumbs } from '@/components/ui';
import { asExample, API_BASE_URL } from '@/api/client';
import { formatCompact } from '@/lib/format';
import { isSafeHomeDomain } from '@/lib/safe-domain';
import { serializeJsonLd, ogImageFor } from '@/lib/seo';

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
  org_verified?: boolean;
  scam_reason?: string;
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

interface CoinPriceRow {
  asset_id: string;
  price_usd?: string | null;
  volume_24h_usd?: string | null;
  change_24h_pct?: string | null;
}

async function fetchIssuerCoins(gStrkey: string): Promise<Map<string, CoinPriceRow>> {
  const out = new Map<string, CoinPriceRow>();
  if (isCIStub) return out;
  try {
    // Migrated to /v1/assets?issuer= (rc.47 R-018 finish). Wire
    // shape is `{data: [AssetDetail]}` — fields match CoinPriceRow
    // for the read columns this page renders.
    const res = await fetch(
      `${API_BASE_URL}/v1/assets?issuer=${encodeURIComponent(gStrkey)}&limit=500`,
      { signal: AbortSignal.timeout(2_000) },
    );
    if (!res.ok) return out;
    const env = (await res.json()) as { data?: CoinPriceRow[] };
    for (const c of env.data ?? []) out.set(c.asset_id, c);
    return out;
  } catch {
    return out;
  }
}

export async function generateMetadata({
  params,
}: {
  params: Params;
}): Promise<Metadata> {
  const { g_strkey } = await params;
  const short = `${g_strkey.slice(0, 8)}…${g_strkey.slice(-4)}`;
  const canonical = `https://stellarindex.io/issuers/${g_strkey}`;
  const title = `Issuer ${short} — Stellar`;
  const description = `Identity, auth flags, and issued assets for Stellar issuer ${short}.`;
  return {
    title,
    description,
    alternates: { canonical },
    openGraph: { title, description, url: canonical, type: 'website', images: [ogImageFor('issuers', g_strkey)] },
    twitter: { card: 'summary_large_image', title, description, images: [ogImageFor('issuers', g_strkey)] },
  };
}

export default async function IssuerDetailPage({ params }: { params: Params }) {
  const { g_strkey } = await params;
  // Fan out the issuer detail + per-asset price/volume calls in
  // parallel — they share zero data, so we pay 1× round trip
  // instead of 2×.
  const [detail, coinPrices] = await Promise.all([
    fetchIssuer(g_strkey),
    fetchIssuerCoins(g_strkey),
  ]);

  if (!detail) {
    return (
      <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
        <header className="space-y-3">
          <Breadcrumbs
            items={[
              { label: 'Home', href: '/' },
              { label: 'Issuers', href: '/issuers' },
              { label: shortKey(g_strkey) },
            ]}
          />
          <h1 className="font-mono text-2xl font-semibold tracking-tight">
            {shortKey(g_strkey)}
          </h1>
        </header>
        <Panel
          title="Issuer not found"
          bodyClassName="text-sm text-ink-body"
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
  // Sum per-asset 24h USD volume from the parallel /v1/coins?issuer= fetch.
  // null/missing volumes drop out cleanly; the panel renders "—" when
  // every asset row had no recent USD-priced trade.
  let totalVolume24hUSD = 0;
  let anyVolume = false;
  for (const a of detail.assets ?? []) {
    const v = Number(coinPrices.get(a.asset_id)?.volume_24h_usd ?? '');
    if (Number.isFinite(v) && v > 0) {
      totalVolume24hUSD += v;
      anyVolume = true;
    }
  }

  // Schema.org BreadcrumbList — gives Google a structured
  // hierarchy (Home → Issuers → <org_name>) so search results can
  // render the breadcrumb path under the title. Same shape as
  // /assets/[slug] and /markets/[pair].
  const breadcrumbLD = {
    '@context': 'https://schema.org',
    '@type': 'BreadcrumbList',
    itemListElement: [
      { '@type': 'ListItem', position: 1, name: 'Home', item: 'https://stellarindex.io' },
      { '@type': 'ListItem', position: 2, name: 'Issuers', item: 'https://stellarindex.io/issuers' },
      {
        '@type': 'ListItem',
        position: 3,
        name: detail.org_name || shortKey(g_strkey),
        item: `https://stellarindex.io/issuers/${g_strkey}`,
      },
    ],
  };

  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <script
        type="application/ld+json"
        dangerouslySetInnerHTML={{ __html: serializeJsonLd(breadcrumbLD) }}
      />
      {detail.scam_reason && (
        <div className="rounded-lg border-2 border-down/40 bg-down-subtle px-4 py-3">
          <div className="flex items-baseline gap-2">
            <span className="rounded bg-down px-2 py-0.5 text-[10px] font-bold uppercase tracking-wider text-white">
              Warning
            </span>
            <span className="text-sm font-medium text-down-strong">
              This issuer is flagged as malicious or unsafe.
            </span>
          </div>
          <p className="mt-1.5 text-xs text-down-strong">
            {detail.scam_reason}. Do not trust assets issued from this account.
            Source: stellar.expert directory.
          </p>
        </div>
      )}

      <header className="space-y-3">
        <Breadcrumbs
          items={[
            { label: 'Home', href: '/' },
            { label: 'Issuers', href: '/issuers' },
            { label: detail.org_name || shortKey(g_strkey) },
          ]}
        />
        {detail.org_name ? (
          <>
            {/* CS-100: org_name is SELF-DECLARED SEP-1 metadata. Render the
                verification state next to it so an unverified name can't pass
                as an authoritative identity. Verified = the org's stellar.toml
                lists this issuer back (bidirectional). */}
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-2xl font-semibold tracking-tight">
                {detail.org_name}
              </h1>
              {detail.org_verified ? (
                <span
                  title="SEP-1 verified — the organisation's stellar.toml lists this issuer back (bidirectional)"
                  className="rounded bg-up-subtle px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider text-up-strong"
                >
                  ✓ Verified
                </span>
              ) : (
                <span
                  title="Unverified — this organisation name is self-declared in the issuer's SEP-1 toml and is NOT cross-confirmed by the organisation. Do not treat it as a verified identity."
                  className="rounded bg-surface-sunk px-1.5 py-0.5 text-[10px] font-medium uppercase tracking-wider text-ink-muted"
                >
                  Unverified
                </span>
              )}
            </div>
            <p className="font-mono text-xs text-ink-muted break-all">
              {g_strkey}
            </p>
          </>
        ) : (
          <h1 className="font-mono text-2xl font-semibold tracking-tight break-all">
            {g_strkey}
          </h1>
        )}
        {detail.home_domain && (
          <p className="text-sm text-ink-body">
            {/* home_domain is attacker-controlled on-chain data — only
                link it when it parses as a strict hostname, else render
                as plain text (phishing guard, WA-02). */}
            {isSafeHomeDomain(detail.home_domain) ? (
              <a
                href={`https://${detail.home_domain}`}
                target="_blank"
                rel="noreferrer noopener nofollow"
                className="font-mono hover:text-brand-600 hover:underline"
              >
                {detail.home_domain}
              </a>
            ) : (
              <span
                className="font-mono text-ink-muted"
                title="Unverified issuer-supplied domain"
              >
                {detail.home_domain}
              </span>
            )}
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
          <dl className="grid grid-cols-2 gap-3 text-sm sm:grid-cols-5">
            <Stat label="Assets" value={String(detail.assets?.length ?? 0)} />
            <Stat
              label="24h volume"
              value={anyVolume ? `$${formatCompact(totalVolume24hUSD)}` : '—'}
            />
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
          {detail.auth_required == null &&
          detail.auth_revocable == null &&
          detail.auth_immutable == null &&
          detail.auth_clawback == null ? (
            // The account-flag reader hasn't populated this issuer yet —
            // show that honestly rather than four "unknown" dots that
            // read as a broken panel (audit 2026-06-19).
            <p className="text-xs text-ink-muted">
              Not yet resolved. Issuer account flags populate as the
              account-flag reader processes the issuer; meanwhile see{' '}
              <a
                href={`https://stellar.expert/explorer/public/account/${g_strkey}`}
                target="_blank"
                rel="noreferrer noopener"
                className="text-brand-600 hover:underline"
              >
                stellar.expert
              </a>
              .
            </p>
          ) : (
            <ul className="space-y-1.5 text-xs">
              <FlagRow label="auth_required" v={detail.auth_required} />
              <FlagRow label="auth_revocable" v={detail.auth_revocable} />
              <FlagRow label="auth_immutable" v={detail.auth_immutable} />
              <FlagRow label="auth_clawback" v={detail.auth_clawback} />
            </ul>
          )}
        </Panel>
      </div>

      <Panel
        title="External views"
        hint="Cross-reference this issuer on other Stellar explorers"
        bodyClassName="text-sm text-ink-body"
      >
        <ul className="space-y-2">
          <li>
            <a
              href={`https://stellar.expert/explorer/public/account/${g_strkey}`}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1.5 hover:text-brand-600 hover:underline"
            >
              stellar.expert
              <span className="text-[10px] uppercase tracking-wider text-ink-faint">
                ↗
              </span>
            </a>
            <span className="ml-2 text-xs text-ink-faint">
              account history, balance, signers
            </span>
          </li>
          <li>
            <a
              href={`https://stellarchain.io/accounts/${g_strkey}`}
              target="_blank"
              rel="noreferrer noopener"
              className="inline-flex items-center gap-1.5 hover:text-brand-600 hover:underline"
            >
              stellarchain.io
              <span className="text-[10px] uppercase tracking-wider text-ink-faint">
                ↗
              </span>
            </a>
            <span className="ml-2 text-xs text-ink-faint">
              ledger entries, operations log
            </span>
          </li>
          {isSafeHomeDomain(detail.home_domain) && (
            <li>
              <a
                href={`https://${detail.home_domain}/.well-known/stellar.toml`}
                target="_blank"
                rel="noreferrer noopener nofollow"
                className="inline-flex items-center gap-1.5 hover:text-brand-600 hover:underline"
              >
                stellar.toml
                <span className="text-[10px] uppercase tracking-wider text-ink-faint">
                  ↗
                </span>
              </a>
              <span className="ml-2 text-xs text-ink-faint">
                SEP-1 source on {detail.home_domain}
              </span>
            </li>
          )}
        </ul>
      </Panel>

      <Panel
        title={`Issued assets (${detail.assets?.length ?? 0})`}
        hint="All classic assets we've observed minted by this G-strkey"
        source={asExample('/v1/issuers/{g_strkey}', { g_strkey })}
        bodyClassName="-mx-4"
      >
        {!detail.assets || detail.assets.length === 0 ? (
          <p className="px-4 py-3 text-sm text-ink-muted">
            No issued assets observed.
          </p>
        ) : (
          <div className="overflow-x-auto">
            <table className="min-w-full divide-y divide-line text-sm">
              <thead>
                <tr className="text-left text-[11px] uppercase tracking-wider text-ink-muted">
                  <Th>Code</Th>
                  <Th align="right">Price</Th>
                  <Th align="right">24h %</Th>
                  <Th align="right">24h volume</Th>
                  <Th align="right">Observations</Th>
                  <Th align="right">First seen</Th>
                </tr>
              </thead>
              <tbody className="divide-y divide-line-subtle">
                {detail.assets.map((a) => {
                  const coin = coinPrices.get(a.asset_id);
                  return (
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
                        <span className="ml-2 font-mono text-[11px] text-ink-muted">
                          {a.slug}
                        </span>
                        <Link
                          href={`/markets?asset=${encodeURIComponent(a.asset_id)}`}
                          className="ml-2 text-[11px] text-brand-600 hover:underline"
                          title={`All markets for ${a.code}`}
                        >
                          markets →
                        </Link>
                      </Td>
                      <Td align="right">
                        <PriceCell raw={coin?.price_usd} />
                      </Td>
                      <Td align="right">
                        <ChangeCell raw={coin?.change_24h_pct} />
                      </Td>
                      <Td align="right">
                        <UsdVolumeCell raw={coin?.volume_24h_usd} />
                      </Td>
                      <Td align="right">
                        <span className="font-mono tabular-nums">
                          {formatCompact(a.observation_count)}
                        </span>
                      </Td>
                      <Td align="right">
                        <span className="font-mono text-xs">
                          #{a.first_seen_ledger.toLocaleString()}
                        </span>
                      </Td>
                    </tr>
                  );
                })}
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
      <dt className="text-[11px] uppercase tracking-wider text-ink-muted">
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
    tone = 'bg-warn-500';
    text = 'true';
  } else if (v === false) {
    tone = 'bg-line-strong';
    text = 'false';
  } else {
    tone = 'bg-line';
    text = 'unknown';
  }
  return (
    <li className="flex items-center justify-between gap-2 font-mono">
      <span className="text-ink-body">{label}</span>
      <span className="flex items-center gap-1.5">
        <span className={`inline-block h-2 w-2 rounded-full ${tone}`} />
        <span className="text-ink-body">{text}</span>
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

function PriceCell({ raw }: { raw?: string | null }) {
  if (!raw) return <span className="text-ink-faint">—</span>;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <span className="text-ink-faint">—</span>;
  // 6 dp for sub-dollar (USDC/scam tokens), 4 dp otherwise.
  const fixed = n < 1 ? n.toFixed(6) : n.toFixed(4);
  return (
    <span className="font-mono tabular-nums text-ink-body">
      ${fixed}
    </span>
  );
}

function ChangeCell({ raw }: { raw?: string | null }) {
  if (!raw) return <span className="text-ink-faint">—</span>;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <span className="text-ink-faint">—</span>;
  const tone =
    n > 0
      ? 'text-up'
      : n < 0
        ? 'text-down'
        : 'text-ink-muted';
  const sign = n > 0 ? '+' : '';
  return (
    <span className={`font-mono tabular-nums ${tone}`}>
      {sign}
      {n.toFixed(2)}%
    </span>
  );
}

function UsdVolumeCell({ raw }: { raw?: string | null }) {
  if (!raw) return <span className="text-ink-faint">—</span>;
  const n = Number(raw);
  if (!Number.isFinite(n)) return <span className="text-ink-faint">—</span>;
  return (
    <span className="font-mono tabular-nums text-ink-body">
      ${formatCompact(n)}
    </span>
  );
}
