import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function AnomaliesPage() {
  return (
    <PlaceholderPage
      title="Anomalies"
      blurb="Freeze + anomaly timeline. Every clear→firing transition with reason + recovery + frozen-value detail."
      phase="Phase 12.3"
      source={asExample('/v1/anomalies', { since: '24h', limit: 50 })}
      features={[
        'Currently firing — active freezes with full breakdown',
        'Freeze timeline — last N events sorted by recency',
        'Per-asset rate — count of freezes per asset over a window',
        'Per-reason breakdown — single_source / divergence / outlier_storm / manual',
        'Calendar heatmap of daily anomaly count',
      ]}
    />
  );
}
