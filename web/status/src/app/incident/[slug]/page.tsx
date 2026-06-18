import type { Metadata } from 'next';
import Link from 'next/link';
import { notFound } from 'next/navigation';
import { ArrowLeft, ExternalLink } from 'lucide-react';

import { loadIncident, loadIncidents } from '@/lib/incidents';
import { Markdown } from '@/lib/markdown';
import { Badge, Card, Container, type BadgeTone } from '@/components/ui';

// Each incident postmortem rendered as its own static page so
// every event has a permanent, shareable URL — the rest of the
// status site polls the live API but this surface is built once
// per release from the embedded markdown corpus.

// PUBLIC_REPO_URL is the base URL of the PUBLIC source repo (e.g.
// https://github.com/<org>/<repo>), set at build time once the
// repo is published at the v1.0 public flip. While unset the
// "View source on GitHub" link is omitted entirely — linking the
// private repo would 404 for every customer (WB-07). Trailing
// slash trimmed so `${PUBLIC_REPO_URL}/blob/main/...` is clean.
const PUBLIC_REPO_URL = (process.env.NEXT_PUBLIC_PUBLIC_REPO_URL ?? '').replace(
  /\/+$/,
  '',
);

export const dynamic = 'error';
export const dynamicParams = false;

export function generateStaticParams() {
  return loadIncidents().map((i) => ({ slug: i.slug }));
}

export async function generateMetadata({
  params,
}: {
  params: Promise<{ slug: string }>;
}): Promise<Metadata> {
  const { slug } = await params;
  const inc = loadIncident(slug);
  if (!inc) return { title: 'Incident not found' };
  return {
    title: `${inc.title} — Stellar Index status`,
    description: `Postmortem for ${inc.severity} on ${inc.date}.`,
    alternates: { canonical: `/incident/${slug}` },
  };
}

export default async function IncidentPage({
  params,
}: {
  params: Promise<{ slug: string }>;
}) {
  const { slug } = await params;
  const inc = loadIncident(slug);
  if (!inc) notFound();

  const sevTone: BadgeTone =
    inc.severity === 'SEV-1' ? 'bad' : inc.severity === 'SEV-2' ? 'warn' : 'ok';
  const statusTone: BadgeTone =
    inc.status === 'resolved'
      ? 'ok'
      : inc.status === 'monitoring'
        ? 'brand'
        : 'warn';

  return (
    <Container className="max-w-4xl space-y-6 py-10">
      <Link
        href="/"
        className="inline-flex items-center gap-1.5 text-sm text-ink-muted hover:text-brand-600"
      >
        <ArrowLeft className="h-3.5 w-3.5" />
        Back to status
      </Link>

      <header className="space-y-4 border-b border-line pb-6">
        <div className="flex flex-wrap items-center gap-2">
          <Badge tone={sevTone} dot>
            {inc.severity}
          </Badge>
          <Badge tone={statusTone}>{inc.status}</Badge>
          {inc.affected_components.map((c) => (
            <span
              key={c}
              className="inline-flex items-center rounded-full bg-surface-subtle px-2 py-0.5 font-mono text-[10px] text-ink-muted ring-1 ring-inset ring-line"
            >
              {c}
            </span>
          ))}
          <span className="ml-auto font-mono text-xs text-ink-faint">
            {inc.date}
          </span>
        </div>
        <h1 className="text-h2 font-semibold text-ink">{inc.title}</h1>
        <Timeline started_at={inc.started_at} resolved_at={inc.resolved_at} />
        {/* The repo is private until the v1.0 public flip, so a
                "View source" link would 404 for every customer. Gate it
                behind a build-time env flag that holds the PUBLIC repo's
                base URL (e.g. https://github.com/<org>/<repo>); when
                unset, drop the link entirely (WB-07). */}
        {PUBLIC_REPO_URL && (
          <a
            href={`${PUBLIC_REPO_URL}/blob/main/${inc.source_path}`}
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center gap-1 text-xs text-ink-faint hover:text-brand-600"
          >
            View source on GitHub
            <ExternalLink className="h-3 w-3" />
          </a>
        )}
      </header>

      <article>
        <Markdown source={stripDuplicateH1(inc.body)} />
      </article>
    </Container>
  );
}

function Timeline({
  started_at,
  resolved_at,
}: {
  started_at: string;
  resolved_at: string | null;
}) {
  if (!started_at) return null;
  const start = new Date(started_at);
  if (!Number.isFinite(start.getTime())) return null;
  const end = resolved_at ? new Date(resolved_at) : null;
  const duration = end ? Math.max(0, end.getTime() - start.getTime()) : null;
  return (
    <div className="grid grid-cols-1 gap-3 text-xs sm:grid-cols-3">
      <Cell label="Started" value={formatTs(started_at)} />
      <Cell
        label="Resolved"
        value={resolved_at ? formatTs(resolved_at) : '—'}
      />
      <Cell
        label="Duration"
        value={duration != null ? formatDuration(duration) : '—'}
      />
    </div>
  );
}

function Cell({ label, value }: { label: string; value: string }) {
  return (
    <Card flat className="px-3 py-2">
      <div className="text-[10px] font-semibold uppercase tracking-wider text-ink-faint">
        {label}
      </div>
      <div className="mt-0.5 font-mono text-xs text-ink">{value}</div>
    </Card>
  );
}

function formatTs(iso: string): string {
  const d = new Date(iso);
  if (!Number.isFinite(d.getTime())) return iso;
  return d
    .toISOString()
    .replace('T', ' ')
    .replace(/\.\d+Z$/, ' UTC');
}

function formatDuration(ms: number): string {
  const min = Math.round(ms / 60_000);
  if (min < 60) return `${min}m`;
  const h = Math.floor(min / 60);
  const m = min - h * 60;
  return m === 0 ? `${h}h` : `${h}h ${m}m`;
}

// stripDuplicateH1 — incident body usually starts with `# [SEV-X]
// <title>` which duplicates the page header. Drop it so the body
// starts at "## Identification".
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
