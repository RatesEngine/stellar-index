import { ImageResponse } from 'workers-og';

// Dynamic OG card generator (SEO plan D7). GET /og/{type}/{id} → a 1200×630 PNG
// rendered with satori + resvg-wasm, edge-cached. v1 is a branded, readable
// entity card; live-data enrichment (price/balance) + shell head-injection are
// documented follow-ups. All flagship pages' og:image point here.

// Asset code from a canonical id: "USDC-GA5Z…" → "USDC", "native" → "XLM",
// catalogue slug ("usdc") → "USDC".
function code(s) {
  if (!s) return '';
  if (s === 'native') return 'XLM';
  const dash = s.indexOf('-');
  return (dash > 0 ? s.slice(0, dash) : s).toUpperCase();
}

function prettyLabel(type, id) {
  if (!id) return null;
  if (type === 'markets' && id.includes('~')) {
    const [b, q] = id.split('~');
    return `${code(b)} / ${code(q)}`;
  }
  if (type === 'assets') return code(id);
  // tx / ledger / account / contract ids — truncate long strkeys/hashes.
  return id.length > 24 ? `${id.slice(0, 10)}…${id.slice(-8)}` : id;
}

const TYPE_LABEL = {
  markets: 'Market',
  assets: 'Asset',
  transactions: 'Transaction',
  ledgers: 'Ledger',
  accounts: 'Account',
  contracts: 'Contract',
  protocols: 'Protocol',
};

export async function onRequest(context) {
  const { request } = context;
  const url = new URL(request.url);
  const parts = url.pathname.replace(/^\/og\/?/, '').split('/').filter(Boolean);
  const type = (parts[0] || 'home').replace(/[^a-z0-9-]/gi, '');
  const rawId = decodeURIComponent(parts.slice(1).join('/') || '');
  const label = prettyLabel(type, rawId) || 'Stellar pricing & protocol explorer';
  const kicker = TYPE_LABEL[type] ? `Stellar Index · ${TYPE_LABEL[type]}` : 'Stellar Index';

  const html = `
    <div style="display:flex;flex-direction:column;justify-content:space-between;width:1200px;height:630px;background:#0b0f1a;color:#ffffff;padding:84px;font-family:sans-serif;">
      <div style="display:flex;font-size:30px;color:#7aa2ff;font-weight:600;">${kicker}</div>
      <div style="display:flex;font-size:76px;font-weight:700;line-height:1.05;">${label}</div>
      <div style="display:flex;font-size:26px;color:#8a93a6;">stellarindex.io</div>
    </div>`;

  return new ImageResponse(html, {
    width: 1200,
    height: 630,
    headers: { 'cache-control': 'public, s-maxage=60, stale-while-revalidate=300' },
  });
}
