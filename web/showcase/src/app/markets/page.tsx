import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function MarketsPage() {
  return (
    <PlaceholderPage
      title="Markets"
      blurb="Every trading pair on Stellar — sortable by volume, spread, depth, recent activity."
      phase="Phase 8.10"
      source={asExample('/v1/markets', { sort: 'volume_24h_usd:desc', limit: 100 })}
      features={[
        'Sortable pair table (base × quote)',
        'Heatmap grid: base on rows, quotes on columns, cell coloured by 24h % change',
        'Per-venue sub-tables (/markets/sdex, /markets/soroswap, …)',
        'Live tape — new trades streaming in via /v1/observations/stream',
      ]}
    />
  );
}
