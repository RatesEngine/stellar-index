// Build-time loader for the curated subset of operator docs
// surfaced on /research/operations/<slug>. Most of
// docs/operations/ is per-alert runbooks that target on-call
// engineers — those stay private. The four docs allow-listed
// here are the canonical recipes any prospective operator or
// auditor would want to read before standing up their own copy.

import { readFileSync } from 'node:fs';
import path from 'node:path';

export type OperationsDoc = {
  slug: string;
  title: string;
  description: string;
  last_verified: string;
  body: string;
  source_path: string;
};

const REPO_ROOT = path.resolve(process.cwd(), '..', '..');
const DOCS_DIR = path.join(REPO_ROOT, 'docs', 'operations');

const CURATED: { slug: string; description: string }[] = [
  {
    slug: 'archival-node-bringup',
    description:
      'Six-step recipe to stand up a new archival node from a fresh box. ~10–13 hours wall-clock for the full mainnet history catch-up; mostly bandwidth-bound. Includes the disaster-recovery triage tree.',
  },
  {
    slug: 'release-process',
    description:
      'How a binary release is cut. CHANGELOG promotion, tag push, automated GitHub release, container build, and container push to ghcr.io. Includes the manual fallback path when CI is unavailable.',
  },
  {
    slug: 'deploy-workflow',
    description:
      'Pushing a tagged release into a region. Stage → backup → atomic install → restart → health probe → automatic rollback on failure. Operator-triggered, never automatic on tag.',
  },
  {
    slug: 'sev-playbook',
    description:
      'Incident response procedure. Severity classification (SEV-1 / SEV-2 / SEV-3), responder roles, customer-comms cadence, postmortem requirement.',
  },
];

let cache: OperationsDoc[] | null = null;

export function loadOperationsDocs(): OperationsDoc[] {
  if (cache) return cache;
  const out: OperationsDoc[] = [];
  for (const c of CURATED) {
    const file = `${c.slug}.md`;
    const full = path.join(DOCS_DIR, file);
    let raw: string;
    try {
      raw = readFileSync(full, 'utf-8');
    } catch {
      continue;
    }
    const parsed = parseFrontmatter(raw);
    if (!parsed) continue;
    out.push({
      slug: c.slug,
      title: String(parsed.fm['title'] ?? c.slug),
      description: c.description,
      last_verified: String(parsed.fm['last_verified'] ?? ''),
      body: parsed.body.trim(),
      source_path: `docs/operations/${file}`,
    });
  }
  cache = out;
  return out;
}

export function loadOperationsDoc(slug: string): OperationsDoc | null {
  return loadOperationsDocs().find((d) => d.slug === slug) ?? null;
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
    fm[m[1]!] = m[2]!.trim().replace(/^['"]|['"]$/g, '');
  }
  return { fm, body };
}
