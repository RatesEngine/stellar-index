import type { Metadata } from 'next';
import Link from 'next/link';
import { BookOpen, FileText, ShieldCheck, Wrench } from 'lucide-react';

import { loadADRs } from '@/lib/adr';
import { loadArchitectureDocs } from '@/lib/architecture';
import { loadDiscoveryDocs } from '@/lib/discovery';
import { loadOperationsDocs } from '@/lib/operations';
import { StatusBadge } from './StatusBadge';

export const metadata: Metadata = {
  title: 'Research — architecture decisions and methodology',
  description:
    'Every architectural decision behind Rates Engine, with rationale, alternatives considered, and consequences. Browse the ADR archive, integration audits, and operations runbooks.',
};

// TOPICS held the GitHub-only catch-all topic cards. As each
// topic earns a curated on-site browser, its card is removed.
// Today there is none — operations got curated in #868-ish; the
// per-alert runbooks remain GitHub-only.
const TOPICS: { name: string; description: string; href?: string }[] = [];

export default function ResearchPage() {
  const adrs = loadADRs();
  const archDocs = loadArchitectureDocs();
  const discoveryDocs = loadDiscoveryDocs();
  const opsDocs = loadOperationsDocs();

  // Sort newest first within each status group; status order
  // surfaces Accepted ADRs above Proposed/Superseded so visitors
  // see the load-bearing decisions immediately.
  const grouped = {
    Accepted: adrs.filter((a) => a.status === 'Accepted'),
    Proposed: adrs.filter((a) => a.status === 'Proposed'),
    Superseded: adrs.filter((a) => a.status === 'Superseded'),
    Rejected: adrs.filter((a) => a.status === 'Rejected'),
  };
  for (const k of Object.keys(grouped) as (keyof typeof grouped)[]) {
    grouped[k].sort((a, b) => Number(b.id) - Number(a.id));
  }

  return (
    <div className="mx-auto max-w-7xl space-y-10 px-6 py-8">
      <header className="space-y-3">
        <h1 className="text-3xl font-semibold tracking-tight">Research</h1>
        <p className="max-w-3xl text-base text-slate-600 dark:text-slate-400">
          The thinking behind every Rates Engine choice. Architecture
          decision records (ADRs) below capture every load-bearing
          design call with its alternatives + consequences. The
          discovery audits, operations runbooks, and architecture
          narratives live alongside the source on GitHub.
        </p>
      </header>

      <section className="space-y-4">
        <div className="flex items-baseline justify-between">
          <h2 className="text-xl font-semibold tracking-tight">
            Architecture narratives
          </h2>
          <span className="text-xs text-slate-500">
            {archDocs.length} docs ·{' '}
            <a
              href="https://github.com/RatesEngine/rates-engine/tree/main/docs/architecture"
              target="_blank"
              rel="noreferrer noopener"
              className="hover:text-brand-600"
            >
              source on GitHub
            </a>
          </span>
        </div>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {archDocs.map((d) => (
            <Link
              key={d.slug}
              href={`/research/architecture/${d.slug}`}
              className="group flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-4 transition hover:border-brand-300 hover:shadow-sm dark:border-slate-800 dark:bg-slate-900 dark:hover:border-brand-700"
            >
              <div className="flex items-center gap-2">
                <BookOpen className="h-3.5 w-3.5 text-slate-400 group-hover:text-brand-500" />
                <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
                  Architecture
                </span>
                {d.last_verified && (
                  <span className="ml-auto text-[10px] text-slate-400">
                    Verified {d.last_verified}
                  </span>
                )}
              </div>
              <h4 className="text-sm font-semibold leading-snug text-slate-900 group-hover:text-brand-600 dark:text-slate-100">
                {d.title}
              </h4>
              <p className="text-xs text-slate-600 dark:text-slate-400">
                {d.description}
              </p>
            </Link>
          ))}
        </div>
      </section>

      <section className="space-y-4">
        <div className="flex items-baseline justify-between">
          <h2 className="text-xl font-semibold tracking-tight">
            Integration audits
          </h2>
          <span className="text-xs text-slate-500">
            {discoveryDocs.length} audits ·{' '}
            <a
              href="https://github.com/RatesEngine/rates-engine/tree/main/docs/discovery"
              target="_blank"
              rel="noreferrer noopener"
              className="hover:text-brand-600"
            >
              source on GitHub
            </a>
          </span>
        </div>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          For every on-chain venue we ingest, we verified the event
          schema and decoder against the upstream Rust source. Each
          audit names the contract repo and commit checked, the
          quirks we found, and how the decoder handles them.
        </p>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {discoveryDocs.map((d) => (
            <Link
              key={d.slug}
              href={`/research/discovery/${d.slug}`}
              className="group flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-4 transition hover:border-brand-300 hover:shadow-sm dark:border-slate-800 dark:bg-slate-900 dark:hover:border-brand-700"
            >
              <div className="flex items-center gap-2">
                <ShieldCheck className="h-3.5 w-3.5 text-slate-400 group-hover:text-brand-500" />
                <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
                  {d.category}
                </span>
              </div>
              <h4 className="text-sm font-semibold leading-snug text-slate-900 group-hover:text-brand-600 dark:text-slate-100">
                {d.title}
              </h4>
              <p className="text-xs text-slate-600 dark:text-slate-400">
                {d.description}
              </p>
            </Link>
          ))}
        </div>
      </section>

      <section className="space-y-4">
        <div className="flex items-baseline justify-between">
          <h2 className="text-xl font-semibold tracking-tight">
            Operations runbooks
          </h2>
          <span className="text-xs text-slate-500">
            {opsDocs.length} guides ·{' '}
            <a
              href="https://github.com/RatesEngine/rates-engine/tree/main/docs/operations"
              target="_blank"
              rel="noreferrer noopener"
              className="hover:text-brand-600"
            >
              source on GitHub
            </a>
          </span>
        </div>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          The recipes any new operator (or auditor) would want to
          read before standing up their own copy. Per-alert on-call
          runbooks stay private; these four are the cross-cutting
          procedures.
        </p>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {opsDocs.map((d) => (
            <Link
              key={d.slug}
              href={`/research/operations/${d.slug}`}
              className="group flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-4 transition hover:border-brand-300 hover:shadow-sm dark:border-slate-800 dark:bg-slate-900 dark:hover:border-brand-700"
            >
              <div className="flex items-center gap-2">
                <Wrench className="h-3.5 w-3.5 text-slate-400 group-hover:text-brand-500" />
                <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
                  Operations
                </span>
                {d.last_verified && (
                  <span className="ml-auto text-[10px] text-slate-400">
                    Verified {d.last_verified}
                  </span>
                )}
              </div>
              <h4 className="text-sm font-semibold leading-snug text-slate-900 group-hover:text-brand-600 dark:text-slate-100">
                {d.title}
              </h4>
              <p className="text-xs text-slate-600 dark:text-slate-400">
                {d.description}
              </p>
            </Link>
          ))}
        </div>
      </section>

      <section className="space-y-4">
        <div className="flex items-baseline justify-between">
          <h2 className="text-xl font-semibold tracking-tight">
            Architecture decision records
          </h2>
          <span className="text-xs text-slate-500">
            {adrs.length} records ·{' '}
            <a
              href="https://github.com/RatesEngine/rates-engine/tree/main/docs/adr"
              target="_blank"
              rel="noreferrer noopener"
              className="hover:text-brand-600"
            >
              source on GitHub
            </a>
          </span>
        </div>

        {(['Accepted', 'Proposed', 'Superseded', 'Rejected'] as const).map(
          (status) =>
            grouped[status].length === 0 ? null : (
              <div key={status} className="space-y-2">
                {status !== 'Accepted' && (
                  <h3 className="text-xs font-semibold uppercase tracking-wider text-slate-500">
                    {status}
                  </h3>
                )}
                <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
                  {grouped[status].map((adr) => (
                    <Link
                      key={adr.id}
                      href={`/research/adr/${adr.id}`}
                      className="group flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-4 transition hover:border-brand-300 hover:shadow-sm dark:border-slate-800 dark:bg-slate-900 dark:hover:border-brand-700"
                    >
                      <div className="flex items-center gap-2">
                        <FileText className="h-3.5 w-3.5 text-slate-400 group-hover:text-brand-500" />
                        <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
                          ADR-{adr.id}
                        </span>
                        <StatusBadge status={adr.status} />
                        <span className="ml-auto text-[10px] text-slate-400">
                          {adr.date}
                        </span>
                      </div>
                      <h4 className="text-sm font-semibold leading-snug text-slate-900 group-hover:text-brand-600 dark:text-slate-100">
                        {adr.title}
                      </h4>
                    </Link>
                  ))}
                </div>
              </div>
            ),
        )}
      </section>

      {TOPICS.length > 0 && (
        <section className="space-y-3">
          <h2 className="text-xl font-semibold tracking-tight">Browse by topic</h2>
          <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
            {TOPICS.map((t) =>
              t.href ? (
                <a
                  key={t.name}
                  href={t.href}
                  target="_blank"
                  rel="noreferrer noopener"
                  className="flex flex-col gap-1 rounded-xl border border-slate-200 bg-white p-4 transition hover:border-brand-300 dark:border-slate-800 dark:bg-slate-900 dark:hover:border-brand-700"
                >
                  <h3 className="text-sm font-semibold">{t.name}</h3>
                  <p className="text-xs text-slate-600 dark:text-slate-400">
                    {t.description}
                  </p>
                </a>
              ) : (
                <div
                  key={t.name}
                  className="flex flex-col gap-1 rounded-xl border border-slate-200 bg-white p-4 dark:border-slate-800 dark:bg-slate-900"
                >
                  <h3 className="text-sm font-semibold">{t.name}</h3>
                  <p className="text-xs text-slate-600 dark:text-slate-400">
                    {t.description}
                  </p>
                </div>
              ),
            )}
          </div>
        </section>
      )}

      <section className="rounded-xl border border-slate-200 bg-white p-5 text-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold">Why we publish all of this</h2>
        <p className="mt-2 text-slate-600 dark:text-slate-400">
          Stellar already has Horizon. The reason a second pricing
          stack adds value is methodology — what gets included in the
          VWAP, how we handle cross-pair triangulation, what triggers a
          freeze, how we audit a Soroban contract before flipping
          BackfillSafe. None of that is useful behind a closed door.
          Every choice has an ADR with a &quot;Why this not the
          alternative&quot; section; every audit is captured in
          discovery notes; every alert has a runbook.
        </p>
      </section>
    </div>
  );
}
