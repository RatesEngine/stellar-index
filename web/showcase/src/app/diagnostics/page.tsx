import { CursorsTable } from './CursorsTable';

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
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Diagnostics</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Public system-health view. Today: live per-source ingest cursors
          straight from <code className="font-mono">/v1/diagnostics/cursors</code>.
          Decoder coverage, archive completeness, cross-region consistency, and
          SLO burn rates plumb in as their endpoints ship.
        </p>
      </header>

      <CursorsTable />
    </div>
  );
}
