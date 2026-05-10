import type { Metadata } from 'next';

import { DexesView } from './DexesView';

export const metadata: Metadata = {
  title: 'DEXes — AMMs and order books on Stellar',
  description:
    'Live 24h volume + trade count + pool count for every Stellar DEX we ingest — Soroswap, Phoenix, Aquarius, SDEX, Comet. Source: /v1/sources?include=stats.',
};

export default function DexesPage() {
  return <DexesView />;
}
