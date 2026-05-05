'use client';

import Link from 'next/link';
import { useSearchParams } from 'next/navigation';

export type CoinTab =
  | 'overview'
  | 'chart'
  | 'markets'
  | 'history'
  | 'supply'
  | 'issuer'
  | 'liquidity';

/**
 * Client tab strip for /coins/[slug]. Reads `?tab=` from URL state;
 * the parent server component renders both overview + chart bodies
 * and toggles visibility based on the active tab.
 *
 * Disabled tabs render as cursor-not-allowed labels until their
 * content lands in subsequent PRs.
 */
export function CoinTabs({ slug, hasIssuer }: { slug: string; hasIssuer: boolean }) {
  const params = useSearchParams();
  const active = (params.get('tab') as CoinTab) || 'overview';

  type T = { key: CoinTab; label: string; disabled?: boolean };
  const tabs: T[] = [
    { key: 'overview', label: 'Overview' },
    { key: 'chart', label: 'Chart' },
    { key: 'markets', label: 'Markets' },
    { key: 'history', label: 'History' },
    { key: 'supply', label: 'Supply' },
    ...(hasIssuer ? ([{ key: 'issuer', label: 'Issuer' }] as const) : []),
    { key: 'liquidity', label: 'Liquidity', disabled: true },
  ];

  return (
    <nav className="flex gap-1 overflow-x-auto border-b border-slate-200 text-sm dark:border-slate-800">
      {tabs.map((t) =>
        t.disabled ? (
          <span
            key={t.key}
            className="cursor-not-allowed border-b-2 border-transparent px-3 py-2 text-slate-400 dark:text-slate-600"
            title="Coming soon"
          >
            {t.label}
          </span>
        ) : (
          <Link
            key={t.key}
            href={
              t.key === 'overview' ? `/coins/${slug}` : `/coins/${slug}?tab=${t.key}`
            }
            className={`border-b-2 px-3 py-2 ${
              t.key === active
                ? 'border-brand-500 font-medium text-brand-600 dark:text-brand-400'
                : 'border-transparent text-slate-600 hover:text-brand-600 dark:text-slate-300'
            }`}
          >
            {t.label}
          </Link>
        ),
      )}
    </nav>
  );
}

export function ActiveTabSlot({
  overview,
  chart,
  markets,
  history,
  supply,
  issuer,
}: {
  overview: React.ReactNode;
  chart: React.ReactNode;
  markets?: React.ReactNode;
  history?: React.ReactNode;
  supply?: React.ReactNode;
  issuer?: React.ReactNode;
}) {
  return (
    <ActiveBody
      overview={overview}
      chart={chart}
      markets={markets}
      history={history}
      supply={supply}
      issuer={issuer}
    />
  );
}

function ActiveBody({
  overview,
  chart,
  markets,
  history,
  supply,
  issuer,
}: {
  overview: React.ReactNode;
  chart: React.ReactNode;
  markets?: React.ReactNode;
  history?: React.ReactNode;
  supply?: React.ReactNode;
  issuer?: React.ReactNode;
}) {
  const params = useSearchParams();
  const tab = (params.get('tab') as CoinTab) || 'overview';
  if (tab === 'chart') return <>{chart}</>;
  if (tab === 'markets' && markets) return <>{markets}</>;
  if (tab === 'history' && history) return <>{history}</>;
  if (tab === 'supply' && supply) return <>{supply}</>;
  if (tab === 'issuer' && issuer) return <>{issuer}</>;
  return <>{overview}</>;
}
