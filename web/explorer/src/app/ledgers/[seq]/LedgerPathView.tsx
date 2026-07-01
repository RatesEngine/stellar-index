'use client';

import { useLastPathSegment } from '@/lib/useLastPathSegment';

import { LedgerView } from '../../ledger/LedgerView';

// Reads the real ledger sequence from the path at runtime (the CF Function
// serves the one built shell for any /ledgers/{seq}). See the transactions
// route for the full rationale (SEO plan D1; Spike A).
export function LedgerPathView() {
  const seq = useLastPathSegment();
  return <LedgerView seq={seq} />;
}
