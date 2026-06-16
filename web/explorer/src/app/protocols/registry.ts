// Static protocol registry — the bounded name set for generateStaticParams,
// mirrored from internal/api/v1/protocols_registry.go. The Go registry is
// the source of truth for the wire data (categories, descriptions, genesis,
// factories, event kinds, completeness); this file only needs the NAME set
// (so the static export knows which slugs to pre-render) plus a friendly
// display label per protocol. Everything else is fetched at runtime from
// GET /v1/protocols/{name}.
//
// If protocols_registry.go gains or drops a protocol, add/remove the row
// here too — CI doesn't cross-check them, the static export simply won't
// pre-render a slug that isn't listed.

export interface ProtocolRegistryEntry {
  /** Canonical source name — the /v1/protocols/{name} path segment. */
  name: string;
  /** Friendly display label for headers + cards. */
  label: string;
  /** Short fallback description (the API serves the authoritative one). */
  description: string;
}

export const PROTOCOLS: ProtocolRegistryEntry[] = [
  {
    name: 'sdex',
    label: 'SDEX',
    description: "Stellar's protocol-native central-limit order book.",
  },
  {
    name: 'soroswap',
    label: 'Soroswap',
    description: 'Constant-product Soroban AMM pairs.',
  },
  {
    name: 'aquarius',
    label: 'Aquarius',
    description: 'Incentivised constant-product and stableswap pools.',
  },
  {
    name: 'phoenix',
    label: 'Phoenix',
    description: 'Soroban constant-product AMM with liquidity + stake events.',
  },
  {
    name: 'comet',
    label: 'Comet',
    description: 'Balancer-v1-style weighted pools on Soroban.',
  },
  {
    name: 'blend',
    label: 'Blend',
    description: 'Isolated lending pools on Soroban.',
  },
  {
    name: 'defindex',
    label: 'DeFindex',
    description: 'Yield vaults and strategies across Soroban DeFi.',
  },
  {
    name: 'cctp',
    label: 'Circle CCTP',
    description: 'Canonical burn-and-mint USDC bridging.',
  },
  {
    name: 'rozo',
    label: 'Rozo',
    description: 'Intent-bridge payment settlement on Stellar.',
  },
  {
    name: 'soroswap-router',
    label: 'Soroswap Router',
    description: 'Aggregated multi-hop swap intents from router invocations.',
  },
  {
    name: 'band',
    label: 'Band Protocol',
    description: 'Reference-rate oracle observed from relay() invocations.',
  },
  {
    name: 'reflector-dex',
    label: 'Reflector (DEX)',
    description: 'Reflector oracle — Stellar-DEX price feed.',
  },
  {
    name: 'reflector-cex',
    label: 'Reflector (CEX)',
    description: 'Reflector oracle — centralized-exchange price feed.',
  },
  {
    name: 'reflector-fx',
    label: 'Reflector (FX)',
    description: 'Reflector oracle — fiat exchange-rate feed.',
  },
  {
    name: 'redstone',
    label: 'RedStone',
    description: 'Batched multi-feed price pushes to the RedStone adapter.',
  },
];

const BY_NAME = new Map(PROTOCOLS.map((p) => [p.name, p]));

export function protocolMeta(name: string): ProtocolRegistryEntry | undefined {
  return BY_NAME.get(name);
}

// Category → chip tone. The API serves the authoritative category string;
// this maps it to the slate/brand palette used across the explorer. Unknown
// categories fall through to a neutral chip (keeps rendering if the Go
// registry adds a category before this map is updated).
export const CATEGORY_TONE: Record<string, string> = {
  dex: 'bg-line text-ink',
  amm: 'bg-up-subtle text-up-strong',
  lending: 'bg-brand-100 text-brand-800',
  yield: 'bg-violet-100 text-violet-800',
  bridge: 'bg-warn-50 text-warn-700',
  oracle: 'bg-indigo-100 text-brand-800',
  token: 'bg-teal-100 text-teal-800',
};

export function categoryTone(category: string): string {
  return (
    CATEGORY_TONE[category] ??
    'bg-surface-subtle text-ink-body'
  );
}
