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

interface VerifiedCurrencyListItem {
  ticker: string;
  slug: string;
  name: string;
  class?: string;
  networks: NetworkEntry[];
}

/**
 * Build-time catalogue source. Both generateStaticParams AND
 * fetchGlobalAsset derive from the same single `/v1/assets/verified`
 * call — without this consolidation, the build fires hundreds of
 * `/v1/assets/{slug}` requests in parallel and trips r1's anonymous-
 * tier rate limit (60 req/min), producing prerendered "Not found"
 * pages for every [slug]/[network] route. The catalogue listing
 * already carries `networks[]` per entry, so per-slug fetches are
 * redundant at build time.
 *
 * Sequential retry on 429 (small jittered backoff) because even
 * the single call occasionally races a parallel /assets/[slug]
 * build phase that's hammering the same endpoint.
 */
let cataloguePromise: Promise<Map<string, GlobalAssetView>> | null = null;

function getCatalogue(): Promise<Map<string, GlobalAssetView>> {
  if (cataloguePromise) return cataloguePromise;
  cataloguePromise = fetchCatalogueWithRetry();
  return cataloguePromise;
}

async function fetchCatalogueWithRetry(): Promise<Map<string, GlobalAssetView>> {
  if (isCIStub) return new Map();
  const maxAttempts = 5;
  for (let attempt = 0; attempt < maxAttempts; attempt++) {
    try {
      const res = await fetch(`${API_BASE_URL}/v1/assets/verified`, {
        signal: AbortSignal.timeout(FETCH_TIMEOUT_MS),
      });
      if (res.status === 429) {
        const backoffMs = 1000 * (attempt + 1) + Math.floor(Math.random() * 500);
        // eslint-disable-next-line no-console
        console.warn(
          `[catalogue] /v1/assets/verified 429 (attempt ${attempt + 1}/${maxAttempts}); backing off ${backoffMs}ms`,
        );
        await new Promise((r) => setTimeout(r, backoffMs));
        continue;
      }
      if (!res.ok) {
        // eslint-disable-next-line no-console
        console.warn(`[catalogue] /v1/assets/verified http=${res.status}`);
        return new Map();
      }
      const env = (await res.json()) as { data?: VerifiedCurrencyListItem[] };
      const map = new Map<string, GlobalAssetView>();
      for (const item of env.data ?? []) {
        map.set(item.slug, {
          ticker: item.ticker,
          slug: item.slug,
          name: item.name,
          class: item.class as GlobalAssetView['class'],
          networks: item.networks ?? [],
        });
      }
      return map;
    } catch (err) {
      // eslint-disable-next-line no-console
      console.warn(
        `[catalogue] fetch threw (attempt ${attempt + 1}): ${err instanceof Error ? err.message : String(err)}`,
      );
    }
  }
  return new Map();
}

/**
 * Static-export enumeration of every (slug, network) pair the
 * catalogue knows about. Emits all four case variants per slug
 * (lower / upper / Capitalized / as-stored) × all known network
 * casings so user-typed URLs and existing table links both
 * resolve. The catalogue has <50 entries so 4× expansion is
 * cheap (still well under 1000 routes for the whole network grid).
 */
export async function generateStaticParams(): Promise<
  { slug: string; network: string }[]
> {
  const fallback = [{ slug: 'usdc', network: 'stellar' }];
  if (isCIStub) return fallback;
  const map = await getCatalogue();
  const out: { slug: string; network: string }[] = [];
  const seen = new Set<string>();
  for (const view of map.values()) {
    const slugVariants = caseVariants(view.slug);
    for (const n of view.networks) {
      const netVariants = caseVariants(n.network);
      for (const s of slugVariants) {
        for (const nv of netVariants) {
          const key = `${s}|${nv}`;
          if (!seen.has(key)) {
            seen.add(key);
            out.push({ slug: s, network: nv });
          }
        }
      }
    }
  }
  if (out.length === 0) {
    // eslint-disable-next-line no-console
    console.warn(`[assets/[slug]/[network]] catalogue empty; using fallback`);
    return fallback;
  }
  return out;
}

function caseVariants(s: string): string[] {
  const lower = s.toLowerCase();
  const upper = s.toUpperCase();
  const cap = lower.charAt(0).toUpperCase() + lower.slice(1);
  const variants = new Set<string>([s, lower, upper, cap]);
  return Array.from(variants);
}

async function fetchGlobalAsset(slug: string): Promise<GlobalAssetView | null> {
  if (isCIStub) return null;
  const map = await getCatalogue();
  // Case-insensitive lookup — generateStaticParams emits multiple
  // case variants, all of which should resolve to the catalogue's
  // canonical (lowercase) entry.
  const hit = map.get(slug);
  if (hit) return hit;
  return map.get(slug.toLowerCase()) ?? null;
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
    // eslint-disable-next-line no-console
    console.error(`[assets/[slug]/[network]] fetchGlobalAsset(${slug}) returned null at build time`);
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
