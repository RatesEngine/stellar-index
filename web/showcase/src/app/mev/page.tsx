import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function MevPage() {
  return (
    <PlaceholderPage
      title="MEV"
      blurb="Suspicious-pattern detector feed. Sandwiches, oracle deviations, liquidation cascades, wash trading."
      phase="Phase 12.5"
      source={asExample('/v1/mev', { since: '24h', limit: 50 })}
      features={[
        'Auto-flagged events with confidence score per pattern',
        'Per-kind tally — counts last 7d / 30d',
        'Event detail — tx hashes, accounts, profit estimate, full timeline',
        'Cross-references to /coins/{id} + /tx/{hash} for drill-in',
      ]}
    />
  );
}
