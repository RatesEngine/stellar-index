import type { Metadata } from 'next';
import Link from 'next/link';

export const metadata: Metadata = {
  title: 'SDEX — the Stellar Decentralized Exchange',
  description:
    'The SDEX is the protocol-native central-limit order book built into every Stellar ledger — no smart contract required. How it works, its markets, and how Stellar Index indexes every offer and trade.',
  alternates: { canonical: '/sdex' },
  openGraph: { title: 'SDEX — Stellar Decentralized Exchange', description: 'Stellar’s protocol-native order book: markets, offers, and trades.', url: 'https://stellarindex.io/sdex', type: 'website' },
};

export default function SdexPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-8 px-6 py-10">
      <header className="space-y-3">
        <h1 className="text-3xl font-semibold tracking-tight">SDEX — Stellar Decentralized Exchange</h1>
        <p className="text-base text-ink-body">
          The SDEX is Stellar&rsquo;s <strong>protocol-native</strong> central-limit
          order book — built into every ledger, no smart contract required. Anyone
          can post an offer to trade one asset for another, and the network matches
          them deterministically at ledger close. It predates Soroban and is
          distinct from the on-chain AMM protocols.
        </p>
      </header>

      <section className="space-y-3 text-[15px] leading-relaxed text-ink-body">
        <p>
          Stellar Index ingests every SDEX <code className="font-mono text-sm">ManageOffer</code> and
          path-payment trade directly from the certified ledger lake, so SDEX
          volume contributes to the same aggregate VWAP as every other venue.
        </p>
      </section>

      <div className="grid gap-4 sm:grid-cols-2">
        <Link href="/protocols/sdex" className="rounded-xl border border-line bg-surface p-5 hover:border-brand-300 hover:bg-surface-subtle">
          <h2 className="text-lg font-semibold text-ink">SDEX analytics</h2>
          <p className="mt-1.5 text-sm text-ink-body">Volume, trade counts, and activity for the order book.</p>
        </Link>
        <Link href="/markets" className="rounded-xl border border-line bg-surface p-5 hover:border-brand-300 hover:bg-surface-subtle">
          <h2 className="text-lg font-semibold text-ink">Markets</h2>
          <p className="mt-1.5 text-sm text-ink-body">Aggregate per-pair prices across SDEX + every other venue.</p>
        </Link>
      </div>

      <p className="border-t border-line pt-5 text-sm text-ink-muted">
        For Soroban AMM protocols (Soroswap, Aquarius, Phoenix, Comet), see{' '}
        <Link href="/amm" className="text-brand-600 hover:underline">AMM protocols</Link>.
      </p>
    </div>
  );
}
