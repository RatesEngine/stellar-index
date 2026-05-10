'use client';

import { useIssuerLookup, useSACWrappers } from '@/api/hooks';

/**
 * AssetLabel — single shared component for rendering a canonical
 * asset string compactly across the explorer. Handles every form
 * the v1 API emits:
 *
 *   - `native`              → "XLM"
 *   - numeric ("0", "1")    → "XLM-native" (legacy markets-table form)
 *   - `fiat:USD`            → "USD"
 *   - `crypto:XLM`          → "XLM"
 *   - `C…` (55-char SAC)    → resolved via /v1/sac-wrappers when the
 *                             operator has populated the map; falls
 *                             back to truncated C-strkey when the map
 *                             returns no entry.
 *   - `<CODE>-<G-strkey>`   → CODE prominent, issuer truncated
 *
 * Centralised here (was previously copy-pasted into 5 view files)
 * so SAC resolution lands everywhere with a single edit and any
 * future canonical-form addition (e.g. `lp:…`) only needs to be
 * handled in one place.
 */
export function AssetLabel({
  canonical,
}: {
  canonical: string | undefined | null;
}) {
  const { data: sacMap } = useSACWrappers();
  const { data: issuerMap } = useIssuerLookup();

  if (!canonical) return <span className="text-xs text-slate-400">—</span>;
  if (canonical === 'native')
    return <span className="font-medium">XLM</span>;
  if (canonical.startsWith('fiat:')) {
    return <span className="font-medium">{canonical.replace('fiat:', '')}</span>;
  }
  if (canonical.startsWith('crypto:')) {
    return <span className="font-medium">{canonical.replace('crypto:', '')}</span>;
  }
  // Numeric form ("0", "1", …) is the legacy markets-table native render.
  if (/^\d+$/.test(canonical)) {
    return <span className="font-medium">XLM-native</span>;
  }
  // SAC contract — try to resolve via the operator-config map.
  // Stellar contract IDs are 56 chars (C + 55 alphanumerics);
  // accept upper- or lower-case to be defensive in case a decoder
  // emits a non-canonical-cased fingerprint.
  if (/^C[A-Za-z0-9]{55}$/.test(canonical)) {
    // The native XLM SAC is intentionally absent from the operator
    // wrapper map (configs/ansible/.../ratesengine.toml.j2 — it isn't
    // a wrapper of a classic asset and the on-chain usd_volume
    // validator rejects mapping it). Hardcode the well-known C-strkey
    // here so Soroban DEX rows that emit XLM as base/quote render
    // "XLM" instead of a truncated SAC fingerprint.
    if (canonical === 'CAS3J7GYLGXMF6TDJBBYYSE3HQ6BBSMLNUQ34T6TZMYMW2EVH34XOWMA') {
      return (
        <div>
          <div className="font-medium">XLM</div>
          <div className="text-[10px] uppercase tracking-wide text-slate-500">
            SAC
          </div>
        </div>
      );
    }
    const resolved = sacMap?.[canonical];
    if (resolved === 'native') {
      return (
        <div>
          <div className="font-medium">XLM</div>
          <div className="text-[10px] uppercase tracking-wide text-slate-500">
            SAC
          </div>
        </div>
      );
    }
    if (resolved) {
      const dashIx = resolved.indexOf('-');
      const code = dashIx === -1 ? resolved : resolved.slice(0, dashIx);
      return (
        <div>
          <div className="font-medium">{code}</div>
          <div className="text-[10px] uppercase tracking-wide text-slate-500">
            SAC
          </div>
        </div>
      );
    }
    // Unresolved SAC — truncate the C-strkey and tooltip the full value.
    return (
      <span className="font-mono text-[11px]" title={canonical}>
        {canonical.slice(0, 6)}…{canonical.slice(-4)}
      </span>
    );
  }
  // Classic credit asset: <CODE>-<G-issuer>.
  const dashIx = canonical.indexOf('-');
  if (dashIx === -1) {
    // Unstructured fallback — anything longer than a sensible asset
    // code gets truncated with the full string in the tooltip so
    // table rows stay readable. Shorter strings render verbatim.
    if (canonical.length > 16) {
      return (
        <span className="font-mono text-[11px]" title={canonical}>
          {canonical.slice(0, 8)}…{canonical.slice(-4)}
        </span>
      );
    }
    return <span className="font-mono text-xs">{canonical}</span>;
  }
  const code = canonical.slice(0, dashIx);
  const issuer = canonical.slice(dashIx + 1);
  // When we know the issuer's organisation, render the org name
  // as the subtitle (e.g. "USDC / Circle") instead of the raw
  // truncated G-strkey. The issuer's full G-strkey stays in the
  // tooltip so power users can still copy it.
  const known = issuerMap?.[issuer];
  if (known?.org_name) {
    return (
      <div>
        <div className="font-medium">{code}</div>
        <div
          className="text-[10px] text-slate-500"
          title={issuer}
        >
          by {known.org_name}
        </div>
      </div>
    );
  }
  return (
    <div>
      <div className="font-medium">{code}</div>
      <div
        className="font-mono text-[10px] text-slate-500"
        title={issuer}
      >
        {issuer.length > 12 ? `${issuer.slice(0, 6)}…${issuer.slice(-4)}` : issuer}
      </div>
    </div>
  );
}
