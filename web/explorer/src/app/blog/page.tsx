import type { Metadata } from 'next';
import Link from 'next/link';

import { loadBlogPosts } from '@/lib/blog';

export const metadata: Metadata = {
  title: 'Blog — engineering notes + product updates',
  description:
    'Rates Engine engineering blog — release notes, architecture decisions, post-mortems, product updates.',
};

export default function BlogIndexPage() {
  const posts = loadBlogPosts();

  return (
    <div className="mx-auto max-w-3xl space-y-8 px-6 py-12">
      <header className="space-y-2">
        <p className="font-mono text-xs uppercase tracking-widest text-brand-600 dark:text-brand-400">
          Blog
        </p>
        <h1 className="text-4xl font-semibold tracking-tight">
          Engineering notes
        </h1>
        <p className="text-base text-slate-600 dark:text-slate-400">
          Release notes, architecture decisions, and the why behind
          each surface. Sourced from{' '}
          <code className="font-mono text-sm">docs/blog/*.md</code> in
          the public repo — every post links back to its source on
          GitHub.
        </p>
      </header>

      {posts.length === 0 ? (
        <div className="rounded-xl border border-slate-200 bg-white p-6 dark:border-slate-800 dark:bg-slate-900">
          <p className="text-sm text-slate-600 dark:text-slate-400">
            No posts yet. The first post lands once v1 ships. For
            per-release notes meanwhile, see{' '}
            <Link href="/changelog" className="text-brand-600 hover:underline">
              /changelog
            </Link>{' '}
            (also available as an{' '}
            <Link href="/changelog.atom" className="text-brand-600 hover:underline">
              Atom feed
            </Link>
            ); for design rationale, see{' '}
            <Link href="/research" className="text-brand-600 hover:underline">
              /research
            </Link>
            .
          </p>
        </div>
      ) : (
        <ul className="space-y-4">
          {posts.map((p) => (
            <li
              key={p.slug}
              className="rounded-xl border border-slate-200 bg-white p-5 transition-colors hover:border-brand-500 dark:border-slate-800 dark:bg-slate-900"
            >
              <Link href={`/blog/${p.slug}`} className="group block space-y-2">
                <div className="flex items-baseline justify-between gap-2">
                  <h2 className="text-xl font-semibold tracking-tight group-hover:text-brand-600">
                    {p.title}
                  </h2>
                  <span className="font-mono text-xs text-slate-500">{p.date}</span>
                </div>
                {p.summary && (
                  <p className="text-sm text-slate-600 dark:text-slate-400">{p.summary}</p>
                )}
                <p className="font-mono text-[11px] text-slate-400">{p.author}</p>
              </Link>
            </li>
          ))}
        </ul>
      )}
    </div>
  );
}
