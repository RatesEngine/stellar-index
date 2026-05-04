import { Code2 } from 'lucide-react';

import { Panel } from '@/components/reveal';
import type { RequestExample } from '@/api/client';

export type PlaceholderPageProps = {
  title: string;
  blurb: string;
  /** API endpoint that will back this view once it ships. */
  source: RequestExample;
  /** Phase reference per the showcase implementation plan. */
  phase: string;
  /** Comma-separated list of features the page will expose. */
  features?: string[];
};

/**
 * PlaceholderPage — renders a top-level section page that exists in
 * the IA but hasn't been built yet. Shows the planned API call,
 * the implementation-plan reference, and a feature list so the
 * navbar / footer don't 404 during the staged rollout.
 *
 * Replace usages with real content as each section ships.
 */
export function PlaceholderPage({
  title,
  blurb,
  source,
  phase,
  features,
}: PlaceholderPageProps) {
  return (
    <div className="mx-auto max-w-4xl space-y-6 p-6">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">{title}</h1>
        <p className="text-sm text-slate-500">{blurb}</p>
      </header>
      <Panel
        title="Coming up"
        hint={phase}
        source={source}
        bodyClassName="space-y-3"
      >
        <p className="text-sm text-slate-600 dark:text-slate-400">
          This page is part of the staged showcase rollout. The
          underlying API call shown via the <Code2 className="inline h-3 w-3" />
          {' '}reveal is what will back it. Spec lives in{' '}
          <a
            className="underline decoration-dotted"
            href="https://github.com/RatesEngine/rates-engine/blob/main/docs/architecture/showcase-site-data-inventory.md"
          >
            data-inventory
          </a>
          .
        </p>
        {features && features.length > 0 && (
          <ul className="list-disc space-y-1 pl-5 text-sm">
            {features.map((f) => (
              <li key={f}>{f}</li>
            ))}
          </ul>
        )}
      </Panel>
    </div>
  );
}
