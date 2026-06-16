import type { Metadata } from 'next';

import { Container, PageHeader, SectionHeader } from '@/components/ui';
import { BackfillSummary } from './BackfillSummary';
import { CursorsTable } from './CursorsTable';
import { HealthSummary } from './HealthSummary';

export const metadata: Metadata = {
  title: 'Diagnostics — public system-health view',
  description:
    'Live ingest cursors, archive completeness, decoder coverage. Watch each indexer source tick in real time.',
  alternates: { canonical: '/diagnostics' },
};

/**
 * /diagnostics — public system-health view.
 *
 * v0 ships only the live ingest-cursor table backed by
 * `/v1/diagnostics/pulse`-adjacent data. The remaining panels
 * (decoder coverage, archive completeness, cross-region consistency,
 * SLO burn rates) land as their underlying endpoints ship.
 */
export default function DiagnosticsPage() {
  return (
    <Container className="space-y-10 py-8 sm:py-10">
      <PageHeader
        eyebrow="System health"
        title="Diagnostics"
        description={
          <>
            Public system-health view. Today: live per-source ingest cursors
            straight from{' '}
            <code className="rounded bg-surface-subtle px-1 font-mono text-[13px]">
              /v1/diagnostics/cursors
            </code>
            . Decoder coverage, archive completeness, cross-region consistency,
            and SLO burn rates plumb in as their endpoints ship.
          </>
        }
      />

      <section className="space-y-4">
        <SectionHeader title="Live ingest" />
        <HealthSummary />
      </section>

      <section className="space-y-4">
        <SectionHeader title="Backfill workers" />
        <BackfillSummary />
      </section>

      <CursorsTable />
    </Container>
  );
}
