import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'Not found',
  description: 'The page you tried to visit doesn’t exist on Rates Engine.',
};

/**
 * Custom 404 page. Static-export compatible — no client components,
 * no data fetching. Lists a few likely destinations so visitors who
 * mistype a URL or follow a stale link can recover quickly.
 */
export default function NotFound() {
  return (
    <div className="mx-auto max-w-xl space-y-6 p-12 text-center">
      <div className="space-y-2">
        <p className="font-mono text-xs uppercase tracking-widest text-slate-500">
          404
        </p>
        <h1 className="text-3xl font-semibold tracking-tight">
          Couldn&apos;t find that page.
        </h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          The URL doesn&apos;t map to anything on Rates Engine. If you
          followed a link, the destination may have been renamed or
          removed.
        </p>
      </div>

      <ul className="space-y-1 text-left text-sm">
        <li>
          <Link href="/" className="hover:text-brand-600">
            ← Home
          </Link>
        </li>
        <li>
          <Link href="/assets" className="hover:text-brand-600">
            Browse all coins
          </Link>
        </li>
        <li>
          <Link href="/markets" className="hover:text-brand-600">
            Browse markets
          </Link>
        </li>
        <li>
          <Link href="/sources" className="hover:text-brand-600">
            Source registry
          </Link>
        </li>
        <li>
          <Link href="/diagnostics" className="hover:text-brand-600">
            System diagnostics
          </Link>
        </li>
        <li>
          <Link href="/docs" className="hover:text-brand-600">
            API docs
          </Link>
        </li>
      </ul>
    </div>
  );
}
