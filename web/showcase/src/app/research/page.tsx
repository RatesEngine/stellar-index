import { PlaceholderPage } from '@/components/panels/PlaceholderPage';
import { asExample } from '@/api/client';

export default function ResearchPage() {
  return (
    <PlaceholderPage
      title="Research"
      blurb="Articles, post-mortems, deep dives. Every claim deep-links into the live data via the time machine."
      phase="Phase 12.6"
      source={asExample('/research', { format: 'index' })}
      features={[
        'MDX-authored posts in posts/*.md',
        'Custom <RatesLink> + <RatesPanel> primitives that deep-link into the live site at a specific ledger',
        'Time-machine integration — a post about a 2025-03-14 incident scrubs every panel to that moment',
        'Tagged + sorted by date',
      ]}
    />
  );
}
