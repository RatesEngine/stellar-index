import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function DexesPage() {
  return (
    <PlaceholderPage
      title="DEXes"
      blurb="AMMs + order books on Stellar. Soroswap, Phoenix, Aquarius, SDEX — TVL, volume, pair count, status badge."
      phase="Phase 9.1"
      source={asExample('/v1/protocols', { kind: 'amm', sort: 'tvl:desc' })}
      features={[
        'Scoreboard with TVL + multi-window deltas + sparkline per protocol',
        'Status badge: 🔥 Surging / ↗ Growing / ↔ Stable / ⚠ Cooling / 🆘 Declining',
        'Acceleration leaderboard — protocols whose growth is speeding up',
        'TVL-share stacked area showing how dominance has shifted over time',
      ]}
    />
  );
}
