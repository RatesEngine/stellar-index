import {
  AccelerationArrow,
  DirectionPill,
  MultiWindowDelta,
  RankBadge,
  Sparkline,
  StreakIndicator,
} from '@/components/primitives';

/**
 * /dev/primitives — Storybook-style demo page for the design-system
 * primitives in `src/components/primitives/`. Not linked from the
 * public site; the URL is documented in the README for reviewers.
 *
 * Pure server-component render — every primitive takes static props
 * so this page is fully static-exportable.
 */
export default function PrimitivesPage() {
  return (
    <main className="mx-auto max-w-5xl space-y-12 p-8">
      <header>
        <h1 className="text-2xl font-semibold tracking-tight">
          Design-system primitives
        </h1>
        <p className="text-sm text-slate-500">
          Static demo of the components in{' '}
          <code className="font-mono text-xs">
            src/components/primitives/
          </code>
          . Spec lives in{' '}
          <a
            className="underline decoration-dotted"
            href="https://github.com/RatesEngine/rates-engine/blob/main/docs/architecture/showcase-site-data-inventory.md#6-cross-cutting-view-primitives"
          >
            data-inventory §6
          </a>
          .
        </p>
      </header>

      <Section title="DirectionPill">
        <Row>
          <DirectionPill deltaPct={0.42} />
          <DirectionPill deltaPct={3.21} />
          <DirectionPill deltaPct={12.5} />
          <DirectionPill deltaPct={42.0} />
          <DirectionPill deltaPct={-0.42} />
          <DirectionPill deltaPct={-3.21} />
          <DirectionPill deltaPct={-12.5} />
          <DirectionPill deltaPct={-42.0} />
          <DirectionPill deltaPct={null} />
        </Row>
        <p className="text-xs text-slate-500">Compact variant:</p>
        <Row>
          <DirectionPill deltaPct={3.2} compact />
          <DirectionPill deltaPct={-7.4} compact />
          <DirectionPill deltaPct={null} compact />
        </Row>
      </Section>

      <Section title="MultiWindowDelta">
        <MultiWindowDelta
          windows={[
            { label: '1h', deltaPct: 0.5 },
            { label: '24h', deltaPct: 3.2 },
            { label: '7d', deltaPct: -1.1 },
            { label: '30d', deltaPct: 18.4 },
          ]}
        />
        <MultiWindowDelta
          windows={[
            { label: '1h', deltaPct: -0.2 },
            { label: '24h', deltaPct: -8.7 },
            { label: '7d', deltaPct: -22.3 },
            { label: '30d', deltaPct: null },
          ]}
        />
      </Section>

      <Section title="Sparkline">
        <Row>
          <Sparkline values={[10, 11, 10.5, 12, 13, 12.5, 14]} />
          <Sparkline values={[14, 13, 12.5, 12, 10.5, 11, 10]} />
          <Sparkline
            values={[10, 12, 11, 13, 12, 14, 13, 15]}
            width={120}
            height={32}
          />
          <Sparkline values={[10, 10, 10, 10, 10]} tone="neutral" />
        </Row>
      </Section>

      <Section title="StreakIndicator">
        <Row>
          <StreakIndicator kind="streak" direction="up" days={14} />
          <StreakIndicator kind="streak" direction="up" days={1} />
          <StreakIndicator kind="streak" direction="down" days={3} />
          <StreakIndicator kind="ath" at={isoMinusHours(2)} />
          <StreakIndicator kind="atl" at={isoMinusDays(3)} />
          <StreakIndicator kind="new" since={isoMinusHours(6)} />
        </Row>
      </Section>

      <Section title="RankBadge">
        <Row>
          <RankBadge delta={2} />
          <RankBadge delta={1} />
          <RankBadge delta={0} />
          <RankBadge delta={-1} />
          <RankBadge delta={-3} />
          <RankBadge delta={0} isNew />
        </Row>
      </Section>

      <Section title="AccelerationArrow">
        <Row>
          <AccelerationArrow direction="up" acceleration="increasing" />
          <AccelerationArrow direction="up" acceleration="flat" />
          <AccelerationArrow direction="up" acceleration="decreasing" />
          <AccelerationArrow direction="down" acceleration="increasing" />
          <AccelerationArrow direction="down" acceleration="flat" />
          <AccelerationArrow direction="down" acceleration="decreasing" />
          <AccelerationArrow direction="flat" acceleration="flat" />
        </Row>
      </Section>

      <Section title="Composite — list-row sample">
        <div className="rounded-lg border border-slate-200 p-4 dark:border-slate-800">
          <div className="flex items-center gap-4">
            <span className="font-medium">XLM</span>
            <span className="font-mono tabular-nums">$0.1234</span>
            <Sparkline values={[0.115, 0.118, 0.12, 0.119, 0.122, 0.121, 0.1234]} />
            <MultiWindowDelta
              compact
              windows={[
                { label: '1h', deltaPct: 0.5 },
                { label: '24h', deltaPct: 3.2 },
                { label: '7d', deltaPct: -1.1 },
                { label: '30d', deltaPct: 18.4 },
              ]}
            />
            <StreakIndicator kind="streak" direction="up" days={14} />
            <RankBadge delta={2} />
            <AccelerationArrow direction="up" acceleration="increasing" />
          </div>
        </div>
      </Section>
    </main>
  );
}

function Section({
  title,
  children,
}: {
  title: string;
  children: React.ReactNode;
}) {
  return (
    <section className="space-y-3">
      <h2 className="text-sm font-medium uppercase tracking-wider text-slate-500">
        {title}
      </h2>
      <div className="space-y-3">{children}</div>
    </section>
  );
}

function Row({ children }: { children: React.ReactNode }) {
  return <div className="flex flex-wrap items-center gap-3">{children}</div>;
}

function isoMinusHours(h: number): string {
  return new Date(Date.now() - h * 60 * 60 * 1000).toISOString();
}

function isoMinusDays(d: number): string {
  return new Date(Date.now() - d * 24 * 60 * 60 * 1000).toISOString();
}
