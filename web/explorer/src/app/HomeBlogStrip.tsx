import Link from 'next/link';
import { ArrowRight } from 'lucide-react';

import { loadBlogPosts } from '@/lib/blog';

/**
 * HomeBlogStrip — surfaces the three most recent blog posts on the
 * home page. Server-rendered at build time (the blog loader reads
 * the markdown files in docs/blog/). Renders nothing when there are
 * no posts yet.
 */
export function HomeBlogStrip() {
  const posts = loadBlogPosts().slice(0, 3);
  if (posts.length === 0) return null;

  return (
    <section className="space-y-3">
      <div className="flex items-baseline justify-between">
        <div className="space-y-1">
          <h2 className="text-2xl font-semibold tracking-tight">Latest from the blog</h2>
          <p className="text-sm text-slate-600 dark:text-slate-400">
            Engineering notes + product updates. Sourced from{' '}
            <code className="font-mono text-xs">docs/blog/*.md</code>{' '}
            in the public repo.
          </p>
        </div>
        <Link
          href="/blog"
          className="inline-flex items-center gap-1 text-xs text-brand-600 hover:underline"
        >
          All posts <ArrowRight className="h-3 w-3" />
        </Link>
      </div>
      <div className="grid grid-cols-1 gap-3 lg:grid-cols-3">
        {posts.map((p) => (
          <Link
            key={p.slug}
            href={`/blog/${p.slug}`}
            className="group flex flex-col rounded-xl border border-slate-200 bg-white p-4 transition-colors hover:border-brand-500 dark:border-slate-800 dark:bg-slate-900"
          >
            <span className="font-mono text-[10px] uppercase tracking-wider text-slate-500">
              {p.date}
            </span>
            <h3 className="mt-1 text-base font-semibold leading-snug tracking-tight group-hover:text-brand-600">
              {p.title}
            </h3>
            {p.summary && (
              <p className="mt-2 line-clamp-3 text-sm text-slate-600 dark:text-slate-400">
                {p.summary}
              </p>
            )}
            <span className="mt-auto pt-3 text-[11px] text-slate-500">{p.author}</span>
          </Link>
        ))}
      </div>
    </section>
  );
}
