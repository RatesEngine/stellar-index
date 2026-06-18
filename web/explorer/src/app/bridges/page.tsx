import type { Metadata } from 'next';

import { ProtocolsIndex } from '../protocols/ProtocolsIndex';

export const metadata: Metadata = {
  title: 'Bridges — cross-chain settlement on Stellar',
  description:
    'Circle CCTP v2 (canonical burn-and-mint USDC) and Rozo (intent-bridge payment settlement) — the cross-chain bridges we index on Stellar. Per-bridge contract roster, event distribution, and verified-completeness verdict. Source: /v1/protocols.',
  alternates: { canonical: '/bridges' },
};

export default function BridgesPage() {
  return (
    <ProtocolsIndex
      lockedCategory="bridge"
      eyebrow="Cross-chain"
      title="Bridges"
      description="Cross-chain settlement on Stellar — Circle CCTP v2 (canonical burn-and-mint USDC) and Rozo (intent-bridge payments). Each bridge page carries its full contract roster, the distribution of every event type it emits, and a verified-completeness verdict against the certified ledger lake."
    />
  );
}
