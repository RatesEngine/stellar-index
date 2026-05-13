import Link from 'next/link';

import { Panel } from '@/components/reveal';
import type { RequestExample } from '@/api/client';

/**
 * Per-network identity rendered on a verified-currency global view
 * page (R-018 Phase 1.5). Mirrors the wire shape served by
 * `/v1/assets/{slug}` when the path is a verified-currency slug —
 * see `internal/api/v1/assets_global.go::NetworkView`.
 */
export interface NetworkEntry {
  network: string;
  /**
   * "indexed" — we ingest this network's trades; the entry carries
   * `asset_id` + `deep_link` to the per-Stellar-asset view.
   * "external" — we know the asset exists there but don't ingest
   * trades; the entry carries `contract` + optional `external_link`.
   */
  data_quality: 'indexed' | 'external';
  // Stellar fields (only when network === "stellar")
  asset_id?: string;
  code?: string;
  issuer?: string;
  deep_link?: string;
  // Non-Stellar fields
  contract?: string;
  external_link?: string;
}

/**
 * NetworksPanel renders the cross-chain identity list for a
 * verified currency. Each row shows:
 *   - the network name
 *   - a "data_quality" badge (indexed vs external)
 *   - for Stellar: a deep_link into the per-asset Stellar view
 *   - for non-Stellar: the contract address + an external explorer link
 *
 * Heuristic explorer-link mapping for common chains; falls back to
 * the server-supplied `external_link` when the catalogue entry
 * carries one. Keeps the operator's catalogue authoritative without
 * forcing every entry to include a URL.
 */
export function NetworksPanel({
  ticker,
  slug,
  networks,
  source,
}: {
  ticker: string;
  /**
   * Catalogue slug (e.g. "usdc"). Used to build the per-network
   * deep-dive route at /assets/{slug}/{network}.
   */
  slug: string;
  networks: NetworkEntry[];
  source: RequestExample;
}) {
  if (networks.length === 0) return null;

  return (
    <Panel
      title={`${ticker} on every network`}
      source={source}
      panelId="networks-panel"
    >
      <table className="w-full table-fixed text-sm">
        <thead>
          <tr className="text-left text-[11px] uppercase tracking-wider text-slate-500">
            <th className="w-1/5 pb-2 font-medium">Network</th>
            <th className="w-1/5 pb-2 font-medium">Status</th>
            <th className="pb-2 font-medium">Address / Link</th>
          </tr>
        </thead>
        <tbody>
          {networks.map((n) => (
            <tr
              key={n.network}
              className="border-t border-slate-200 dark:border-slate-800"
            >
              <td className="py-2 align-top">
                {/* Link to the per-network deep-dive route per
                    R-018 phase 2 — `/assets/{slug}/{network}`. The
                    deep dive holds the issuer info (Stellar) /
                    contract metadata (non-Stellar) that used to
                    live inline on /assets/{slug}. */}
                <Link
                  href={`/assets/${slug}/${n.network}`}
                  className="font-medium capitalize text-brand-600 hover:underline"
                >
                  {n.network}
                </Link>
              </td>
              <td className="py-2 align-top">
                <DataQualityBadge quality={n.data_quality} />
              </td>
              <td className="py-2 align-top">
                {renderAddress(n)}
              </td>
            </tr>
          ))}
        </tbody>
      </table>
    </Panel>
  );
}

function DataQualityBadge({ quality }: { quality: NetworkEntry['data_quality'] }) {
  if (quality === 'indexed') {
    return (
      <span className="rounded bg-emerald-100 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-emerald-800 dark:bg-emerald-900/40 dark:text-emerald-200">
        Indexed
      </span>
    );
  }
  return (
    <span className="rounded bg-slate-100 px-2 py-0.5 text-[11px] font-medium uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300">
      External
    </span>
  );
}

function renderAddress(n: NetworkEntry) {
  // Stellar — deep_link points at the per-Stellar-asset view.
  if (n.network === 'stellar') {
    if (n.asset_id === 'native') {
      return (
        <Link
          href={n.deep_link ?? '/assets/native'}
          className="text-brand-600 hover:underline"
        >
          native (XLM)
        </Link>
      );
    }
    if (n.asset_id && n.deep_link) {
      const display = n.code && n.issuer
        ? `${n.code} · ${n.issuer.slice(0, 6)}…${n.issuer.slice(-4)}`
        : n.asset_id;
      return (
        <Link
          href={n.deep_link}
          className="font-mono text-xs text-brand-600 hover:underline"
          title={n.asset_id}
        >
          {display}
        </Link>
      );
    }
    return <span className="text-slate-400">—</span>;
  }
  // Non-Stellar — contract + optional external link.
  if (!n.contract && !n.external_link) {
    return <span className="text-slate-400">—</span>;
  }
  const href = n.external_link || defaultExternalLink(n.network, n.contract);
  if (href && n.contract) {
    return (
      <a
        href={href}
        target="_blank"
        rel="noopener noreferrer"
        className="font-mono text-xs text-brand-600 hover:underline"
        title={n.contract}
      >
        {`${n.contract.slice(0, 8)}…${n.contract.slice(-6)}`}
      </a>
    );
  }
  if (n.contract) {
    return (
      <span className="font-mono text-xs" title={n.contract}>
        {`${n.contract.slice(0, 8)}…${n.contract.slice(-6)}`}
      </span>
    );
  }
  return (
    <a
      href={href ?? '#'}
      target="_blank"
      rel="noopener noreferrer"
      className="text-brand-600 hover:underline"
    >
      View on {n.network}
    </a>
  );
}

/**
 * Default per-network block-explorer link prefix. The operator can
 * override per-entry via NetworkEntry.external_link; this fallback
 * only fires when the catalogue didn't specify one. Kept tight: the
 * mapping documents what we'd consider canonical for each chain,
 * and unknown networks fall through to no link rather than guessing.
 */
function defaultExternalLink(network: string, contract?: string): string | null {
  if (!contract) return null;
  switch (network) {
    case 'ethereum':
      return `https://etherscan.io/token/${contract}`;
    case 'polygon':
      return `https://polygonscan.com/token/${contract}`;
    case 'base':
      return `https://basescan.org/token/${contract}`;
    case 'arbitrum':
      return `https://arbiscan.io/token/${contract}`;
    case 'avalanche':
      return `https://snowtrace.io/token/${contract}`;
    case 'bsc':
      return `https://bscscan.com/token/${contract}`;
    case 'solana':
      return `https://solscan.io/token/${contract}`;
    case 'tron':
      return `https://tronscan.org/#/token20/${contract}`;
    default:
      return null;
  }
}

