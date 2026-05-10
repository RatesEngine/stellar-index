// Build-time blog post loader. Reads docs/blog/*.md from the repo
// root, parses YAML frontmatter, and exposes typed records for the
// /blog surface. Server-only; the static-export build inlines the
// results into pre-rendered HTML.
//
// Convention: each post lives at docs/blog/<YYYY-MM-DD>-<slug>.md
// with frontmatter:
//
//   ---
//   title: My post title
//   date: 2026-05-07
//   author: Operator
//   summary: One-line description for the index card.
//   ---
//
//   <body markdown>

import { readFileSync, existsSync, readdirSync } from 'node:fs';
import path from 'node:path';

export type BlogPost = {
  slug: string;       // "2026-05-07-magic-link-launch"
  date: string;       // "2026-05-07"
  title: string;
  author: string;
  summary: string;
  body: string;
  source_path: string;
};

const REPO_ROOT = path.resolve(process.cwd(), '..', '..');
const BLOG_DIR = path.join(REPO_ROOT, 'docs', 'blog');

let cache: BlogPost[] | null = null;

export function loadBlogPosts(): BlogPost[] {
  if (cache) return cache;
  if (!existsSync(BLOG_DIR)) {
    cache = [];
    return cache;
  }
  const files = readdirSync(BLOG_DIR)
    .filter((f) => /^\d{4}-\d{2}-\d{2}-.+\.md$/.test(f))
    .sort()
    .reverse(); // newest first
  const out: BlogPost[] = [];
  for (const f of files) {
    const full = path.join(BLOG_DIR, f);
    const raw = readFileSync(full, 'utf-8');
    const parsed = parseFrontmatter(raw);
    if (!parsed) continue;
    const slug = f.replace(/\.md$/, '');
    const date = slug.slice(0, 10);
    out.push({
      slug,
      date,
      title: String(parsed.fm['title'] ?? slug),
      author: String(parsed.fm['author'] ?? 'Operator'),
      summary: String(parsed.fm['summary'] ?? ''),
      body: parsed.body.trim(),
      source_path: `docs/blog/${f}`,
    });
  }
  cache = out;
  return out;
}

export function loadBlogPost(slug: string): BlogPost | null {
  return loadBlogPosts().find((p) => p.slug === slug) ?? null;
}

function parseFrontmatter(
  raw: string,
): { fm: Record<string, unknown>; body: string } | null {
  if (!raw.startsWith('---')) return { fm: {}, body: raw };
  const end = raw.indexOf('\n---', 3);
  if (end === -1) return null;
  const head = raw.slice(3, end).trim();
  const body = raw.slice(end + 4).replace(/^\n/, '');
  const fm: Record<string, unknown> = {};
  for (const line of head.split('\n')) {
    const m = line.match(/^([A-Za-z_][A-Za-z0-9_]*):\s*(.*)$/);
    if (!m) continue;
    const k = m[1]!;
    const v = m[2]!.trim();
    if (v === '' || v === 'null') {
      fm[k] = null;
    } else {
      fm[k] = v.replace(/^['"]|['"]$/g, '');
    }
  }
  return { fm, body };
}
