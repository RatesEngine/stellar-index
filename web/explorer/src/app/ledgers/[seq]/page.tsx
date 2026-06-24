import type { Metadata } from 'next';
import { Suspense } from 'react';

import { LedgerPathView } from './LedgerPathView';

// Shell-only (like transactions): one `shell` sentinel page; the CF Function
// (functions/ledgers/[[path]].js) serves it for any /ledgers/{seq}. Recent
// ledgers can be pre-rendered + indexed later (SEO plan D6) by returning their
// seqs here; until then the long tail is noindex shells.
export const dynamicParams = false;

export function generateStaticParams() {
  return [{ seq: 'shell' }];
}

export const metadata: Metadata = {
  title: 'Ledger — Stellar Index',
  description:
    'Stellar ledger detail: close time, transaction and operation counts, and the transactions in the ledger.',
  robots: { index: false, follow: true },
};

export default function LedgerDetailPage() {
  return (
    <Suspense fallback={null}>
      <LedgerPathView />
    </Suspense>
  );
}
