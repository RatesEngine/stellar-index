import type { Metadata } from 'next';
import { BackfillSummary } from './BackfillSummary';
import { CursorsTable } from './CursorsTable';
import { HealthSummary } from './HealthSummary';

export const metadata: Metadata = {
  title: 'Diagnostics — public system-health view',
  description:
    'Live ingest cursors, archive completeness, decoder coverage. Watch each indexer source tick in real time.',
};

/**
 * /diagnostics — public system-health view.
 *
 * v0 ships only the live ingest-cursor table backed by
 * `/v1/diagnostics/pulse`-adjacent data. The remaining panels
 * (decoder coverage, archive completeness, cross-region consistency,
 * SLO burn rates) land as their underlying endpoints ship.
 */
export default function DiagnosticsPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Diagnostics</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Public system-health view. Today: live per-source ingest cursors
          straight from <code className="font-mono">/v1/diagnostics/cursors</code>.
          Decoder coverage, archive completeness, cross-region consistency, and
          SLO burn rates plumb in as their endpoints ship.
        </p>
      </header>

      <section className="space-y-2">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-slate-500">
          Live ingest
        </h2>
        <HealthSummary />
      </section>

      <section className="space-y-2">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-slate-500">
          Backfill workers
        </h2>
        <BackfillSummary />
      </section>

      <CursorsTable />
    </div>
  );
}
