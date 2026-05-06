import type { Metadata } from 'next';
import Link from 'next/link';

import { Panel } from '@/components/reveal';

export const metadata: Metadata = {
  title: 'MEV — suspicious-pattern detector',
  description:
    'Sandwich attacks, oracle-update sandwiches, liquidation cascades, wash trading. The MEV detector watches Stellar specifically.',
};

const PATTERNS: { name: string; description: string; example: string }[] = [
  {
    name: 'Sandwich',
    description:
      'Front-run + back-run around a victim swap. Detector groups (front, victim, back) when they share (ledger, op_index neighbors) and the front+back account match.',
    example:
      'Bot detects a large XLM→USDC swap in mempool, drops a smaller XLM→USDC swap before it (raising price), and an opposite-direction swap immediately after.',
  },
  {
    name: 'Oracle-update sandwich',
    description:
      'Specific to Blend lending. A liquidation immediately after a Reflector update, where the same account that placed the liquidate also called update_price() — meaning the oracle update was profitable for them.',
    example:
      'Account writes a Reflector price update that crosses a Blend liquidation threshold, then in the same ledger fires the liquidate() call against an undercollateralised position.',
  },
  {
    name: 'Liquidation cascade',
    description:
      "A liquidation that triggers another liquidation within a short window — usually because the first liquidation's auction sold collateral at a discount, depressing the on-chain oracle price for downstream pools.",
    example:
      "Pool A liquidates a large XLM-collateralised position, dumping XLM into Soroswap. Reflector reads the depressed XLM price; pool B's positions cross liquidation threshold and get liquidated next ledger.",
  },
  {
    name: 'Wash trading',
    description:
      'Self-trading or coordinated trading to inflate observation count for a thin asset. Detector flags rapid back-and-forth trades on the same pair that net to ~zero volume.',
    example:
      'Two accounts swap the same pair back and forth across many ledgers, generating trade count without moving real value.',
  },
];

export default function MevPage() {
  return (
    <div className="mx-auto max-w-7xl space-y-6 px-6 py-8">
      <header className="space-y-2">
        <h1 className="text-3xl font-semibold tracking-tight">MEV</h1>
        <p className="max-w-3xl text-sm text-slate-600 dark:text-slate-400">
          Suspicious-pattern detector feed. Sandwich attacks, oracle
          sandwiches, liquidation cascades, wash trading — the
          patterns Stellar exhibits, with the same canonical-VWAP
          context surrounding each event.
        </p>
      </header>

      <Panel
        title="What we look for"
        hint="Pattern catalogue — confidence-scored, false-positive-tolerant"
      >
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          {PATTERNS.map((p) => (
            <div
              key={p.name}
              className="rounded-lg border border-slate-200 bg-slate-50 p-3 text-xs dark:border-slate-800 dark:bg-slate-900/50"
            >
              <h3 className="text-sm font-semibold">{p.name}</h3>
              <p className="mt-1 text-slate-600 dark:text-slate-400">
                {p.description}
              </p>
              <p className="mt-2 italic text-slate-500">
                e.g. {p.example}
              </p>
            </div>
          ))}
        </div>
      </Panel>

      <Panel
        title="Why this matters for pricing"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          MEV trades show up as ordinary swaps on the wire. Without
          detection, a sandwich pair would inflate observation count
          on the same pair the victim contributed to, and an oracle-
          update sandwich would skew the oracle reading the
          liquidation price was set against.
        </p>
        <p>
          Detected events get a per-trade flag in{' '}
          <code className="font-mono text-xs">mev_events</code>{' '}
          (migration 0021). The aggregator can then optionally exclude
          flagged trades from VWAP — the policy lever lives at the
          aggregator, not the decoder, so we keep the raw observation
          and let downstream methodology decide.
        </p>
      </Panel>

      <Panel
        title="Coming next"
        bodyClassName="text-sm text-slate-600 dark:text-slate-400 space-y-2"
      >
        <p>
          Auto-flagged event feed, per-kind tally, event drill-down
          with tx-hash deep links plumb in once the{' '}
          <code className="font-mono text-xs">/v1/mev</code> endpoint
          ships (Phase 5). The schema is in place; the detector
          worker (Phase 3) lands as the aggregator data flow
          stabilises. For the underlying methodology see the{' '}
          <Link href="/research" className="underline decoration-dotted">
            research index
          </Link>
          .
        </p>
      </Panel>
    </div>
  );
}
