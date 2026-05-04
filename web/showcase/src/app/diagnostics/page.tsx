import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function DiagnosticsPage() {
  return (
    <PlaceholderPage
      title="Diagnostics"
      blurb="Public system-health view. Ingest lag, archive completeness, cross-region consistency, decoder coverage."
      phase="Phase 12.2"
      source={asExample('/v1/diagnostics/pulse')}
      features={[
        'Pulse banner — last ledger, lag vs network tip, ingest rate',
        'Decoder coverage table per source (events seen, errors, orphans)',
        'Archive completeness % per region (R1/R2/R3)',
        'Cross-region price consistency check (per ADR-0015)',
        'Backfill cursor positions',
        'WASM-decoder coverage map (which decoder claims each WASM hash)',
        'SLO multi-window burn rates (per ADR-0009)',
      ]}
    />
  );
}
