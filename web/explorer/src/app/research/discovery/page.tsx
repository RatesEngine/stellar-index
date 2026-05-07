import type { Metadata } from 'next';
import Link from 'next/link';
import { ShieldCheck } from 'lucide-react';

import { loadDiscoveryDocs } from '@/lib/discovery';

export const metadata: Metadata = {
  title: 'Discovery audits — Rates Engine research',
  description:
    'Per-DEX and per-oracle integration audits documenting how each on-chain venue\'s event schema was verified against the upstream Rust source.',
};

export default function DiscoveryIndexPage() {
  const docs = loadDiscoveryDocs();
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">
          Discovery audits
        </h1>
        <p className="max-w-3xl text-base text-slate-600 dark:text-slate-400">
          Per-DEX / per-oracle integration audits.{' '}
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
            href={`/research/discovery/${d.slug}`}
            className="group flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-4 transition hover:border-brand-300 hover:shadow-sm dark:border-slate-800 dark:bg-slate-900 dark:hover:border-brand-700"
          >
            <div className="flex items-center gap-2">
              <ShieldCheck className="h-3.5 w-3.5 text-slate-400 group-hover:text-brand-500" />
              <span className="text-sm font-semibold tracking-tight">
                {d.title}
              </span>
              <span className="ml-auto rounded bg-slate-100 px-1.5 py-0.5 text-[10px] uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-400">
                {d.category}
              </span>
            </div>
            <p className="text-xs text-slate-600 dark:text-slate-400">
              {d.description}
            </p>
          </Link>
        ))}
      </div>
    </div>
  );
}
