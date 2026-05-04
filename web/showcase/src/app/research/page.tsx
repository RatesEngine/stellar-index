import type { Metadata } from 'next';
import { ExternalLink, FileText } from 'lucide-react';

export const metadata: Metadata = {
  title: 'Research — methodology and engineering writeups',
  description:
    'Architecture decisions, integration audits, and methodology notes. The thinking behind every Rates Engine choice.',
};

type Item = {
  title: string;
  blurb: string;
  href: string;
  category: 'ADR' | 'Discovery' | 'Doc';
};

const FEATURED: Item[] = [
  {
    category: 'ADR',
    title: 'ADR-0003: i128 / u128 never truncates to int64',
    blurb:
      "Token amounts and reserves on Soroban are i128. Parsing them as int64 silently corrupts every value above 2^63. The full chain — big.Int in Go, NUMERIC in postgres, strings on the wire — is the only way that doesn't introduce silent bugs.",
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/docs/adr/0003-i128-no-truncation.md',
  },
  {
    category: 'ADR',
    title: 'ADR-0015: Closed-bucket-only API contract',
    blurb:
      'Every region serves the same rate at the same wall-clock time, even though they ingest independently. Achieved by only ever serving CLOSED buckets — the in-progress bucket is invisible until the next minute boundary.',
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/docs/adr/0015-last-closed-bucket-rate-serving.md',
  },
  {
    category: 'ADR',
    title: 'ADR-0019: Multi-factor confidence + freeze',
    blurb:
      'A single-source price is reported, but flagged. Outlier storms, source dropouts, and divergence vs other oracles drive the confidence score; severe-enough events trigger a freeze that halts price serving for the affected pair.',
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/docs/adr/0019-multi-factor-confidence.md',
  },
  {
    category: 'Discovery',
    title: 'Soroswap pair registry — why it\'s persisted in postgres',
    blurb:
      'SwapEvent carries token amounts but not which (token_0, token_1) the pair holds. We need the registry to resolve. Without persistence, every restart and parallel backfill chunk had to rebuild it from scratch — losing trades along the way.',
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/docs/discovery/dexes-amms/soroswap.md',
  },
  {
    category: 'Discovery',
    title: 'CAP-67 unified events — Protocol 23\'s "every transfer is one event"',
    blurb:
      "Post-Whisk (mainnet 2025-09-03), every classic-asset movement emits a unified transfer/mint/burn event with a 4th sep0011_asset topic. Pre-P23 we still parse operations + effects. Decoder switches based on topic shape.",
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/docs/discovery/notes/cap-67-unified-events.md',
  },
  {
    category: 'Discovery',
    title: 'Reflector\'s missing methods — twap() and x_*() do not exist',
    blurb:
      "The proposal claimed Reflector exposes on-chain TWAP and cross-pair methods. They don't exist on any of the three Reflector contracts (DEX/CEX/FX). We compute TWAP and cross-pair locally.",
    href: 'https://github.com/RatesEngine/rates-engine/blob/main/docs/discovery/oracles/reflector.md',
  },
];

const TOPICS: { name: string; description: string; href: string }[] = [
  {
    name: 'Architecture decisions',
    description:
      '25 ADRs covering ingest pipeline, storage choices, latency budget, validator topology, freeze semantics, and more.',
    href: 'https://github.com/RatesEngine/rates-engine/tree/main/docs/adr',
  },
  {
    name: 'Discovery audits',
    description:
      'Per-DEX, per-oracle audit notes verifying event schemas and decoder correctness against upstream Rust source.',
    href: 'https://github.com/RatesEngine/rates-engine/tree/main/docs/discovery',
  },
  {
    name: 'Operations runbooks',
    description:
      'Per-alert runbooks, archival-node bringup, disaster-recovery triage, SEV playbook, release process.',
    href: 'https://github.com/RatesEngine/rates-engine/tree/main/docs/operations',
  },
  {
    name: 'Architecture narratives',
    description:
      'Long-form designs for ingest pipeline, aggregation policy, supply pipeline, contract-schema evolution, showcase site data inventory.',
    href: 'https://github.com/RatesEngine/rates-engine/tree/main/docs/architecture',
  },
];

export default function ResearchPage() {
  return (
    <div className="mx-auto max-w-5xl space-y-8 p-6">
      <header className="space-y-3">
        <h1 className="text-3xl font-semibold tracking-tight">Research</h1>
        <p className="max-w-3xl text-base text-slate-600 dark:text-slate-400">
          The thinking behind every Rates Engine choice — architecture
          decisions, integration audits, methodology notes. All of this
          lives in the public repo; the curated highlights are below,
          and the topics index points you at the full archive.
        </p>
      </header>

      <section className="space-y-3">
        <h2 className="text-xl font-semibold tracking-tight">Featured</h2>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {FEATURED.map((f) => (
            <FeaturedCard key={f.href} item={f} />
          ))}
        </div>
      </section>

      <section className="space-y-3">
        <h2 className="text-xl font-semibold tracking-tight">Browse by topic</h2>
        <div className="grid grid-cols-1 gap-3 md:grid-cols-2">
          {TOPICS.map((t) => (
            <a
              key={t.href}
              href={t.href}
              target="_blank"
              rel="noreferrer"
              className="group flex flex-col gap-1 rounded-xl border border-slate-200 bg-white p-4 hover:border-brand-500 dark:border-slate-800 dark:bg-slate-900"
            >
              <h3 className="flex items-center gap-1.5 text-sm font-semibold group-hover:text-brand-600">
                {t.name}
                <ExternalLink className="h-3 w-3" />
              </h3>
              <p className="text-xs text-slate-600 dark:text-slate-400">
                {t.description}
              </p>
            </a>
          ))}
        </div>
      </section>

      <section className="rounded-xl border border-slate-200 bg-white p-5 text-sm dark:border-slate-800 dark:bg-slate-900">
        <h2 className="text-base font-semibold">Why we publish all of this</h2>
        <p className="mt-2 text-slate-600 dark:text-slate-400">
          Stellar already has Horizon. The reason a second pricing
          stack adds value is methodology — what gets included in the
          VWAP, how we handle cross-pair triangulation, what triggers a
          freeze, how we audit a Soroban contract before flipping
          BackfillSafe. None of that is useful behind a closed door.
          Every choice is in the repo, every audit is in the repo, and
          every disagreement has an ADR with a &quot;Why this not the
          alternative&quot; section.
        </p>
      </section>
    </div>
  );
}

function FeaturedCard({ item }: { item: Item }) {
  return (
    <a
      href={item.href}
      target="_blank"
      rel="noreferrer"
      className="group flex flex-col gap-2 rounded-xl border border-slate-200 bg-white p-4 hover:border-brand-500 dark:border-slate-800 dark:bg-slate-900"
    >
      <div className="flex items-center gap-2">
        <FileText className="h-3.5 w-3.5 text-slate-400" />
        <span className="text-[10px] font-medium uppercase tracking-wider text-slate-500">
          {item.category}
        </span>
      </div>
      <h3 className="text-sm font-semibold group-hover:text-brand-600">
        {item.title}
      </h3>
      <p className="text-xs text-slate-600 dark:text-slate-400">
        {item.blurb}
      </p>
      <p className="mt-1 inline-flex items-center gap-1 text-[11px] text-brand-600 opacity-0 transition group-hover:opacity-100">
        Read on GitHub <ExternalLink className="h-3 w-3" />
      </p>
    </a>
  );
}
