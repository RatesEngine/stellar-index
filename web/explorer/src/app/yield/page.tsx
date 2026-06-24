import type { Metadata } from 'next';

import { CategoryHub } from '@/components/CategoryHub';

export const metadata: Metadata = {
  title: 'Yield protocols on Stellar — vaults & strategies',
  description:
    'Yield protocols on Stellar (Soroban): DeFindex and others — vaults, strategies, and the assets they deploy across Stellar DeFi, indexed per protocol.',
  alternates: { canonical: '/yield' },
  openGraph: { title: 'Stellar yield protocols', description: 'Vaults and strategies on Stellar.', url: 'https://stellarindex.io/yield', type: 'website' },
};

export default function YieldPage() {
  return (
    <CategoryHub
      category="yield"
      title="Yield protocols"
      description="Yield vaults and strategies on Stellar — Soroban contracts that route deposits across DeFi for return. Each protocol page tracks its vaults and deployed assets."
    />
  );
}
