// AlertManager-silence helpers.
//
// The 99-spike scenario will legitimately trip
// `ratesengine_api_latency_p95_high` mid-spike. Without a silence,
// on-call gets paged for the planned spike — design note §6.
//
// silenceForRun posts a silence to AlertManager covering the
// expected run window plus a buffer; clearSilence removes it on
// teardown so a real post-run regression still pages.
//
// Both functions are no-ops when ALERTMANAGER_URL is unset, so
// scenarios run fine against a staging environment that doesn't
// have AlertManager wired (the silence is a courtesy, not a
// correctness invariant).

import http from 'k6/http';

const ENV = (typeof __ENV !== 'undefined') ? __ENV : {};
const url = (ENV.ALERTMANAGER_URL || '').replace(/\/$/, '');
const matchers = (ENV.ALERTMANAGER_SILENCE_MATCHERS ||
  'alertname=APIHighLatencyP95,alertname=APIHighErrorRate').split(',');
const author = ENV.ALERTMANAGER_SILENCE_AUTHOR || 'k6-load-test';

// silenceForRun creates a silence covering durationMinutes from
// now, returns the silence ID for later removal.
export function silenceForRun(durationMinutes, comment) {
  if (!url) return null;
  const startsAt = new Date().toISOString();
  const endsAt = new Date(Date.now() + durationMinutes * 60 * 1000).toISOString();

  const payload = {
    matchers: matchers.map((m) => {
      const [name, value] = m.split('=');
      return { name, value, isRegex: false };
    }),
    startsAt,
    endsAt,
    createdBy: author,
    comment: comment || 'planned k6 load test',
  };

  const r = http.post(`${url}/api/v2/silences`, JSON.stringify(payload), {
    headers: { 'Content-Type': 'application/json' },
    tags: { endpoint: 'alertmanager-silence' },
  });
  if (r.status !== 200) {
    console.warn(`alertmanager silence create returned ${r.status}: ${r.body}`);
    return null;
  }
  try {
    return JSON.parse(r.body).silenceID;
  } catch (e) {
    return null;
  }
}

export function clearSilence(silenceID) {
  if (!url || !silenceID) return;
  const r = http.del(`${url}/api/v2/silence/${silenceID}`, null, {
    tags: { endpoint: 'alertmanager-silence' },
  });
  if (r.status >= 400) {
    console.warn(`alertmanager silence delete ${silenceID} returned ${r.status}`);
  }
}
