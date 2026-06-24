import type { Metadata } from 'next';
import Link from 'next/link';

import { CategoryHub } from '@/components/CategoryHub';

export const metadata: Metadata = {
  title: 'AMM protocols on Stellar — pools, swaps & volume',
  description:
    'Automated market makers on Stellar (Soroban): Soroswap, Aquarius, Phoenix, and Comet — constant-product and weighted pools, swap events, and trading volume, indexed per protocol.',
  alternates: { canonical: '/amm' },
  openGraph: { title: 'Stellar AMM protocols', description: 'Soroban AMMs on Stellar: pools, swaps, and volume.', url: 'https://stellarindex.io/amm', type: 'website' },
};

export default function AmmPage() {
  return (
    <CategoryHub
      category="amm"
      title="AMM protocols"
      description="Automated market makers on Stellar — Soroban contracts running constant-product and weighted liquidity pools. Each protocol page has live pool, swap, and volume analytics."
      footnote={
        <>
          Looking for the order book? See{' '}
          <Link href="/sdex" className="text-brand-600 hover:underline">SDEX</Link>. For
          the protocol-native liquidity pools (not Soroban AMMs), see{' '}
          <Link href="/liquidity-pools" className="text-brand-600 hover:underline">liquidity pools</Link>.
        </>
      }
    />
  );
}
