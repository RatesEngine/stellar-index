// Build-time loader for the curated subset of Phase-1 discovery
// audits surfaced on /research/discovery/<slug>. These are the
// per-DEX / per-oracle integration audits — they document how
// each on-chain venue's event schema was verified against the
// upstream Rust source, with the contract repo + commit
// referenced so a reader can replay the audit themselves.
//
// Discovery docs don't carry YAML frontmatter. Title is taken
// from the first `# <title>` line of the body; the rest of the
// metadata (category, description) is hand-curated below.

import { readFileSync } from 'node:fs';
import path from 'node:path';

export type DiscoveryCategory = 'DEX' | 'Oracle' | 'Data source';

export type DiscoveryDoc = {
  slug: string;
  title: string;
  category: DiscoveryCategory;
  description: string;
  body: string;
  source_path: string;
};

const REPO_ROOT = path.resolve(process.cwd(), '..', '..');

// CURATED — only docs in this list become public pages. The
// remainder of docs/discovery/ stays private (read-only Phase-1
// archive). Order is presentation order on /research.
const CURATED: {
  slug: string;
  category: DiscoveryCategory;
  rel: string;
  description: string;
}[] = [
  // DEXes / AMMs.
  {
    slug: 'sdex',
    category: 'DEX',
    rel: 'docs/discovery/dexes-amms/sdex.md',
    description:
      "Stellar's classic order-book DEX. Operations + effects parsing pre-P23, unified-events post-P23.",
  },
  {
    slug: 'soroswap',
    category: 'DEX',
    rel: 'docs/discovery/dexes-amms/soroswap.md',
    description:
      'Constant-product AMM. SwapEvent + SyncEvent correlation by (ledger, tx_hash, op_index) — reserves come from the SyncEvent that follows each Swap.',
  },
  {
    slug: 'phoenix',
    category: 'DEX',
    rel: 'docs/discovery/dexes-amms/phoenix.md',
    description:
      'Eight events per swap (one per field). Reconstruction requires grouping all eight by topic.',
  },
  {
    slug: 'aquarius',
    category: 'DEX',
    rel: 'docs/discovery/dexes-amms/aquarius.md',
    description:
      'AMM with gauges. Per-pool fee dynamics and gauge-vote weighting documented.',
  },
  {
    slug: 'comet',
    category: 'DEX',
    rel: 'docs/discovery/dexes-amms/comet.md',
    description:
      'Balancer-V1 fork. Shared ("POOL", <event>) topic across every pool — the decoder matches on topic bytes, not contract address.',
  },
  {
    slug: 'blend',
    category: 'DEX',
    rel: 'docs/discovery/dexes-amms/blend.md',
    description:
      'Lending protocol. We index the backstop pool as a sanity-check liquidity source; full lending-position indexing is post-launch.',
  },
  // Oracles.
  {
    slug: 'reflector',
    category: 'Oracle',
    rel: 'docs/discovery/oracles/reflector.md',
    description:
      'Three separate contracts (DEX / CEX / FX). On-chain twap() and x_*() methods do not exist — we compute TWAP and cross-pair locally.',
  },
  {
    slug: 'band',
    category: 'Oracle',
    rel: 'docs/discovery/oracles/band.md',
    description:
      'Soroban contract emits zero events. We observe relay()/force_relay() InvokeContract args via the dispatcher’s ContractCallDecoder hook.',
  },
  {
    slug: 'redstone',
    category: 'Oracle',
    rel: 'docs/discovery/oracles/redstone.md',
    description:
      'Adapter emits one "REDSTONE" event per batch push containing every updated feed. Feed IDs come from the tx’s op args, not the event body.',
  },
  {
    slug: 'chainlink',
    category: 'Oracle',
    rel: 'docs/discovery/oracles/chainlink.md',
    description: 'HTTP feed cross-check, not on-chain.',
  },
];

let cache: DiscoveryDoc[] | null = null;

export function loadDiscoveryDocs(): DiscoveryDoc[] {
  if (cache) return cache;
  const out: DiscoveryDoc[] = [];
  for (const c of CURATED) {
    const full = path.join(REPO_ROOT, c.rel);
    let raw: string;
    try {
      raw = readFileSync(full, 'utf-8');
    } catch {
      continue;
    }
    const title = readTitle(raw) ?? c.slug;
    out.push({
      slug: c.slug,
      title,
      category: c.category,
      description: c.description,
      body: raw.trim(),
      source_path: c.rel,
    });
  }
  cache = out;
  return out;
}

export function loadDiscoveryDoc(slug: string): DiscoveryDoc | null {
  return loadDiscoveryDocs().find((d) => d.slug === slug) ?? null;
}

// readTitle — first `# <text>` line. Discovery docs don't have
// YAML frontmatter; the H1 is the canonical title.
function readTitle(raw: string): string | null {
  for (const line of raw.split('\n')) {
    const m = line.match(/^# (.+)$/);
    if (m) return m[1]!.trim();
  }
  return null;
}
