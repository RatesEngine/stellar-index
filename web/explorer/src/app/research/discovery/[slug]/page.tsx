import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import { loadDiscoveryDoc, loadDiscoveryDocs } from '@/lib/discovery';
import { Markdown } from '@/lib/markdown';
import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES } from '@/lib/seo';

// Each curated discovery audit rendered as a static page. Same
// shape as the ADR + architecture browsers; the body is the
// canonical doc/discovery/<sub>/<slug>.md, parsed at build time.

export const dynamic = 'error';
export const dynamicParams = false;

export function generateStaticParams() {
  return loadDiscoveryDocs().map((d) => ({ slug: d.slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  const doc = loadDiscoveryDoc(slug);
  if (!doc) return { title: 'Discovery doc not found' };
  const canonical = `https://ratesengine.net/research/discovery/${slug}`;
  const title = `${doc.title} — Rates Engine integration audit`;
  return {
    title,
    description: doc.description,
    alternates: { canonical },
    openGraph: { title, description: doc.description, url: canonical, type: 'article', images: SITE_OG_IMAGES },
    twitter: { card: 'summary_large_image', title, description: doc.description, images: SITE_TWITTER_IMAGES },
  };
}

export default async function DiscoveryDocPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const doc = loadDiscoveryDoc(slug);
  if (!doc) notFound();

  return (
    <div className="mx-auto max-w-4xl space-y-6 px-6 py-8">
      <Link
        href="/research"
        className="inline-flex items-center gap-1.5 text-sm text-slate-600 hover:text-brand-600 dark:text-slate-400"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to research
      </Link>

      <header className="space-y-3 border-b border-slate-200 pb-6 dark:border-slate-800">
        <div className="flex items-center gap-3 text-xs">
          <span className="font-medium uppercase tracking-wider text-slate-500">
            Integration audit
          </span>
          <span className="rounded-full bg-slate-100 px-2 py-0.5 text-[10px] font-medium uppercase tracking-wider text-slate-600 dark:bg-slate-800 dark:text-slate-300">
            {doc.category}
          </span>
        </div>
        <h1 className="text-2xl font-semibold tracking-tight">{doc.title}</h1>
        <p className="text-sm text-slate-600 dark:text-slate-400">
          {doc.description}
        </p>
        <a
          href={`https://github.com/RatesEngine/rates-engine/blob/main/${doc.source_path}`}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex items-center gap-1 text-xs text-slate-500 hover:text-brand-600"
        >
          View source on GitHub
          <ExternalLink className="h-3 w-3" />
        </a>
      </header>

      <article>
        <Markdown source={stripDuplicateH1(doc.body)} />
      </article>
    </div>
  );
}

function stripDuplicateH1(body: string): string {
  const lines = body.split('\n');
  let i = 0;
  while (i < lines.length && lines[i]!.trim() === '') i++;
  if (i < lines.length && lines[i]!.startsWith('# ')) {
    i++;
    while (i < lines.length && lines[i]!.trim() === '') i++;
    return lines.slice(i).join('\n');
  }
  return body;
}
