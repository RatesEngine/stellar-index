// Build-time loader for the curated subset of architecture docs
// surfaced on /research/architecture/<slug>. Rather than blanket-
// publishing every file under docs/architecture/, the public
// surface is a hand-picked allow-list — integrators want the
// long-form system narratives, not the internal launch-readiness
// backlog or the ecosystem-review notes.
//
// Adding a doc to this surface: push its slug into CURATED below
// (the slug is the filename minus .md), set a short description
// for the index page, and ensure it's safe to be public.

import { readFileSync } from 'node:fs';
import path from 'node:path';

export type ArchitectureDoc = {
  slug: string;
  title: string;
  description: string;
  last_verified: string;
  body: string;
  source_path: string;
};

const REPO_ROOT = path.resolve(process.cwd(), '..', '..');
const DOCS_DIR = path.join(REPO_ROOT, 'docs', 'architecture');

// CURATED — every doc that should be browsable on the public
// site. Order is presentation order on /research (most-foundational
// first). The `description` powers the index card; pick something
// a non-Rates-Engine engineer would understand.
const CURATED: { slug: string; description: string }[] = [
  {
    slug: 'ingest-pipeline',
    description:
      'How a Stellar ledger event becomes a row in our trades hypertable. Galexie → MinIO → ledgerstream → dispatcher → per-source decoders. The one canonical path.',
  },
  {
    slug: 'aggregation-plan',
    description:
      'The policy chain from raw trade to served price — class filter, outlier filter, VWAP, freeze gate. Every load-bearing decision the aggregator makes.',
  },
  {
    slug: 'supply-pipeline',
    description:
      'Three-domain supply derivation: XLM hard-coded, classic from ledger entries, SEP-41 from event sums. Per-asset refresh cadence. ADR-0011 in full.',
  },
  {
    slug: 'contract-schema-evolution',
    description:
      'Soroban DeFi contracts upgrade in place, and event schemas can change with them. How decoders stay correct across WASM versions, including for backfill.',
  },
  {
    slug: 'oracle-manipulation-defense',
    description:
      'Attack catalogue: TWAP-window stuffing, single-block manipulation, oracle drift. The defensive layers we run, ordered by how cheap they are to detect.',
  },
  {
    slug: 'ha-plan',
    description:
      'Per-region high-availability topology — colo primary + cloud DR, three-tier hot/warm/cold storage, the failover decision tree.',
  },
  {
    slug: 'semver-policy',
    description:
      'What bumps the major / minor / patch on every binary release. Public types in pkg/* are SemVer-stable; internal/* moves freely.',
  },
];

let cache: ArchitectureDoc[] | null = null;

export function loadArchitectureDocs(): ArchitectureDoc[] {
  if (cache) return cache;
  const out: ArchitectureDoc[] = [];
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
      source_path: `docs/architecture/${file}`,
    });
  }
  cache = out;
  return out;
}

export function loadArchitectureDoc(slug: string): ArchitectureDoc | null {
  return loadArchitectureDocs().find((d) => d.slug === slug) ?? null;
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
