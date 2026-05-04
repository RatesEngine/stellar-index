import type { Metadata } from 'next';
import { Panel } from '@/components/reveal';

export const metadata: Metadata = {
  title: 'API docs',
  description:
    'Full reference for the Rates Engine v1 API — every endpoint, every parameter, auto-generated from the OpenAPI spec.',
};

export default function DocsPage() {
  return (
    <div className="mx-auto max-w-4xl space-y-6 p-6">
      <header className="space-y-2">
        <h1 className="text-2xl font-semibold tracking-tight">API docs</h1>
        <p className="text-sm text-slate-500">
          Full reference for the Rates Engine v1 API. Auto-generated
          from the OpenAPI spec.
        </p>
      </header>
      <Panel title="Reference">
        <p className="text-sm">
          The full reference lives at{' '}
          <a
            className="underline decoration-dotted"
            href="https://docs.ratesengine.net"
          >
            docs.ratesengine.net
          </a>
          {' '}(Redocly-rendered).
        </p>
        <p className="mt-3 text-xs text-slate-500">
          Once Phase 12.10 lands, this page embeds the full reference
          inline so the showcase site is a one-stop tour.
        </p>
      </Panel>
    </div>
  );
}
