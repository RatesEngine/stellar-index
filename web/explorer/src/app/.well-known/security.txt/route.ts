// /.well-known/security.txt — RFC-9116 disclosure metadata.
//
// Generated at build time from values that mirror the SECURITY.md
// policy. The Expires field is stamped at one year from the build
// date so it stays valid as long as we rebuild and redeploy in
// that window (CF Pages rebuilds on every push to main; status
// site rebuilds with the explorer).

import { NextResponse } from 'next/server';

export const dynamic = 'force-static';

const SITE_URL = 'https://ratesengine.net';

export function GET() {
  const now = new Date();
  const expires = new Date(now);
  expires.setUTCFullYear(now.getUTCFullYear() + 1);

  const lines = [
    `# Rates Engine — security.txt`,
    `# RFC-9116. Mirrors ${SITE_URL}/research → SECURITY.md.`,
    ``,
    `Contact: mailto:security@ratesengine.net`,
    `Expires: ${expires.toISOString()}`,
    `Preferred-Languages: en`,
    `Canonical: ${SITE_URL}/.well-known/security.txt`,
    `Policy: https://github.com/RatesEngine/rates-engine/blob/main/SECURITY.md`,
    `Acknowledgments: https://github.com/RatesEngine/rates-engine/security/advisories`,
    ``,
  ].join('\n');

  return new NextResponse(lines, {
    headers: {
      'content-type': 'text/plain; charset=utf-8',
      'cache-control': 'public, max-age=86400',
    },
  });
}
