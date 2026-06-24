import type { Metadata } from 'next';
import { Suspense } from 'react';

import { TxPathView } from './TxPathView';

// output:'export' — only the params returned here become files. Transactions
// are unbounded with ≈zero individual search value, so we DON'T pre-render
// them: one `shell` sentinel page is built, and the CF Pages Function
// (functions/transactions/[[path]].js) serves it for any /transactions/{hash}
// while the client reads the real hash from the path (TxPathView).
export const dynamicParams = false;

export function generateStaticParams() {
  return [{ hash: 'shell' }];
}

export const metadata: Metadata = {
  title: 'Transaction — Stellar Index',
  description:
    'Stellar transaction detail: summary, decoded operations, and the contract events it emitted.',
  // Long-tail entity shells are noindex (we don't want millions of thin tx
  // pages in the index). Curated/linked entities get indexed via their own
  // pre-rendered routes; this shell serves arbitrary shared/edited URLs.
  robots: { index: false, follow: true },
};

export default function TransactionDetailPage() {
  return (
    <Suspense fallback={null}>
      <TxPathView />
    </Suspense>
  );
}
