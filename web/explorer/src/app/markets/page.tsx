import { Suspense } from 'react';
import type { Metadata } from 'next';

import { Container, PageHeader, Skeleton } from '@/components/ui';
import { MarketsTable } from './MarketsTable';

export const metadata: Metadata = {
  title: 'Markets — every active trading pair',
  description:
    'Every (base, quote) pair that has traded on Stellar in the last 14 days. Sortable by 24h trade count, with last-trade-relative timestamps.',
};

/**
 * /markets — every active trading pair on Stellar.
 *
 * v0 wires the live `/v1/markets` endpoint with a sortable table.
 * The pair-heatmap, per-venue sub-tables, and live tape (via
 * `/v1/observations/stream`) follow as their underlying data
 * surfaces stabilise.
 */
export default function MarketsPage() {
  return (
    <Container className="space-y-8 py-8 sm:py-10">
      <PageHeader
        eyebrow="Trading pairs"
        title="Markets"
        description="Every (base, quote) pair that has traded on Stellar in the last 14 days. Heatmap, per-venue sub-tables, and a live trade tape land in subsequent passes."
      />

      <Suspense fallback={<Skeleton className="h-96 w-full" />}>
        <MarketsTable />
      </Suspense>
    </Container>
  );
}
