import type { Metadata } from 'next';

import { NetworkView } from './NetworkView';

export const metadata: Metadata = {
  alternates: { canonical: '/network' },
  title: 'Network — Stellar throughput + stats',
  description:
    'Stellar network throughput over time — operations, transactions, contract events, and ledgers per day — plus the live at-a-glance network snapshot.',
};

/** /network — network throughput time-series + the current snapshot. */
export default function NetworkPage() {
  return <NetworkView />;
}
