/**
 * Seed data for the design-iteration phase. Lifted out of the
 * `/coins` table so the detail page (`/coins/{slug}`) can read
 * the same source. Replaced by API hooks once `/v1/coins` ships
 * per data-inventory §10.1.
 */
export type SeedCoin = {
  rank: number;
  rankDelta: number;
  slug: string;
  ticker: string;
  name: string;
  description?: string;
  type: 'native' | 'classic' | 'soroban';
  issuer?: string;
  homeDomain?: string;
  price: number;
  h1: number;
  h24: number;
  d7: number;
  d30: number;
  volume24h: number;
  marketCap: number;
  circulatingSupply: number;
  totalSupply: number;
  spark: number[];
  confidence: number;
  sources: { name: string; weight: number; venue?: string }[];
};

export const SEED_COINS: SeedCoin[] = [
  {
    rank: 1, rankDelta: 0, type: 'native',
    slug: 'stellar', ticker: 'XLM', name: 'Stellar',
    description: 'Native asset of the Stellar network. Powers all on-chain operations + reserves.',
    price: 0.1234, h1: 0.5, h24: 3.2, d7: -1.1, d30: 18.4,
    volume24h: 12_400_000, marketCap: 3_400_000_000,
    circulatingSupply: 27_900_000_000, totalSupply: 50_001_802_437,
    spark: [0.115, 0.118, 0.12, 0.119, 0.122, 0.121, 0.1234],
    confidence: 92,
    sources: [
      { name: 'sdex', weight: 0.32, venue: 'SDEX' },
      { name: 'binance', weight: 0.28, venue: 'Binance' },
      { name: 'kraken', weight: 0.18, venue: 'Kraken' },
      { name: 'coinbase', weight: 0.12, venue: 'Coinbase' },
      { name: 'bitstamp', weight: 0.10, venue: 'Bitstamp' },
    ],
  },
  {
    rank: 2, rankDelta: 1, type: 'soroban',
    slug: 'aqua', ticker: 'AQUA', name: 'Aquarius',
    description: 'Liquidity gauge token for the Aquarius AMM on Soroban.',
    issuer: 'GBNZILSTVQZ4R7IKQDGHYGY2QXL5QOFJYQMXPKWRRM5PAV7Y4M67AQUA',
    homeDomain: 'aqua.network',
    price: 0.0042, h1: 1.2, h24: 8.1, d7: 14.5, d30: 22.1,
    volume24h: 2_100_000, marketCap: 421_000_000,
    circulatingSupply: 100_000_000_000, totalSupply: 100_000_000_000,
    spark: [0.0036, 0.0038, 0.0039, 0.0041, 0.004, 0.0042, 0.0042],
    confidence: 78,
    sources: [
      { name: 'soroswap', weight: 0.45, venue: 'Soroswap' },
      { name: 'phoenix', weight: 0.30, venue: 'Phoenix' },
      { name: 'aquarius', weight: 0.18, venue: 'Aquarius' },
      { name: 'sdex', weight: 0.07, venue: 'SDEX' },
    ],
  },
  {
    rank: 3, rankDelta: -1, type: 'classic',
    slug: 'usdc', ticker: 'USDC', name: 'USD Coin (Centre)',
    description: 'Centre-issued fiat-backed stablecoin on Stellar.',
    issuer: 'GA5ZSEJYB37JRC5AVCIA5MOP4RHTM335X2KGX3IHOJAPP5RE34K4KZVN',
    homeDomain: 'centre.io',
    price: 1.0001, h1: 0.0, h24: 0.02, d7: -0.01, d30: 0.0,
    volume24h: 8_700_000, marketCap: 50_000_000,
    circulatingSupply: 50_000_000, totalSupply: 50_000_000,
    spark: [0.9999, 1.0, 1.0001, 1.0, 0.9998, 1.0001, 1.0001],
    confidence: 99,
    sources: [
      { name: 'sdex', weight: 0.40 },
      { name: 'soroswap', weight: 0.25 },
      { name: 'phoenix', weight: 0.20 },
      { name: 'binance', weight: 0.15 },
    ],
  },
  {
    rank: 4, rankDelta: 0, type: 'soroban',
    slug: 'blnd', ticker: 'BLND', name: 'Blend',
    description: 'Governance + utility token for the Blend lending protocol on Soroban.',
    issuer: 'GAUTUYY7CHRBFPSAKPSESK5KZWWQGXYY7XKEJ44GD4M5NBHKR75GBLND',
    homeDomain: 'blend.capital',
    price: 0.0823, h1: -0.4, h24: -2.1, d7: -7.8, d30: -12.3,
    volume24h: 312_000, marketCap: 41_200_000,
    circulatingSupply: 500_000_000, totalSupply: 1_000_000_000,
    spark: [0.092, 0.09, 0.087, 0.085, 0.083, 0.084, 0.0823],
    confidence: 71,
    sources: [
      { name: 'soroswap', weight: 0.55 },
      { name: 'sdex', weight: 0.30 },
      { name: 'phoenix', weight: 0.15 },
    ],
  },
  {
    rank: 5, rankDelta: 2, type: 'classic',
    slug: 'eurc', ticker: 'EURC', name: 'Euro Coin',
    description: 'Centre-issued EUR-pegged stablecoin on Stellar.',
    issuer: 'GAP5LETOV6YIE62YAM56STDANPRDO7ZFDBGSNHJQIYGGKSMOZAHOOS2S',
    homeDomain: 'centre.io',
    price: 1.0805, h1: 0.0, h24: 0.1, d7: 0.4, d30: 1.2,
    volume24h: 521_000, marketCap: 12_800_000,
    circulatingSupply: 12_800_000, totalSupply: 12_800_000,
    spark: [1.076, 1.077, 1.078, 1.079, 1.08, 1.081, 1.0805],
    confidence: 98,
    sources: [
      { name: 'sdex', weight: 0.50 },
      { name: 'soroswap', weight: 0.30 },
      { name: 'kraken', weight: 0.20 },
    ],
  },
  {
    rank: 6, rankDelta: -1, type: 'soroban',
    slug: 'yxlm', ticker: 'yXLM', name: 'yXLM (yieldswap)',
    description: 'Yield-bearing XLM wrapper from yieldswap.',
    homeDomain: 'yieldswap.fi',
    price: 1.044, h1: 0.0, h24: 0.5, d7: 1.8, d30: 3.5,
    volume24h: 92_000, marketCap: 8_300_000,
    circulatingSupply: 8_300_000, totalSupply: 8_300_000,
    spark: [1.024, 1.029, 1.033, 1.038, 1.041, 1.042, 1.044],
    confidence: 64,
    sources: [
      { name: 'soroswap', weight: 0.70 },
      { name: 'phoenix', weight: 0.30 },
    ],
  },
  {
    rank: 7, rankDelta: 0, type: 'classic',
    slug: 'mxne', ticker: 'MXNe', name: 'Mexican Peso (Bitso)',
    description: 'Bitso-issued MXN-pegged stablecoin on Stellar.',
    issuer: 'GBJSXLIRDM3XYKCXX2DVKMNVRPXCQRSAAGSDC4LUQ4GIIUFHVOH7SYRM',
    homeDomain: 'bitso.com',
    price: 0.0589, h1: 0.0, h24: -0.1, d7: 0.2, d30: -0.5,
    volume24h: 45_000, marketCap: 2_100_000,
    circulatingSupply: 2_100_000, totalSupply: 2_100_000,
    spark: [0.0588, 0.059, 0.0589, 0.0591, 0.0588, 0.0589, 0.0589],
    confidence: 88,
    sources: [
      { name: 'sdex', weight: 0.65 },
      { name: 'soroswap', weight: 0.35 },
    ],
  },
];

