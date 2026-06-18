import { Suspense } from 'react';
import type { Metadata } from 'next';

import { TransactionsView } from './TransactionsView';

export const metadata: Metadata = {
  title: 'Transactions — recent Stellar network activity',
  description:
    'Recent Stellar transactions, newest ledger first — source account, operation count, result, fee, and memo. Click through for the full decoded transaction. Source: /v1/ledgers/{seq}/transactions.',
  alternates: { canonical: '/transactions' },
};

// Query-param page (reads ?seq= client-side); useSearchParams needs a
// Suspense boundary under output:'export'.
export default function TransactionsPage() {
  return (
    <Suspense fallback={null}>
      <TransactionsView />
    </Suspense>
  );
}
