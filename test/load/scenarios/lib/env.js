// Shared env / auth helpers for every k6 scenario.
//
// Operators export K6_TARGET (the API base URL, including /v1) and
// RATESENGINE_LOAD_API_KEY (the load-test API key, minted from
// vault) before running k6. Scenarios import baseUrl + apiKey and
// must NEVER hard-code these values.
//
// The Makefile target refuses to run if K6_TARGET resolves to a
// production hostname; this module enforces the same guard at
// scenario-init time so a direct `k6 run` against a misconfigured
// shell still aborts before the first request.

const ENV = (typeof __ENV !== 'undefined') ? __ENV : {};

export const baseUrl = (ENV.K6_TARGET || '').replace(/\/$/, '');
export const apiKey = ENV.RATESENGINE_LOAD_API_KEY || '';

const PROD_HOSTS = [
  'api.ratesengine.net',
  'api.ratesengine.io',
  'rates.stellar.org',
];

if (!baseUrl) {
  throw new Error(
    'K6_TARGET is required (e.g. https://api.staging.ratesengine.net/v1). ' +
    'Export it before running k6.',
  );
}
if (!apiKey) {
  throw new Error(
    'RATESENGINE_LOAD_API_KEY is required. Export the load-test ' +
    'API key (mint from vault) before running k6.',
  );
}
for (const h of PROD_HOSTS) {
  if (baseUrl.includes(h)) {
    throw new Error(
      `Refusing to load-test production target ${baseUrl}. ` +
      'Point K6_TARGET at a staging host.',
    );
  }
}

export const headers = {
  'X-API-Key': apiKey,
  'User-Agent': 'ratesengine-k6/1.0',
};
