import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ArrowLeft, Github } from 'lucide-react';

import { loadBlogPost, loadBlogPosts } from '@/lib/blog';
import { Markdown } from '@/lib/markdown';

type Params = Promise<{ slug: string }>;

export function generateStaticParams() {
  return loadBlogPosts().map((p) => ({ slug: p.slug }));
}

export async function generateMetadata({ params }: { params: Params }): Promise<Metadata> {
  const { slug } = await params;
  const post = loadBlogPost(slug);
  if (!post) return { title: 'Post not found — Blog' };
  return {
    title: `${post.title} — Blog`,
    description: post.summary,
  };
}

export default async function BlogPostPage({ params }: { params: Params }) {
  const { slug } = await params;
  const post = loadBlogPost(slug);
  if (!post) notFound();

  return (
    <div className="mx-auto max-w-3xl space-y-6 px-6 py-12">
      <Link
        href="/blog"
        className="inline-flex items-center gap-1.5 text-sm text-slate-600 hover:text-brand-600 dark:text-slate-400"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        All posts
      </Link>

      <header className="space-y-2 border-b border-slate-200 pb-4 dark:border-slate-800">
        <h1 className="text-3xl font-semibold tracking-tight">{post.title}</h1>
        <p className="text-sm text-slate-500">
          {post.date} · {post.author}
        </p>
        {post.summary && (
          <p className="text-base text-slate-600 dark:text-slate-400">{post.summary}</p>
        )}
      </header>

      <article className="prose prose-slate max-w-none dark:prose-invert">
        <Markdown source={post.body} />
      </article>

      <footer className="border-t border-slate-200 pt-4 text-xs dark:border-slate-800">
        <a
          href={`https://github.com/RatesEngine/rates-engine/blob/main/${post.source_path}`}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex items-center gap-1 text-slate-500 hover:text-brand-600"
        >
          <Github className="h-3.5 w-3.5" />
          Source: {post.source_path}
        </a>
      </footer>
    </div>
  );
}
