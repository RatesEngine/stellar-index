---
title: Pre-launch hardening checklist
last_verified: 2026-05-05
status: operator runbook
---

# Pre-launch hardening checklist

Run through this BEFORE flipping public DNS at
`api.ratesengine.net` / `ratesengine.net`. Each item is a config
edit + service restart on R1; combined effort ~10 minutes.

The API binary at startup now logs `SECURITY:` warnings when any
of these are still in their dev-friendly defaults — checking
`journalctl -u ratesengine-api -b -p warning | grep SECURITY` is
the canonical way to verify nothing's missed.

## 1. Bind the API to loopback

**Why:** the API listens on `0.0.0.0:3000` by default — every
request goes through it including raw `http://<R1-IP>:3000` that
bypasses Caddy's TLS. Once DNS lands, customers expect TLS-only;
the loopback bind makes Caddy mandatory.

**Config — append to `[api]` block in `/etc/ratesengine.toml`:**

```toml
[api]
listen_addr = "127.0.0.1:3000"
```

**Apply:**

```sh
systemctl restart ratesengine-api
ss -tlnp | grep ratesengine-api    # should show 127.0.0.1:3000, not *:3000
```

**Verify** the loopback restriction holds end-to-end:

```sh
# From R1 (loopback) — works:
curl -fsS http://localhost:3000/v1/healthz

# From the public internet (replace with your IP) — should now refuse:
curl --connect-timeout 3 http://136.243.90.96:3000/v1/healthz   # connection refused

# Through Caddy (TLS) — works post-DNS:
curl -fsS https://api.ratesengine.net/v1/healthz
```

Caddy is on the same host so loopback access from Caddy still
works; only external direct-port access dies.

## 2. Narrow CORS

**Why:** `[api].allowed_origins = ["*"]` lets any browser-side
origin call the API. With `auth_mode = apikey_optional` (R1's
current mode) that means a third-party site can phish a logged-in
user's bearer token. Restrict to the showcase + API hostnames.

**Config — replace `[api]` block:**

```toml
[api]
allowed_origins = [
  "https://ratesengine.net",
  "https://api.ratesengine.net",
]
```

If you preview the showcase via Cloudflare Pages preview URLs
(`<branch>.<projectslug>.pages.dev`), add specific preview
hostnames temporarily — wildcards aren't honoured by the CORS
middleware.

**Apply:** `systemctl restart ratesengine-api`.

## 3. Confirm trusted-proxy CIDRs match the proxy's source

**Why:** with the loopback bind, the only legitimate caller of
the API IS Caddy. `trusted_proxy_cidrs = ["127.0.0.1/32"]` is
already correct on R1 (Caddy is on the same host). If you later
move Caddy to a separate host or front R1 with Cloudflare's
proxy mode, the CIDRs need to expand to the proxy's source range
or `X-Forwarded-For` from the wider internet starts being trusted.

**No edit needed today** unless the proxy topology changes.

## 4. Cloudflare proxy in front of api.ratesengine.net (recommended)

**Why:** Caddy gives TLS termination but no L7 protection. R1
is one box; a sustained L7 flood saturates it. Cloudflare's
free tier in front of `api.ratesengine.net` gives WAF rules, rate
limiting at the edge, and IP-based bot blocking out of the box.

**Steps:**

1. In Cloudflare dashboard, set the `api` A record to
   `136.243.90.96` with the **orange cloud** (proxy) ON.
2. Cloudflare's edge IPs are now the immediate peer R1 sees.
   Add their range to `trusted_proxy_cidrs`:
   ```toml
   trusted_proxy_cidrs = [
     "127.0.0.1/32",
     # Cloudflare IPv4 ranges — fetch fresh from
     # https://www.cloudflare.com/ips-v4/
     "173.245.48.0/20",
     "103.21.244.0/22",
     "103.22.200.0/22",
     # … etc
   ]
   ```
3. (Optional) Cloudflare Origin Cert — replace Caddy's
   Let's Encrypt with a long-lived Cloudflare-issued cert so
   the connection from CF edge → R1 origin is authenticated.

## 5. Stripe webhook secret (if launching paid tiers day 1)

**Config — `/etc/default/ratesengine`:**

```sh
RATESENGINE_STRIPE_WEBHOOK_SECRET=whsec_xxxxxxxxxxxxxxxxxxxxxxxxxxxx
```

(Sourced into the systemd unit via `EnvironmentFile=`.)

Restart: `systemctl restart ratesengine-api`. Verify via Stripe's
"Send test event" button → should land in `journalctl -u
ratesengine-api` and apply the upgrade.

## 6. Healthchecks.io URLs

Four URLs go into `/etc/default/ratesengine-healthchecks`:

```sh
HEALTHCHECKS_URL_INDEXER='https://hc-ping.com/<uuid-indexer>'
HEALTHCHECKS_URL_AGGREGATOR='https://hc-ping.com/<uuid-aggregator>'
HEALTHCHECKS_URL_API='https://hc-ping.com/<uuid-api>'
HEALTHCHECKS_URL_SMOKE='https://hc-ping.com/<uuid-smoke>'
```

Plus the deadmansswitch URL into `/etc/default/alertmanager-secrets`:

```sh
HEALTHCHECKS_DEADMANSSWITCH_URL='https://hc-ping.com/<uuid-dms>'
SLACK_WEBHOOK_URL='https://hooks.slack.com/services/...'
```

Apply:

```sh
systemctl restart 'ratesengine-heartbeat@*.timer' ratesengine-smoke.timer
bash /opt/ratesengine/alertmanager/apply.sh
```

## 7. FX API keys (recommended, not blocking)

The 4 FX sources flagged "stopped" in `/v1/sources` are missing
operator-supplied API keys. Set in `[external.fx]` under
`/etc/ratesengine.toml`:

```toml
[external.fx]
openexchangerates_app_id = "<your-key>"
# … per the source-config docs
```

Restart the indexer. Aggregator picks them up on the next tick.

Without these, fiat divergence has fewer cross-checks (CoinGecko
+ Reflector still cover most cases). Not a launch blocker.

## 8. Smoke from the open internet

Once DNS lands and Caddy has its cert:

```sh
# From your laptop, NOT R1:
API_BASE_URL=https://api.ratesengine.net make smoke
```

13/13 green confirms TLS + DNS + cert + path are all healthy
end-to-end before customer traffic arrives.

## 9. Backup baseline

The launch-readiness backlog has `L4.16` for automated daily
Postgres dumps + MinIO snapshot replication; until that lands,
take a manual baseline:

```sh
# On R1:
pg_dump -h localhost -U ratesengine ratesengine | gzip \
  > /var/backups/ratesengine-baseline-$(date +%F).sql.gz
mc mirror /var/lib/galexie-archive remote-backup/galexie-archive-baseline-$(date +%F)
```

Copy off-host. Document where.

## Verification at the end

```sh
# No SECURITY warnings in last boot:
journalctl -u ratesengine-api -b -p warning | grep SECURITY    # empty == good

# ListenAddr is loopback:
ss -tlnp | grep ratesengine-api        # 127.0.0.1:3000

# Smoke from outside:
API_BASE_URL=https://api.ratesengine.net make smoke
```
