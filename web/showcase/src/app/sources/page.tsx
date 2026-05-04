import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function SourcesPage() {
  return (
    <PlaceholderPage
      title="Sources"
      blurb="Where the prices come from. Every exchange, AMM, oracle, and FX feed we ingest."
      phase="Phase 8.x — sources directory"
      source={asExample('/v1/sources', { include: 'health' })}
      features={[
        'Per-source health: events seen 24h, decode errors, orphan rate, last decoded ledger',
        'Reliability scoreboard — rolling 30d uptime, mean lag, error rate',
        'VWAP-weight history per pair × source over time',
        'WASM history (Soroban sources) with side-by-side WAT diff between versions',
      ]}
    />
  );
}
