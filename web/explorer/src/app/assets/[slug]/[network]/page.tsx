import type { Metadata } from 'next';
import { notFound } from 'next/navigation';
import Link from 'next/link';
import { Suspense } from 'react';

import { API_BASE_URL } from '@/api/client';
import { IssuerPanel } from '../IssuerPanel';
import { MarketsTabPanel } from '../MarketsTabPanel';
import { SupplyTabPanel } from '../SupplyTabPanel';

/**
 * /assets/[slug]/[network] — per-network deep dive.
 *
 * Hosts the network-specific detail that used to live inline on
 * /assets/[slug] (R-018 phase 2 — assets-redesign spec). The
 * cross-chain /assets/[slug] page lists the asset's networks[];
 * this page is what each row links to.
 *
 * For Stellar: surfaces the issuer panel, on-chain SDEX markets,
 * supply (mint/burn observers), and per-Stellar-asset trade history
 * via the canonical asset_id (e.g. USDC-GA5Z...).
 *
 * For non-Stellar (Ethereum, Solana, Polygon, …): we don't index
 * trades on those chains today, so this page renders the catalogue's
 * contract metadata + an external explorer link.
 */

interface NetworkEntry {
  network: string;
  data_quality: 'indexed' | 'external';
  asset_id?: string;
  code?: string;
  issuer?: string;
  contract?: string;
  external_link?: string;
  deep_link?: string;
}

interface GlobalAssetView {
  ticker: string;
  slug: string;
  name: string;
  description?: string;
  class?: 'fiat' | 'crypto' | 'stablecoin';
  networks: NetworkEntry[];
}

const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');
const FETCH_TIMEOUT_MS = 8_000;

interface VerifiedCurrencyEntry {
  slug: string;
  networks: { network: string }[];
}

/**
 * Static-export enumeration of every (slug, network) pair the
 * catalogue knows about. Fetches /v1/assets/verified once at build
 * time and emits one route per network entry per catalogue slug.
 * Static-asset count is bounded at len(catalogue) × max networks
 * per row — ~70 routes today (45 catalogue × ≤6 networks each).
 */
export async function generateStaticParams(): Promise<
  { slug: string; network: string }[]
> {
  const fallback = [{ slug: 'usdc', network: 'stellar' }];
  if (isCIStub) {
    return fallback;
  }
  try {
    const res = await fetch(`${API_BASE_URL}/v1/assets/verified`, {
      signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
    });
    if (!res.ok) {
      // eslint-disable-next-line no-console
      console.warn(
        `[assets/[slug]/[network]] /v1/assets/verified returned ${res.status}; using fallback list`,
      );
      return fallback;
    }
    const env = (await res.json()) as { data?: VerifiedCurrencyEntry[] };
    const entries = env.data ?? [];
    const out: { slug: string; network: string }[] = [];
    for (const e of entries) {
      for (const n of e.networks ?? []) {
        out.push({ slug: e.slug, network: n.network.toLowerCase() });
      }
    }
    if (out.length === 0) {
      // eslint-disable-next-line no-console
      console.warn(
        `[assets/[slug]/[network]] catalogue returned 0 network pairs; using fallback list`,
      );
      return fallback;
    }
    // eslint-disable-next-line no-console
    console.log(
      `[assets/[slug]/[network]] generating ${out.length} routes from /v1/assets/verified`,
    );
    return out;
  } catch (err) {
    // eslint-disable-next-line no-console
    console.warn(
      `[assets/[slug]/[network]] fetch threw, falling back: ${err instanceof Error ? err.message : String(err)}`,
    );
    return fallback;
  }
}

async function fetchGlobalAsset(slug: string): Promise<GlobalAssetView | null> {
  if (isCIStub) return null;
  try {
    const res = await fetch(
      `${API_BASE_URL}/v1/assets/${encodeURIComponent(slug)}`,
      { signal: AbortSignal.timeout(FETCH_TIMEOUT_MS) },
    );
    if (!res.ok) return null;
    const env = (await res.json()) as {
      data: GlobalAssetView | Record<string, unknown>;
    };
    const d = env.data;
    if (
      typeof d === 'object' &&
      d !== null &&
      typeof (d as GlobalAssetView).ticker === 'string' &&
      Array.isArray((d as GlobalAssetView).networks)
    ) {
      return d as GlobalAssetView;
    }
    return null;
  } catch {
    return null;
  }
}

function findNetwork(
  view: GlobalAssetView,
  network: string,
): NetworkEntry | null {
  const target = network.toLowerCase();
  return view.networks.find((n) => n.network.toLowerCase() === target) ?? null;
}

export async function generateMetadata(props: {
  params: Promise<{ slug: string; network: string }>;
}): Promise<Metadata> {
  const { slug, network } = await props.params;
  const view = await fetchGlobalAsset(slug);
  if (!view) {
    return { title: `${slug} on ${network}` };
  }
  const networkLabel = network.charAt(0).toUpperCase() + network.slice(1);
  return {
    title: `${view.ticker} on ${networkLabel} — Rates Engine`,
    description:
      view.description ?? `${view.name} on ${networkLabel}: issuer, markets, supply.`,
  };
}

