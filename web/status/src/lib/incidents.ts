// Build-time loader for the incident corpus. Reads
// `internal/incidents/data/*.md` from the repo root, parses YAML
// frontmatter, and exposes typed records for /incident/[slug].
//
// The runtime API at /v1/incidents serves the same corpus
// (embedded into the Go binary at compile time). We re-load here
// at build time so the static export can pre-render every
// postmortem page without a client-side fetch.

import { readFileSync, readdirSync } from 'node:fs';
import path from 'node:path';

export type IncidentSeverity = 'SEV-1' | 'SEV-2' | 'SEV-3';
export type IncidentStatus =
  | 'investigating'
  | 'identified'
  | 'monitoring'
  | 'resolved';

export type Incident = {
  slug: string;
  title: string;
  severity: IncidentSeverity;
  status: IncidentStatus;
  date: string;
  started_at: string;
  resolved_at: string | null;
  affected_components: string[];
  body: string;
  source_path: string;
};

const REPO_ROOT = path.resolve(process.cwd(), '..', '..');
const DATA_DIR = path.join(REPO_ROOT, 'internal', 'incidents', 'data');

let cache: Incident[] | null = null;

export function loadIncidents(): Incident[] {
  if (cache) return cache;
  let files: string[] = [];
  try {
    files = readdirSync(DATA_DIR);
  } catch {
    cache = [];
    return cache;
  }
  const out: Incident[] = [];
  for (const f of files) {
    if (!f.endsWith('.md')) continue;
    if (f.startsWith('_')) continue; // _template.md
    const full = path.join(DATA_DIR, f);
    const raw = readFileSync(full, 'utf-8');
    const parsed = parseFrontmatter(raw);
    if (!parsed) continue;
    const slug = f.replace(/\.md$/, '');
    out.push({
      slug,
      title: String(parsed.fm['title'] ?? slug),
      severity: (String(parsed.fm['severity'] ?? 'SEV-3') as IncidentSeverity),
      status: (String(parsed.fm['status'] ?? 'resolved') as IncidentStatus),
      date: String(parsed.fm['date'] ?? ''),
      started_at: String(parsed.fm['started_at'] ?? ''),
      resolved_at:
        parsed.fm['resolved_at'] && parsed.fm['resolved_at'] !== 'null'
          ? String(parsed.fm['resolved_at'])
          : null,
      affected_components: Array.isArray(parsed.fm['affected_components'])
        ? (parsed.fm['affected_components'] as string[])
        : [],
      body: parsed.body.trim(),
      source_path: `internal/incidents/data/${f}`,
    });
  }
  // Newest first.
  out.sort((a, b) => (a.started_at < b.started_at ? 1 : -1));
  cache = out;
  return out;
}

export function loadIncident(slug: string): Incident | null {
  return loadIncidents().find((i) => i.slug === slug) ?? null;
}

// parseFrontmatter — handles the small set of shapes our incident
// template uses: scalar `key: value`, quoted strings, `key: null`,
// and bullet lists indented under a key:
//
//   affected_components:
//     - indexer
//     - storage
//
// No nested objects. If we ever need them, swap for a real YAML
// lib.
function parseFrontmatter(
  raw: string,
): { fm: Record<string, unknown>; body: string } | null {
  if (!raw.startsWith('---')) return { fm: {}, body: raw };
  const end = raw.indexOf('\n---', 3);
  if (end === -1) return null;
  const head = raw.slice(3, end).trim();
  const body = raw.slice(end + 4).replace(/^\n/, '');

  const fm: Record<string, unknown> = {};
  const lines = head.split('\n');
  let i = 0;
  while (i < lines.length) {
    const line = lines[i]!;
    const m = line.match(/^([A-Za-z_][A-Za-z0-9_]*):\s*(.*)$/);
    if (!m) {
      i++;
      continue;
    }
    const k = m[1]!;
    const v = m[2]!.trim();
    if (v === '' || v === 'null') {
      // Could be a bullet-list block.
      const items: string[] = [];
      let j = i + 1;
      while (j < lines.length && /^\s+-\s+/.test(lines[j]!)) {
        items.push(lines[j]!.replace(/^\s+-\s+/, '').replace(/^['"]|['"]$/g, ''));
        j++;
      }
      if (items.length > 0) {
        fm[k] = items;
        i = j;
        continue;
      }
      fm[k] = v === 'null' ? null : '';
    } else {
      fm[k] = v.replace(/^['"]|['"]$/g, '');
    }
    i++;
  }
  return { fm, body };
}
