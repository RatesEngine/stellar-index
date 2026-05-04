import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function OraclesPage() {
  return (
    <PlaceholderPage
      title="Oracles"
      blurb="On-chain price oracles on Stellar. Reflector trio (DEX/CEX/FX), Redstone, Band."
      phase="Phase 9.7"
      source={asExample('/v1/oracles')}
      features={[
        'Per-oracle scoreboard — feed count, freshness lag, update cadence',
        'Live divergence vs our independent VWAP',
        'Per-feed history charts',
        'SEP-40 compatibility surface — drop-in replacement for on-chain lastprice() calls',
      ]}
    />
  );
}
