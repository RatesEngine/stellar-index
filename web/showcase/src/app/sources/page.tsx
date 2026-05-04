import { SourcesTable } from './SourcesTable';

/**
 * /sources — directory of every venue we ingest.
 *
 * Live-data pass: groups by class (exchange / aggregator / oracle /
 * authority_sanity) so the "only Class=exchange contributes to VWAP
 * by default" boundary is visible at a glance. Per-source health
 * metrics (events seen 24h, decode errors, orphan rate, last
 * decoded ledger) plumb in once `/v1/sources/{name}/health` ships;
 * the WASM-history pane lands once decoder_stats + wasm_versions
 * are joined into the response.
 */
export default function SourcesPage() {
  return (
    <div className="mx-auto max-w-6xl space-y-6 p-6">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Sources</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Every venue we ingest, grouped by class. Only Class=exchange sources
          contribute to VWAP by default — aggregators and oracles are reported
          alongside but excluded so we don&apos;t double-count upstream markets
          or import their methodology.
        </p>
      </header>

      <SourcesTable />
    </div>
  );
}
