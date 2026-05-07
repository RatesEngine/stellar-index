// Build-time ADR loader. Reads docs/adr/*.md from the repo root,
// parses YAML frontmatter, and exposes typed records for the
// /research surface.
//
// Used only by server components — `fs` access is not in the
// browser bundle. Next.js 15's static export evaluates these reads
// at build time and inlines the results into pre-rendered HTML.

import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';

export type ADRStatus = 'Proposed' | 'Accepted' | 'Superseded' | 'Rejected';

export type ADR = {
  id: string;             // "0003"
  slug: string;           // "0003-i128-no-truncation"
  title: string;          // from frontmatter `title`
  status: ADRStatus;
  date: string;           // YYYY-MM-DD
  supersedes: string[];
  superseded_by: string | null;
  body: string;           // markdown body, frontmatter stripped
  source_path: string;    // "docs/adr/0003-i128-no-truncation.md" (relative to repo root, for the GitHub link)
};

const REPO_ROOT = path.resolve(process.cwd(), '..', '..');
const ADR_DIR = path.join(REPO_ROOT, 'docs', 'adr');

let cache: ADR[] | null = null;

export function loadADRs(): ADR[] {
  if (cache) return cache;
  const files = readdirSync(ADR_DIR)
    .filter((f) => /^\d{4}-.+\.md$/.test(f))
    .sort();
  const out: ADR[] = [];
  for (const f of files) {
    const full = path.join(ADR_DIR, f);
    const raw = readFileSync(full, 'utf-8');
    const parsed = parseFrontmatter(raw);
    if (!parsed) continue;
    const slug = f.replace(/\.md$/, '');
    const id = slug.split('-')[0]!;
    out.push({
      id,
      slug,
      title: String(parsed.fm['title'] ?? slug),
      status: (String(parsed.fm['status'] ?? 'Proposed') as ADRStatus),
      date: String(parsed.fm['date'] ?? ''),
      supersedes: Array.isArray(parsed.fm['supersedes'])
        ? (parsed.fm['supersedes'] as string[])
        : [],
      superseded_by:
        parsed.fm['superseded_by'] && parsed.fm['superseded_by'] !== 'null'
          ? String(parsed.fm['superseded_by'])
          : null,
      body: parsed.body.trim(),
      source_path: `docs/adr/${f}`,
    });
  }
  cache = out;
  return out;
}

export function loadADR(id: string): ADR | null {
  return loadADRs().find((a) => a.id === id) ?? null;
}

// parseFrontmatter — minimal YAML-frontmatter parser. Supports
// the small set of shapes the ADR template uses: scalar
// `key: value`, empty list `key: []`, and `key: null`. No nested
// objects; if we ever need them, switch to a real YAML lib.
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
    } else if (v === '[]') {
      fm[k] = [];
    } else if (v.startsWith('[') && v.endsWith(']')) {
      fm[k] = v
        .slice(1, -1)
        .split(',')
        .map((s) => s.trim().replace(/^['"]|['"]$/g, ''))
        .filter(Boolean);
    } else {
      fm[k] = v.replace(/^['"]|['"]$/g, '');
    }
  }
  return { fm, body };
}
