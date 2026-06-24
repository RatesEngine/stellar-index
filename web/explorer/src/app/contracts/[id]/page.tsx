import type { Metadata } from 'next';
import { Suspense } from 'react';

import { ContractPathView } from './ContractPathView';

// Shell-only for now; active contracts (top by event activity) will be
// pre-rendered + indexed via generateStaticParams (SEO plan D6). Until then the
// long tail is noindex shells served by functions/contracts/[[path]].js.
export const dynamicParams = false;

export function generateStaticParams() {
  return [{ id: 'shell' }];
}

export const metadata: Metadata = {
  title: 'Contract — Stellar Index',
  description:
    'Soroban contract detail: WASM, exports, events, and state for a Stellar smart contract.',
  robots: { index: false, follow: true },
};

export default function ContractDetailPage() {
  return (
    <Suspense fallback={null}>
      <ContractPathView />
    </Suspense>
  );
}
