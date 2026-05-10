'use client';

import { useState } from 'react';

import { Panel } from '@/components/reveal';

// CURATED_ASSET_ABOUT — short descriptions for the major Stellar
// classic + Soroban assets. Currencies not in this map render no
// panel (clean fail; the listing already gives users the data they
// need). Multi-paragraph text rendered as separate <p>'s; first
// paragraph shows verbatim, the rest hide behind "Read more →".
//
// Curated by hand from each issuer's own docs + the Phase-1 audit
// pages under docs/discovery/. One-line addition extends the map.
const CURATED_ASSET_ABOUT: Record<string, string> = {
  XLM: `Stellar Lumens (XLM) is the native asset of the Stellar network — the public blockchain that Rates Engine indexes natively. Lumens pay transaction fees, fund minimum account reserves (currently 1 XLM per base reserve unit + 0.5 per ledger entry), and serve as a convenient bridge asset for path-payments between any two issued tokens.

XLM has a fixed maximum supply of ~50B; the Stellar Development Foundation periodically retires from its inflation pool. Unlike Bitcoin's purely-mined supply curve, lumen supply changes are governed by SDF allocation policy and the on-chain governance hooks the SCP consensus protocol exposes.`,
  USDC: `USD Coin (USDC) on Stellar is issued by Circle Internet Financial — the same regulated issuer that runs the Ethereum-native USDC. Each USDC is backed 1:1 by USD-denominated reserves (cash + short-duration U.S. treasuries) held in segregated accounts and attested to monthly by Deloitte.

USDC is the dominant USD-pegged stablecoin on Stellar by volume, the canonical USD proxy for the aggregator's stablecoin-fiat policy, and the quote leg of most active on-chain DEX pairs.`,
  EURC: `EURC is Circle's euro-denominated stablecoin. Like USDC it carries 1:1 fiat backing, monthly attestations, and Circle's regulated-issuer status. On Stellar EURC is the canonical EUR proxy and the largest non-USD stablecoin by 24h volume — most EUR cross-rates on the explorer flow through it.`,
  AQUA: `AQUA is the governance + reward token for the Aquarius DEX, a Stellar-native AMM that issues liquidity-incentive emissions to selected pools. Holders vote on emission allocations; LPs in voted-up pools earn AQUA on top of their swap fees. AQUA is one of the most-held classic credit assets on Stellar by trustline count.`,
  yXLM: `yXLM is a yield-bearing wrapper for XLM issued by Ultra Stellar (yieldblox.com). Holders earn variable APY funded by lending the underlying XLM into Blend pools and similar credit markets. The peg to underlying XLM is maintained by the issuer's redemption mechanism, not by an on-chain CDP.`,
  SHX: `SHX is the native token of StellarX, an early Stellar-native trading frontend. The asset has a long history on the network and remains in the top tier by trustline count, though active trade volume is concentrated against XLM and USDC.`,
  BLND: `BLND is the governance token for Blend Capital's Stellar lending protocol. Holders participate in protocol-level decisions; the token is not redeemable for collateral. BLND issuance is controlled by the BLND-USDC backstop pool that secures the protocol's bad-debt insurance fund.`,
  XRP: `XRP on Stellar is issued by Bitstamp's wallet-bridge token, NOT the native XRP Ledger asset. Trustlines hold an IOU that the issuer redeems via the bridge. Liquidity is materially lower than the native asset on the XRP Ledger; for the canonical XRP price reference the explorer's XRP rate triangulates via Binance/Coinbase XRP/USD.`,
};

export function AssetAbout({ symbol }: { symbol: string }) {
  const text = CURATED_ASSET_ABOUT[symbol];
  if (!text) return null;
  return <ExpandableText title={`About ${symbol}`} body={text} />;
}

function ExpandableText({ title, body }: { title: string; body: string }) {
  const [expanded, setExpanded] = useState(false);
  const paragraphs = body.split(/\n\s*\n/).filter(Boolean);
  const teaser = paragraphs[0];
  const more = paragraphs.slice(1);
  const hasMore = more.length > 0;
  return (
    <Panel
      title={title}
      bodyClassName="text-sm text-slate-700 dark:text-slate-300 space-y-3 leading-relaxed"
    >
      <p>{teaser}</p>
      {expanded && more.map((p, i) => <p key={i}>{p}</p>)}
      {hasMore && (
        <button
          type="button"
          onClick={() => setExpanded((v) => !v)}
          className="text-xs text-brand-600 hover:underline"
        >
          {expanded ? 'Show less' : 'Read more →'}
        </button>
      )}
    </Panel>
  );
}
