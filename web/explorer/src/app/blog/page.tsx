import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Blog — engineering notes + product updates',
  description:
    'Rates Engine engineering blog — design notes, post-mortems, product updates.',
};

export default function BlogPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-6 px-6 py-12">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">Blog</h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          Engineering notes, design decisions, post-mortems, product updates.
        </p>
      </header>
      <div className="rounded-lg border border-slate-200 bg-slate-50 p-6 text-sm text-slate-700 dark:border-slate-800 dark:bg-slate-900/40 dark:text-slate-300">
        <p className="font-medium">First post is in the works.</p>
        <p className="mt-2">
          For now, the closest equivalent is{' '}
          <Link href="/research" className="text-brand-600 hover:underline">
            /research
          </Link>{' '}
          — the architecture decision records, discovery audits, and
          operational runbooks that document how the platform is built.
          The{' '}
          <Link href="/changelog" className="text-brand-600 hover:underline">
            /changelog
          </Link>{' '}
          is the per-release ledger.
        </p>
      </div>
    </div>
  );
}
