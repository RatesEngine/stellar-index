'use client';

import { useEffect, useState } from 'react';

import { LedgerView } from '../../ledger/LedgerView';

// Reads the real ledger sequence from the path at runtime (the CF Function
// serves the one built shell for any /ledgers/{seq}). See the transactions
// route for the full rationale (SEO plan D1; Spike A).
export function LedgerPathView() {
  const [seq, setSeq] = useState('');
  useEffect(() => {
    const s =
      window.location.pathname.replace(/\/+$/, '').split('/').pop() ?? '';
    setSeq(decodeURIComponent(s));
  }, []);
  return <LedgerView seq={seq} />;
}
