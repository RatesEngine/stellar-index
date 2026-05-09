# external-poller-stale

**Alert:** `ratesengine_external_poller_stale`
**Severity:** ticket
**Component:** external-poller
**SLO impact:** divergence-detection blind spot; aggregated-source
sanity reads degraded.

## What it means

The named external poller (CoinGecko, CoinMarketCap, CryptoCompare,
ECB, ExchangeRatesAPI, PolygonForex, Binance, Coinbase, Kraken,
Bitstamp) has not produced a single successful `PollOnce` in the
last 30 minutes. Either the venue is rejecting our calls (auth /
rate-limit), the venue is down, or the network path is broken.

Companion alert `ratesengine_external_poller_error_rate_high` fires
at the *informational* tier when error rate > 50% sustained 15 min
— a softer "something's degrading" signal that doesn't yet block
data flow.

## Triage

1. **Identify the source.** The alert label `{{ $labels.source }}`
   names which poller (e.g. `coingecko`, `binance`).

2. **Check the indexer log on r1** (or the active region):

   ```sh
   ssh root@136.243.90.96 \
     'journalctl -u ratesengine-indexer --since "1 hour ago" \
       --no-pager | grep -E "poller error|poller stopping" | grep <source>'
   ```

3. **Decode the most recent error string.** Common patterns:

   | Error contains              | Cause                            | Action                              |
   |-----------------------------|----------------------------------|-------------------------------------|
   | `http 429`                  | rate-limited                     | provision a higher-tier API key     |
   | `http 401` / `http 403`     | auth failure                     | rotate / re-issue the API key       |
   | `http 5..`                  | venue outage                     | wait + verify upstream status page  |
   | `http: timeout`             | network slowness                 | check r1 → public network egress    |
   | `dial tcp: ... no route`    | DNS / IP-allowlist / firewall    | check r1 networking + ufw + DNS     |
   | `decode` / `unmarshal`      | venue API changed shape          | bug — patch the decoder, file PR    |

## Common scenarios + fixes

### CoinGecko throttled (post-2024 unauthenticated-tier tightening)

Symptom: error contains `http 429` repeated every minute.

Fix: register a free demo API key at
[coingecko.com/en/developers/dashboard](https://www.coingecko.com/en/developers/dashboard),
add `COINGECKO_DEMO_API_KEY=<key>` to the indexer's environment file
(usually `/etc/systemd/system/ratesengine-indexer.service.d/env.conf`
or `/etc/ratesengine.toml.env` depending on the systemd unit's
`EnvironmentFile=`), then:

```sh
systemctl daemon-reload
systemctl restart ratesengine-indexer
```

Verify on next startup the indexer log shows
`source=coingecko ... auth_mode=demo` (or `pro` if a paid key).

### Paid-tier API key expired

Symptom: error contains `http 401` or `http 403` *with* a key set in
env. Common for CMC / CryptoCompare on annual renewal.

Fix: renew/rotate the key in the venue's dashboard, update the env
file, restart the indexer.

### Venue outage

Symptom: error contains `http 5..` or `connection refused`.

Action: check the venue's status page. If confirmed down, just
wait — the alert will clear once the venue recovers. Capture the
incident in `docs/operations/incidents/` if outage > 1h.

## Verification (post-fix)

```sh
curl -s http://r1:9100/metrics \
  | grep -E 'ratesengine_external_poller_(polls|last_success).*<source>'
```

You should see:
- `ratesengine_external_poller_polls_total{source="<source>",outcome="success"}` incrementing
- `ratesengine_external_poller_last_success_unix{source="<source>"}` reflecting the recent poll

## Why this alert exists

Pre-2026-05-09 the only signal of a sustained-failing poller was a
WARN log per failed poll. A poller in steady-state failure (e.g.
CoinGecko 429s every 60s for 13 hours) was effectively invisible to
Prometheus — discovery required someone manually `journalctl`-ing
the indexer. The metric + alert close that gap.

See the launch incident report and PR #1139 (CoinGecko backoff +
demo-key support) and PR #1140 (this metric + alert) for the
discovery-path narrative.
