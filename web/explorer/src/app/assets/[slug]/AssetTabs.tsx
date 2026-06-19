'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';

export type AssetTab =
  | 'overview'
  | 'chart'
  | 'markets'
  | 'history'
  | 'supply'
  | 'holders'
  | 'issuer'
  | 'liquidity';

/**
 * Client tab strip for /assets/[slug]. Reads `?tab=` from URL state;
 * the parent server component renders all tab bodies and toggles
 * visibility based on the active tab.
 */
export function AssetTabs({ slug, hasIssuer }: { slug: string; hasIssuer: boolean }) {
  const params = useSearchParams();
  // Chart is the default (the bare /assets/{slug} URL) — the page leads
  // with price action, with the dense stats always present in the rail.
  const active = (params.get('tab') as AssetTab) || 'chart';

  type T = { key: AssetTab; label: string };
  const tabs: T[] = [
    { key: 'chart', label: 'Chart' },
    { key: 'markets', label: 'Markets' },
    { key: 'history', label: 'Trades' },
    { key: 'holders', label: 'Holders' },
    { key: 'liquidity', label: 'Liquidity' },
    { key: 'supply', label: 'Supply' },
    ...(hasIssuer ? ([{ key: 'issuer', label: 'Issuer' }] as const) : []),
    { key: 'overview', label: 'About' },
  ];

  return (
    <nav className="flex gap-1 overflow-x-auto border-b border-line text-sm">
      {tabs.map((t) => (
        <Link
          key={t.key}
          href={
            t.key === 'chart' ? `/assets/${slug}` : `/assets/${slug}?tab=${t.key}`
          }
          aria-current={t.key === active ? 'page' : undefined}
          className={`border-b-2 px-3 py-2 ${
            t.key === active
              ? 'border-brand-500 font-medium text-brand-600'
              : 'border-transparent text-ink-body hover:text-brand-600'
          }`}
        >
          {t.label}
        </Link>
      ))}
    </nav>
  );
}

export function ActiveTabSlot({
  overview,
  chart,
  markets,
  history,
  supply,
  holders,
  issuer,
  liquidity,
}: {
  overview: React.ReactNode;
  chart: React.ReactNode;
  markets?: React.ReactNode;
  history?: React.ReactNode;
  supply?: React.ReactNode;
  holders?: React.ReactNode;
  issuer?: React.ReactNode;
  liquidity?: React.ReactNode;
}) {
  return (
    <ActiveBody
      overview={overview}
      chart={chart}
      markets={markets}
      history={history}
      supply={supply}
      holders={holders}
      issuer={issuer}
      liquidity={liquidity}
    />
  );
}

function ActiveBody({
  overview,
  chart,
  markets,
  history,
  supply,
  holders,
  issuer,
  liquidity,
}: {
  overview: React.ReactNode;
  chart: React.ReactNode;
  markets?: React.ReactNode;
  history?: React.ReactNode;
  supply?: React.ReactNode;
  holders?: React.ReactNode;
  issuer?: React.ReactNode;
  liquidity?: React.ReactNode;
}) {
  const params = useSearchParams();
  const tab = (params.get('tab') as AssetTab) || 'chart';
  if (tab === 'overview') return <>{overview}</>;
  if (tab === 'markets' && markets) return <>{markets}</>;
  if (tab === 'history' && history) return <>{history}</>;
  if (tab === 'supply' && supply) return <>{supply}</>;
  if (tab === 'holders' && holders) return <>{holders}</>;
  if (tab === 'issuer' && issuer) return <>{issuer}</>;
  if (tab === 'liquidity' && liquidity) return <>{liquidity}</>;
  return <>{chart}</>;
}
