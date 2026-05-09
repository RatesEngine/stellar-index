// /changelog.atom — RFC-4287 syndication feed of Rates Engine
// product releases. Same shape contract as /v1/incidents.atom on
// the API side: subscribers in Feedly, Slack RSS bot, etc. get
// pushed every new release with no polling.
//
// Static-export pre-rendered. The CHANGELOG.md content is read
// at build time, parsed once, and emitted to out/changelog.atom
// — Cloudflare Pages serves the file with a stable URL.

import { NextResponse } from 'next/server';

import { loadReleases, versionSlug, type Release } from '@/lib/changelog';

// Required for output: 'export'.
export const dynamic = 'force-static';

const SITE_URL = 'https://ratesengine.net';
const FEED_TITLE = 'Rates Engine — release notes';
const FEED_AUTHOR = 'Rates Engine';

export function GET() {
  // Drop the Unreleased section from the syndication feed —
  // every explorer redeploy would otherwise surface it as a "new
  // release" entry to Feedly / Slack RSS subscribers. The
  // rendered /changelog page intentionally keeps Unreleased
  // visible (visitors want a forward look); the atom feed has the
  // opposite contract — only dated, immutable releases belong
  // here.
  const releases = loadReleases().filter(
    (r) => r.version.toLowerCase() !== 'unreleased',
  );
  const updated = pickFeedUpdated(releases);
  const entries = releases.map(renderEntry).join('\n');

  const body = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>${SITE_URL}/changelog</id>
  <title>${esc(FEED_TITLE)}</title>
  <link rel="self" href="${SITE_URL}/changelog.atom" type="application/atom+xml" />
  <link rel="alternate" href="${SITE_URL}/changelog" type="text/html" />
  <updated>${updated}</updated>
  <author><name>${esc(FEED_AUTHOR)}</name></author>
${entries}
</feed>
`;

  return new NextResponse(body, {
    headers: {
      'content-type': 'application/atom+xml; charset=utf-8',
      // Encourage feed readers + CDNs to cache for an hour. The
      // feed updates only on redeploy, so an hour is conservative.
      'cache-control': 'public, max-age=3600',
    },
  });
}

function renderEntry(r: Release): string {
  const id = `urn:ratesengine:release:${versionSlug(r.version)}`;
  const title = `Rates Engine ${r.version}`;
  const url = `${SITE_URL}/changelog#${versionSlug(r.version)}`;
  const published = atomDate(r.date);
  // Body is the original markdown wrapped in CDATA so feed
  // readers that render plain text (Slack, terminal RSS) still
  // see the structure, and ones that render markdown (Feedly's
  // Pro renderer) get the headings and bullets intact.
  const summaryLines: string[] = [];
  for (const block of r.blocks) {
    summaryLines.push(`### ${block.kind}`);
    summaryLines.push(...block.lines);
    summaryLines.push('');
  }
  const summary = summaryLines.join('\n').trim();

  return `  <entry>
    <id>${id}</id>
    <title>${esc(title)}</title>
    <link rel="alternate" href="${url}" type="text/html" />
    <published>${published}</published>
    <updated>${published}</updated>
    <content type="text"><![CDATA[${summary}]]></content>
  </entry>`;
}

function pickFeedUpdated(releases: Release[]): string {
  for (const r of releases) {
    if (r.date) return atomDate(r.date);
  }
  return new Date().toISOString();
}

function atomDate(date?: string): string {
  if (!date) return new Date().toISOString();
  // CHANGELOG dates are YYYY-MM-DD; normalise to ISO-8601 UTC at
  // start-of-day so feed readers get a stable sort key.
  const d = new Date(`${date}T00:00:00Z`);
  if (Number.isNaN(d.getTime())) return new Date().toISOString();
  return d.toISOString();
}

function esc(s: string): string {
  return s
    .replace(/&/g, '&amp;')
    .replace(/</g, '&lt;')
    .replace(/>/g, '&gt;')
    .replace(/"/g, '&quot;');
}
