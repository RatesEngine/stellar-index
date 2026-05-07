import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import { loadADR, loadADRs } from '@/lib/adr';
import { Markdown } from '@/lib/markdown';
import { StatusBadge } from '../../StatusBadge';

// Each ADR rendered as its own page so every architectural
// decision has a shareable URL with proper SEO. The body is the
// canonical doc/adr/*.md, parsed at build time and served as
// pre-rendered HTML — no client-side fetch needed.

export const dynamic = 'error'; // static export only
export const dynamicParams = false;

export function generateStaticParams() {
  return loadADRs().map((a) => ({ id: a.id }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ id: string }>;
}): Promise<Metadata> {
  const { id } = await params;
  const adr = loadADR(id);
  if (!adr) return { title: 'ADR not found' };
  return {
    title: `ADR-${adr.id}: ${adr.title} — Rates Engine research`,
    description: `Architecture decision record ${adr.id} (${adr.status}, ${adr.date}).`,
  };
}

export default async function ADRPage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  const adr = loadADR(id);
  if (!adr) notFound();

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
        <div className="flex items-center gap-3">
          <span className="text-xs font-medium uppercase tracking-wider text-slate-500">
            ADR-{adr.id}
          </span>
          <StatusBadge status={adr.status} />
          <span className="text-xs text-slate-500">{adr.date}</span>
        </div>
        <h1 className="text-2xl font-semibold tracking-tight">{adr.title}</h1>
        <a
          href={`https://github.com/RatesEngine/rates-engine/blob/main/${adr.source_path}`}
          target="_blank"
          rel="noreferrer noopener"
          className="inline-flex items-center gap-1 text-xs text-slate-500 hover:text-brand-600"
        >
          View source on GitHub
          <ExternalLink className="h-3 w-3" />
        </a>
      </header>

      <article>
        <Markdown source={stripDuplicateH1(adr.body, adr.title)} />
      </article>

      <RelatedADRs adr={adr} />
    </div>
  );
}

// stripDuplicateH1 — the ADR body's first non-empty content is
// usually a `# ADR-NNNN: <title>` heading that duplicates what we
// already render in the page header. Drop it so the body starts
// at "## Context".
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

function RelatedADRs({ adr }: { adr: ReturnType<typeof loadADR> }) {
  if (!adr) return null;
  const all = loadADRs();
  const related = all.filter(
    (a) =>
      adr.supersedes.includes(a.id) ||
      adr.superseded_by === a.id ||
      a.supersedes.includes(adr.id) ||
      a.superseded_by === adr.id,
  );
  if (related.length === 0) return null;
  return (
    <section className="border-t border-slate-200 pt-6 dark:border-slate-800">
      <h2 className="mb-3 text-sm font-semibold uppercase tracking-wider text-slate-500">
        Related
      </h2>
      <ul className="space-y-2 text-sm">
        {related.map((r) => (
          <li key={r.id}>
            <Link
              href={`/research/adr/${r.id}`}
              className="text-brand-600 hover:underline"
            >
              ADR-{r.id}: {r.title}
            </Link>
          </li>
        ))}
      </ul>
    </section>
  );
}
