// /blog.atom — RFC-4287 syndication feed of /blog posts. Same
// shape contract as /changelog.atom — feed readers (Feedly, Slack
// RSS bot, etc.) subscribe once and get every new post pushed.
//
// Static-export pre-rendered. docs/blog/*.md is read at build
// time, parsed once, and emitted to out/blog.atom; Cloudflare
// Pages serves the file under a stable URL.

import { NextResponse } from 'next/server';

import { loadBlogPosts, type BlogPost } from '@/lib/blog';

export const dynamic = 'force-static';

const SITE_URL = 'https://ratesengine.net';
const FEED_TITLE = 'Rates Engine — engineering notes';
const FEED_AUTHOR = 'Rates Engine';

export function GET() {
  const posts = loadBlogPosts();
  const updated = pickFeedUpdated(posts);
  const entries = posts.map(renderEntry).join('\n');

  const body = `<?xml version="1.0" encoding="UTF-8"?>
<feed xmlns="http://www.w3.org/2005/Atom">
  <id>${SITE_URL}/blog</id>
  <title>${esc(FEED_TITLE)}</title>
  <link rel="self" href="${SITE_URL}/blog.atom" type="application/atom+xml" />
  <link rel="alternate" href="${SITE_URL}/blog" type="text/html" />
  <updated>${updated}</updated>
  <author><name>${esc(FEED_AUTHOR)}</name></author>
${entries}
</feed>
`;

  return new NextResponse(body, {
    headers: {
      'content-type': 'application/atom+xml; charset=utf-8',
      'cache-control': 'public, max-age=3600',
    },
  });
}

function renderEntry(p: BlogPost): string {
  const id = `urn:ratesengine:blog:${p.slug}`;
  const url = `${SITE_URL}/blog/${p.slug}`;
  const published = atomDate(p.date);
  return `  <entry>
    <id>${id}</id>
    <title>${esc(p.title)}</title>
    <link rel="alternate" href="${url}" type="text/html" />
    <author><name>${esc(p.author)}</name></author>
    <published>${published}</published>
    <updated>${published}</updated>
    <summary type="text">${esc(p.summary)}</summary>
    <content type="text"><![CDATA[${p.body}]]></content>
  </entry>`;
}

function pickFeedUpdated(posts: BlogPost[]): string {
  for (const p of posts) {
    if (p.date) return atomDate(p.date);
  }
  return new Date().toISOString();
}

function atomDate(date?: string): string {
  if (!date) return new Date().toISOString();
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
