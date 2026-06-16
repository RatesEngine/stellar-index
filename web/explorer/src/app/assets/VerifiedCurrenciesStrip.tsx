import Link from 'next/link';

import { API_BASE_URL } from '@/api/client';
import { Badge, SectionHeader } from '@/components/ui';

/**
 * Mirror of `VerifiedCurrencyListItem` on the wire.
 * See `internal/api/v1/assets_global.go`.
 */
export interface VerifiedItem {
  ticker: string;
  slug: string;
  name: string;
  class?: 'crypto' | 'stablecoin' | 'fiat';
  verified_issuer?: string;
  // market_cap_usd is populated for fiat rows by /v1/assets/verified
  // (R-018 assets-unification step 5). Decimal string with 2
  // fractional digits. Empty for crypto/stablecoin rows.
  market_cap_usd?: string;
}

// CI builds use a stub hostname that doesn't resolve; bypass the
// network fetch in that case so static export doesn't time out.
const isCIStub =
  API_BASE_URL.includes('.invalid') || API_BASE_URL.includes('local-stub');

const BUILD_FETCH_TIMEOUT_MS = 8_000;

/**
 * fetchVerifiedCurrencies is the shared `/v1/assets/verified`
 * fetcher consumed by both this strip and the AssetsTable. Single
 * server-side fetch per page render — the page calls this once,
 * passes the result to both components as a prop.
 */
export async function fetchVerifiedCurrencies(): Promise<VerifiedItem[]> {
  if (isCIStub) return [];
  try {
    const res = await fetch(`${API_BASE_URL}/v1/assets/verified`, {
      signal: AbortSignal.timeout(BUILD_FETCH_TIMEOUT_MS),
    });
    if (!res.ok) return [];
    const env = (await res.json()) as { data?: VerifiedItem[] };
    return env.data ?? [];
  } catch {
    return [];
  }
}

/**
 * VerifiedCurrenciesStrip renders a curated chip-row of every
 * verified currency in the catalogue at the top of `/assets`.
 *
 * Takes the pre-fetched verified list as a prop so the page-level
 * fetch can be shared with the AssetsTable (which uses the slug set
 * to mark rows with a verified badge). Empty array → returns null.
 *
 * Each chip links to `/assets/{slug}` — the global view added in
 * R-018 Phase 1.5.
 */
export function VerifiedCurrenciesStrip({
  verified,
}: {
  verified: VerifiedItem[];
}) {
  if (verified.length === 0) return null;

  // Sort: rows with a market_cap_usd descending (CNY first, etc.),
  // then rows without a cap (crypto/stablecoin) in their original
  // seed order. Big-number-safe via lexicographic comparison after
  // left-padding — both strings are decimals with the same number
  // of fractional digits, so character-by-character compare works
  // up to the integer-part length. For our M2 figures (≤ 15-digit
  // integer parts) this is exact without parseFloat losing precision.
  const sorted = [...verified].sort((a, b) => {
    const ac = a.market_cap_usd ?? '';
    const bc = b.market_cap_usd ?? '';
    if (ac === '' && bc === '') return 0;
    if (ac === '') return 1; // empties last
    if (bc === '') return -1;
    return compareDecimalDesc(ac, bc);
  });

  return (
    <section className="space-y-4">
      <SectionHeader
        title="Verified currencies"
        actions={
          <Badge tone="ok" dot>
            {verified.length} verified
          </Badge>
        }
      />
      <div className="flex flex-wrap gap-2">
        {sorted.map((vc) => (
          <Link
            key={vc.slug}
            href={`/assets/${vc.slug}`}
            className="group inline-flex items-center gap-2 rounded-md border border-up/30 bg-surface px-3 py-1.5 text-sm font-medium text-ink transition hover:border-up/40 hover:bg-up-subtle"
            title={
              vc.verified_issuer
                ? `${vc.name} — ${vc.verified_issuer}`
                : vc.name
            }
          >
            <svg
              xmlns="http://www.w3.org/2000/svg"
              viewBox="0 0 20 20"
              fill="currentColor"
              className="h-3.5 w-3.5 text-up"
              aria-hidden="true"
            >
              <path
                fillRule="evenodd"
                d="M10 18a8 8 0 100-16 8 8 0 000 16zm3.707-9.293a1 1 0 00-1.414-1.414L9 10.586 7.707 9.293a1 1 0 00-1.414 1.414l2 2a1 1 0 001.414 0l4-4z"
                clipRule="evenodd"
              />
            </svg>
            <span>{vc.ticker}</span>
          </Link>
        ))}
      </div>
    </section>
  );
}

/**
 * compareDecimalDesc compares two decimal strings (e.g.
 * "42280000000000.00" and "21700000000000.00") and returns a
 * comparator-style number for descending sort.
 *
 * Works exactly for our M2 figures (15-digit integer parts) without
 * the float64 precision loss that `parseFloat(...)` would incur.
 * Strategy: compare integer-part length first (longer = bigger);
 * tie-break with character-by-character comparison.
 */
function compareDecimalDesc(a: string, b: string): number {
  const [aInt = '', aFrac = ''] = a.split('.');
  const [bInt = '', bFrac = ''] = b.split('.');
  // Strip any leading zeros so "00042" vs "42" both compare as "42".
  const aIntTrim = aInt.replace(/^0+/, '') || '0';
  const bIntTrim = bInt.replace(/^0+/, '') || '0';
  if (aIntTrim.length !== bIntTrim.length) {
    return bIntTrim.length - aIntTrim.length;
  }
  if (aIntTrim !== bIntTrim) {
    return aIntTrim < bIntTrim ? 1 : -1;
  }
  // Same integer part: compare fractional. Right-pad to equal length.
  const maxLen = Math.max(aFrac.length, bFrac.length);
  const af = aFrac.padEnd(maxLen, '0');
  const bf = bFrac.padEnd(maxLen, '0');
  if (af === bf) return 0;
  return af < bf ? 1 : -1;
}
