import type { Metadata } from 'next';
import Link from 'next/link';
import { BookOpen } from 'lucide-react';

import { loadArchitectureDocs } from '@/lib/architecture';

export const metadata: Metadata = {
  title: 'Architecture narratives — Rates Engine research',
  description:
    'Long-form architecture narratives covering the Rates Engine ingest pipeline, aggregation methodology, and operational invariants.',
};

export default function ArchitectureIndexPage() {
  const docs = loadArchitectureDocs();
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">
          Architecture narratives
        </h1>
        <p className="max-w-3xl text-base text-slate-600 dark:text-slate-400">
          The long-form designs behind every Rates Engine subsystem.{' '}
          <Link href="/research" className="underline decoration-dotted">
            Back to research
          </Link>
          .
        </p>
      </header>
      <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
        {docs.map((d) => (
          <Link
            key={d.slug}
            href={`/research/architecture/${d.slug}`}
            className="group flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-4 transition hover:border-brand-300 hover:shadow-sm dark:border-slate-800 dark:bg-slate-900 dark:hover:border-brand-700"
          >
            <div className="flex items-center gap-2">
              <BookOpen className="h-3.5 w-3.5 text-slate-400 group-hover:text-brand-500" />
              <span className="text-sm font-semibold tracking-tight">
                {d.title}
              </span>
            </div>
            <p className="text-xs text-slate-600 dark:text-slate-400">
              {d.description}
            </p>
            <span className="text-[10px] uppercase tracking-wider text-slate-400">
              Verified {d.last_verified}
            </span>
          </Link>
        ))}
      </div>
    </div>
  );
}
