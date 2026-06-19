import { Suspense } from 'react';
import type { Metadata } from 'next';

import { OperationView } from './OperationView';

export const metadata: Metadata = {
  alternates: { canonical: '/operation' },
  title: 'Operation — Stellar operation detail',
  description:
    'Full detail for a single Stellar operation: type, decoded body fields, result, the events it emitted, and links to its transaction and ledger.',
};

/**
 * /operation?tx=H&i=N — single-operation detail (ADR-0038 Phase D).
 *
 * Query-param page (NOT a dynamic route): an operation is identified
 * by its containing tx hash + op index, both unbounded. The shell
 * hydrates, reads ?tx= / ?i= client-side, fetches /v1/tx/{hash}, and
 * isolates the one operation (plus the events it emitted).
 */
export default function OperationPage() {
  return (
    <Suspense fallback={null}>
      <OperationView />
    </Suspense>
  );
}
