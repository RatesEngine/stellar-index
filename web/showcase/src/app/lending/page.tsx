import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function LendingPage() {
  return (
    <PlaceholderPage
      title="Lending"
      blurb="Collateralized lending protocols on Stellar. Blend (with Comet auction backstop folded in)."
      phase="Phase 9.3"
      source={asExample('/v1/protocols', { kind: 'lending' })}
      features={[
        'Per-pool TVL + utilization + supply/borrow APY',
        'Auctions panel — current + historical liquidations with bid/lot detail',
        'Backstop coverage % per pool',
        'Reflector dependency badge — links to the oracle, divergence indicator',
      ]}
    />
  );
}
