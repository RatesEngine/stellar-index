import type { Metadata } from 'next';
import Link from 'next/link';

import { Panel } from '@/components/reveal';

export const metadata: Metadata = {
  title: 'Anomalies — freeze and outlier timeline',
  description:
    'Every clear→firing freeze transition, with reason + recovery + frozen-value detail. Powered by the freeze-event durable mirror per ADR-0019.',
  alternates: { canonical: '/anomalies' },
};

const REASONS: { name: string; trigger: string; meaning: string }[] = [
  {
    name: 'single_source',
    trigger: 'Only one source contributing in the window',
    meaning:
      "We refuse to serve a price that's based on a single venue. The pair freezes until at least one additional source is observed contributing.",
  },
  {
    name: 'divergence',
    trigger: 'Persistent gap vs an external reference',
    meaning:
      "Our VWAP and an authority reference (CoinGecko, Chainlink HTTP, or a Reflector feed) have been diverging beyond threshold for too long. Almost always means a decoder bug or a stuck source.",
  },
  {
    name: 'outlier_storm',
    trigger: 'Many trades flagged as outliers within a tight window',
    meaning:
      'The aggregator\'s outlier filter rejected a high fraction of recent contributions. Usually a ledger-level shock; the freeze prevents the surviving inliers from setting a misleading "VWAP".',
  },
  {
    name: 'manual',
    trigger: 'Operator-initiated freeze',
    meaning:
      'An operator triggered the freeze via a Redis-direct write — used during incident response to halt serving for one pair without taking the whole API down.',
  },
];

export default function AnomaliesPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Anomalies</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Every clear→firing freeze transition, with reason +
          recovery + the frozen value still served via{' '}
          <code className="font-mono text-xs">/v1/price</code>. Powered
          by the freeze-event durable mirror —{' '}
          <code className="font-mono text-xs">freeze_events</code>{' '}
          hypertable (migration 0018), populated alongside the load-
          bearing Redis marker.
        </p>
      </header>

      <Panel
        title="What freezes a pair"
        bodyClassName="space-y-3"
      >
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Per{' '}
          <Link
            href="/research/adr/0019"
            className="underline decoration-dotted"
          >
            ADR-0019
          </Link>
          , a freeze fires when one of these conditions holds. While
          frozen, the API still serves the last good value — but with{' '}
          <code className="font-mono text-xs">flags.frozen=true</code>
          {' '}so consumers know not to act on it.
        </p>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          {REASONS.map((r) => (
            <div
              key={r.name}
              className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs dark:border-slate-800 dark:bg-slate-900/50"
            >
              <div className="flex items-baseline justify-between">
                <code className="font-mono text-[11px] text-down-strong">
                  {r.name}
                </code>
              </div>
              <div className="mt-1.5 text-[11px] uppercase tracking-wider text-slate-500">
                {r.trigger}
              </div>
              <p className="mt-1.5 text-slate-600 dark:text-slate-400">
                {r.meaning}
              </p>
            </div>
          ))}
        </div>
      </Panel>

      <Panel
        title="Coming next"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          Currently-firing list, freeze timeline, per-asset rate,
          per-reason breakdown, and the calendar heatmap of daily
          counts all plumb in once the{' '}
          <code className="font-mono text-xs">/v1/anomalies</code>{' '}
          endpoint ships (Phase 5). The underlying mirror is already
          running on r1 — see the freeze-event sink listed in the{' '}
          <Link href="/research" className="underline decoration-dotted">
            research index
          </Link>
          .
        </p>
      </Panel>
    </div>
  );
}
