import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import {
  loadArchitectureDoc,
  loadArchitectureDocs,
} from '@/lib/architecture';
import { Markdown } from '@/lib/markdown';
import { SITE_OG_IMAGES, SITE_TWITTER_IMAGES } from '@/lib/seo';

// Each curated architecture doc rendered as a static page.
// Reuses the same loader/renderer pattern as ADRs and incident
// postmortems — the underlying markdown is the source of truth,
// the page just layers on Rates-Engine chrome.

export const dynamic = 'error';
export const dynamicParams = false;

export function generateStaticParams() {
  return loadArchitectureDocs().map((d) => ({ slug: d.slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  const doc = loadArchitectureDoc(slug);
  if (!doc) return { title: 'Architecture doc not found' };
  const canonical = `https://ratesengine.net/research/architecture/${slug}`;
  const title = `${doc.title} — Rates Engine architecture`;
  return {
    title,
    description: doc.description,
    alternates: { canonical },
    openGraph: { title, description: doc.description, url: canonical, type: 'article', images: SITE_OG_IMAGES },
    twitter: { card: 'summary_large_image', title, description: doc.description, images: SITE_TWITTER_IMAGES },
  };
}

export default async function ArchitectureDocPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const doc = loadArchitectureDoc(slug);
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
            Architecture
          </span>
          {doc.last_verified && (
            <span className="text-slate-500">
              Last verified {doc.last_verified}
            </span>
          )}
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
        <Markdown source={stripDuplicateH1(doc.body, doc.title)} />
      </article>
    </div>
  );
}

function stripDuplicateH1(body: string, _title: string): string {
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
