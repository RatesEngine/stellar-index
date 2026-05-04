import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function NetworkPage() {
  return (
    <PlaceholderPage
      title="Network"
      blurb="Stellar macro pulse — total TVL, total volume, Soroban activity, fee market, peg health."
      phase="Phase 12.1"
      source={asExample('/v1/network/health')}
      features={[
        'Total Stellar TVL + multi-window deltas',
        'Total network volume across all venues',
        'Soroban activity index — composite of deploys, upgrades, invocations',
        'Stablecoin peg health strip — USDC/USD, EURC/EUR, MXNe/MXN deviations',
        'Operations per ledger throughput chart',
        'Fee market history',
      ]}
    />
  );
}
