import type { Metadata } from 'next';
import Link from 'next/link';
import { Mail } from 'lucide-react';
import { GithubIcon } from '@/components/GithubIcon';

export const metadata: Metadata = {
  title: 'Company — who we are',
  description:
    'Stellar Index — vendor-neutral pricing infrastructure for the Stellar network. Real-time and historical prices for every Stellar asset. Apache-2.0, pre-v1.',
  alternates: { canonical: '/company' },
};

export default function CompanyPage() {
  return (
    <div className="mx-auto max-w-3xl space-y-12 px-6 py-12">
      <header className="space-y-3">
        <p className="font-mono text-xs uppercase tracking-widest text-brand-600">
          Company
        </p>
        <h1 className="text-4xl font-semibold tracking-tight">
          Pricing infrastructure for Stellar.
        </h1>
        <p className="text-base text-ink-body">
          Stellar Index is a public, vendor-neutral pricing surface for
          the Stellar network — complete, verified, per-protocol on-chain
          data and an aggregated pricing API for every Stellar asset.
        </p>
      </header>

      <section className="space-y-3">
        <h2 className="text-2xl font-semibold tracking-tight">What we do</h2>
        <p className="text-sm text-ink-body">
          We ingest every trade on the Stellar network — on-chain DEXes
          (Soroswap, Phoenix, Aquarius, SDEX, Comet), classic SDEX
          orderbooks, lending protocol auctions (Blend), and major
          centralised exchanges (Binance, Coinbase, Kraken, Bitstamp) —
          and serve a single VWAP through a public REST + SSE API.
          Oracles (Reflector, Redstone, Band) get cross-checked but
          never priced into the canonical VWAP. Everything is
          documented down to the source code, the methodology, and
          the architecture decisions.
        </p>
      </section>

      <section className="space-y-3">
        <h2 className="text-2xl font-semibold tracking-tight">How we ship</h2>
        <ul className="space-y-2 text-sm text-ink-body">
          <li className="flex gap-2">
            <span className="text-ink-faint">•</span>
            <span>
              <strong className="text-ink-body">
                Apache-2.0.
              </strong>{' '}
              The whole codebase — backend, explorer, status site,
              widgets, infrastructure-as-code — is on{' '}
              <a
                href="https://github.com/StellarIndex/stellar-index"
                target="_blank"
                rel="noreferrer noopener"
                className="text-brand-600 hover:underline"
              >
                GitHub
              </a>{' '}
              under the most permissive license that still preserves
              attribution. Run your own copy if you want; we&apos;d
              prefer you didn&apos;t need to.
            </span>
          </li>
          <li className="flex gap-2">
            <span className="text-ink-faint">•</span>
            <span>
              <strong className="text-ink-body">
                Three-region active-active.
              </strong>{' '}
              Per{' '}
              <Link
                href="/research/adr/0008"
                className="text-brand-600 hover:underline"
              >
                ADR-0008
              </Link>
              , Stellar Index runs in three geographically separate
              regions (R1 Hetzner, R2 AWS, R3 Vultr), each with an
              independent history archive and full validator. Every
              region serves the same rate at the same wall-clock
              time — see{' '}
              <Link
                href="/research/adr/0015"
                className="text-brand-600 hover:underline"
              >
                ADR-0015
              </Link>
              .
            </span>
          </li>
          <li className="flex gap-2">
            <span className="text-ink-faint">•</span>
            <span>
              <strong className="text-ink-body">
                Public-first.
              </strong>{' '}
              Anonymous reads work without an account. The free tier
              covers prototyping, low-traffic embeds, and read-only
              integrations. Paid tiers exist for higher rate limits
              and dedicated SLAs — never gated data.
            </span>
          </li>
          <li className="flex gap-2">
            <span className="text-ink-faint">•</span>
            <span>
              <strong className="text-ink-body">
                Honest about what we don&apos;t have.
              </strong>{' '}
              Forex is currently a daily-grain shim while we wire a
              proper feed. Soroban DEX TVL isn&apos;t ingested yet.
              CEX order-book depth isn&apos;t ingested. The{' '}
              <Link href="/methodology" className="text-brand-600 hover:underline">
                /methodology
              </Link>{' '}
              page lists every gap and the path to closing each.
            </span>
          </li>
        </ul>
      </section>

      <section className="space-y-3">
        <h2 className="text-2xl font-semibold tracking-tight">Funding</h2>
        <p className="text-sm text-ink-body">
          Development was supported by a grant from the Stellar Community
          Fund. Operating revenue from paid tiers covers ongoing
          infrastructure + operator headcount; the public tier and the
          open-source codebase are perpetual commitments regardless of
          commercial outcomes.
        </p>
      </section>

      <section className="space-y-3">
        <h2 className="text-2xl font-semibold tracking-tight">What&apos;s next</h2>
        <p className="text-sm text-ink-body">
          v1 ships in the coming weeks. The roadmap that gets us there
          lives in{' '}
          <a
            href="https://github.com/StellarIndex/stellar-index/blob/main/docs/architecture/launch-readiness-backlog.md"
            target="_blank"
            rel="noreferrer noopener"
            className="text-brand-600 hover:underline"
          >
            launch-readiness-backlog.md
          </a>
          ; per-release notes accumulate in{' '}
          <Link href="/changelog" className="text-brand-600 hover:underline">
            /changelog
          </Link>
          . Post-v1 priorities — order-book depth ingest, DEX TVL,
          paid forex feed, multi-network expansion — are public in the
          same backlog.
        </p>
      </section>

      <section className="rounded-xl border border-line bg-surface p-6 shadow-sm">
        <h2 className="text-lg font-semibold">Get in touch</h2>
        <div className="mt-3 grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Link
            href="/contact"
            className="inline-flex items-center justify-center gap-2 rounded-md border border-line px-3 py-2 text-sm hover:border-brand-500 hover:text-brand-600"
          >
            <Mail className="h-4 w-4" />
            Contact
          </Link>
          <a
            href="https://github.com/StellarIndex/stellar-index"
            target="_blank"
            rel="noreferrer noopener"
            className="inline-flex items-center justify-center gap-2 rounded-md border border-line px-3 py-2 text-sm hover:border-brand-500 hover:text-brand-600"
          >
            <GithubIcon className="h-4 w-4" />
            GitHub
          </a>
          <Link
            href="/careers"
            className="inline-flex items-center justify-center gap-2 rounded-md border border-line px-3 py-2 text-sm hover:border-brand-500 hover:text-brand-600"
          >
            Careers
          </Link>
        </div>
      </section>
    </div>
  );
}
