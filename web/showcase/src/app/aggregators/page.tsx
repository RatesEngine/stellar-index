import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function AggregatorsPage() {
  return (
    <PlaceholderPage
      title="Aggregators"
      blurb="Yield aggregators + vault wrappers on Stellar. DeFindex and friends."
      phase="Phase 9.5"
      source={asExample('/v1/protocols', { kind: 'aggregator' })}
      features={[
        'AUM + vault count + 7d net flow per aggregator',
        'Top-3 underlying-protocol exposures (Blend, Aquarius, Soroswap)',
        'Per-vault drill-down with capital allocation chart',
        'Routed-via attribution — what % of underlying-protocol volume came in via this aggregator',
      ]}
    />
  );
}