/**
 * synthesizeCoin returns a minimal seeded shape for a slug that
 * isn't in `SEED_COINS`. Used by `/coins/[slug]` so newly-observed
 * Stellar assets render a usable detail page even though the design-
 * iteration seed only covered a handful. Live panels (chart,
 * issuer, change-summary) all fetch their own data based on slug,
 * so they don't need anything else from the seed.
 *
 * The price / volume / marketCap fields are zeroed; the page guards
 * against zero-price rendering by hiding those panels when the
 * canonical metadata is the slug-derived placeholder.
 */
export function synthesizeCoin(slug: string): SeedCoin {
  return {
    rank: 0,
    rankDelta: 0,
    type: 'classic',
    slug,
    ticker: slug,
    name: slug,
    description: undefined,
    issuer: undefined,
    homeDomain: undefined,
    price: 0,
    h1: 0,
    h24: 0,
    d7: 0,
    d30: 0,
    volume24h: 0,
    marketCap: 0,
    circulatingSupply: 0,
    totalSupply: 0,
    spark: [],
    confidence: 0,
    sources: [],
  };
}

export function findCoin(slug: string): SeedCoin | undefined {
  // Case-insensitive — live API slugs are mixed-case (USDC, yXLM)
  // while the seed slugs were authored lowercase. Either form
  // resolves to the same SeedCoin row.
  const lower = slug.toLowerCase();
  return SEED_COINS.find((c) => c.slug.toLowerCase() === lower);
}
