import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function DivergencesPage() {
  return (
    <PlaceholderPage
      title="Divergences"
      blurb="Cross-reference monitor. Our independent VWAP vs Chainlink, CoinGecko, Reflector, Redstone, Band."
      phase="Phase 12.4"
      source={asExample('/v1/divergences')}
      features={[
        'Per-(asset, reference) live state — our VWAP vs ref, delta %, status',
        'Filter by reference: Chainlink / CoinGecko / Reflector / Redstone / Band',
        'Historical chart — one pair, one reference, delta % over time',
        'Powers the per-incident post-mortem deep-link pattern',
      ]}
    />
  );
}
