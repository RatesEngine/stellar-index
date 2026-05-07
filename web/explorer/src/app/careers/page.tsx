import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Careers — roles open at Rates Engine',
  description:
    'Open roles at Rates Engine — backend, data infrastructure, frontend.',
};

export default function CareersPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-6 px-6 py-12">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Careers</h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Roles at Rates Engine.
        </p>
      </header>
      <div className="rounded-lg border border-slate-200 bg-slate-50 p-6 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-900/40 dark:text-slate-300">
        <p className="font-medium">No open roles listed yet.</p>
        <p className="mt-2">
          We&apos;re a small team focused on shipping the v1 platform.
          If you&apos;re interested in contributing — Apache-2.0
          codebase, real Stellar pricing infrastructure, no AI-slop
          shortcuts — drop a note via{' '}
          <Link href="/contact" className="text-brand-600 hover:underline">
            /contact
          </Link>
          .
        </p>
      </div>
    </div>
  );
}
