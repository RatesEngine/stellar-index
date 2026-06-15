import type { Metadata } from 'next';

import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES } from '@/lib/seo';
import { ProtocolsIndex } from './ProtocolsIndex';

export const metadata: Metadata = {
  title: 'Protocols — every Stellar DeFi protocol, verified',
  description:
    'Complete per-protocol on-chain analytics for every major Stellar protocol — Soroswap, Phoenix, Aquarius, Blend, DeFindex, CCTP, the Reflector trio and more. Contract roster, event-type breakdown and verified completeness for each. Source: /v1/protocols.',
  alternates: { canonical: '/protocols' },
  openGraph: {
    title: 'Protocols — every Stellar DeFi protocol, verified',
    description:
      'Per-protocol on-chain analytics, contract rosters and event-type breakdowns for every major Stellar protocol.',
    url: 'https://stellarindex.io/protocols',
    type: 'website',
    images: SITE_OG_IMAGES,
  },
  twitter: {
    card: 'summary_large_image',
    title: 'Protocols — every Stellar DeFi protocol, verified',
    description:
      'Per-protocol on-chain analytics for every major Stellar protocol.',
    images: SITE_TWITTER_IMAGES,
  },
};

export default function ProtocolsPage() {
  return <ProtocolsIndex />;
}