export default async function AssetOnNetworkPage(props: {
  params: Promise<{ slug: string; network: string }>;
}) {
  const { slug, network } = await props.params;
  const view = await fetchGlobalAsset(slug);
  if (!view) {
    notFound();
  }
  const entry = findNetwork(view, network);
  if (!entry) {
    return (
      <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
        <Breadcrumb slug={view.slug} ticker={view.ticker} network={network} />
        <header className="space-y-2">
          <h1 className="text-2xl font-semibold tracking-tight text-ink dark:text-slate-100">
            {view.ticker} on {capitalize(network)}
          </h1>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            We don&apos;t have a record of {view.ticker} on{' '}
            {capitalize(network)} in our verified-currency catalogue.
            See{' '}
            <Link href={`/assets/${view.slug}`} className="text-brand-600 hover:underline">
              {view.ticker} cross-chain identity
            </Link>{' '}
            for the networks we know about.
          </p>
        </header>
      </div>
    );
  }

  // Stellar deep dive — the network with the most data; renders
  // issuer, SDEX markets, supply, on-chain history.
  if (entry.network.toLowerCase() === 'stellar') {
    return (
      <StellarDeepDive view={view} entry={entry} />
    );
  }

  // Non-Stellar — contract metadata + external explorer link.
  return (
    <NonStellarDeepDive view={view} entry={entry} />
  );
}

function StellarDeepDive({
  view,
  entry,
}: {
  view: GlobalAssetView;
  entry: NetworkEntry;
}) {
  const assetID = entry.asset_id ?? '';
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <Breadcrumb slug={view.slug} ticker={view.ticker} network="stellar" />
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight text-ink dark:text-slate-100">
          {view.ticker} on Stellar
        </h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          {assetID === 'native' ? (
            <>
              Stellar&apos;s native lumen. Used for transaction fees
              and as the reserve asset for every Stellar account.
            </>
          ) : (
            <>
              {entry.code ?? view.ticker} issued on Stellar by{' '}
              <span className="font-mono text-xs">
                {entry.issuer
                  ? `${entry.issuer.slice(0, 8)}…${entry.issuer.slice(-4)}`
                  : '—'}
              </span>
              . On-chain detail: live SDEX markets, supply via
              SEP-1 / on-chain mint observer, issuer profile.
            </>
          )}
        </p>
      </header>

      {entry.issuer && (
        <Suspense fallback={null}>
          <IssuerPanel gStrkey={entry.issuer} />
        </Suspense>
      )}

      {assetID && (
        <>
          <h2 className="pt-4 text-sm font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400">
            Stellar markets
          </h2>
          <Suspense fallback={null}>
            <MarketsTabPanel assetID={assetID} />
          </Suspense>

          <h2 className="pt-4 text-sm font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400">
            Supply on Stellar
          </h2>
          <Suspense fallback={null}>
            <SupplyTabPanel assetID={assetID} />
          </Suspense>
        </>
      )}
    </div>
  );
}

function NonStellarDeepDive({
  view,
  entry,
}: {
  view: GlobalAssetView;
  entry: NetworkEntry;
}) {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <Breadcrumb
        slug={view.slug}
        ticker={view.ticker}
        network={entry.network}
      />
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight text-ink dark:text-slate-100">
          {view.ticker} on {capitalize(entry.network)}
        </h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          {view.name} on {capitalize(entry.network)}. We don&apos;t
          index {entry.network} trades today — the verified-currency
          catalogue is the source of truth for issuance on this
          chain.
        </p>
      </header>

      <section className="rounded-md border border-slate-200 bg-white p-5 dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-sm font-medium uppercase tracking-wider text-slate-500 dark:text-slate-400">
          Contract
        </h2>
        <dl className="mt-3 space-y-2 text-sm">
          {entry.contract && (
            <div className="flex items-baseline gap-3">
              <dt className="w-24 text-xs uppercase tracking-wider text-slate-500">
                Address
              </dt>
              <dd className="break-all font-mono text-xs text-slate-700 dark:text-slate-300">
                {entry.contract}
              </dd>
            </div>
          )}
          {entry.external_link && (
            <div className="flex items-baseline gap-3">
              <dt className="w-24 text-xs uppercase tracking-wider text-slate-500">
                Explorer
              </dt>
              <dd>
                <a
                  href={entry.external_link}
                  target="_blank"
                  rel="noopener noreferrer"
                  className="text-brand-600 hover:underline"
                >
                  View on {capitalize(entry.network)} explorer →
                </a>
              </dd>
            </div>
          )}
          <div className="flex items-baseline gap-3">
            <dt className="w-24 text-xs uppercase tracking-wider text-slate-500">
              Status
            </dt>
            <dd>
              <span className="rounded bg-slate-100 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300">
                External (not indexed)
              </span>
            </dd>
          </div>
        </dl>
      </section>
    </div>
  );
}

function Breadcrumb({
  slug,
  ticker,
  network,
}: {
  slug: string;
  ticker: string;
  network: string;
}) {
  return (
    <nav className="flex items-baseline gap-2 text-xs text-slate-500">
      <Link href="/assets" className="hover:text-brand-600">
        Assets
      </Link>
      <span aria-hidden>/</span>
      <Link href={`/assets/${slug}`} className="hover:text-brand-600">
        {ticker}
      </Link>
      <span aria-hidden>/</span>
      <span className="text-slate-700 capitalize dark:text-slate-200">
        {network}
      </span>
    </nav>
  );
}

function capitalize(s: string): string {
  if (!s) return s;
  return s.charAt(0).toUpperCase() + s.slice(1);
}
