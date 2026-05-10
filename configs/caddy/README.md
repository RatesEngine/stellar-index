# Caddy reverse proxy — configs

## Files

- `Caddyfile.api` — fronts `api.ratesengine.net` → `localhost:3000`. Auto-HTTPS via Let's Encrypt's tls-alpn-01 challenge. Used on R1 today.

## Why Caddy (vs HAProxy / Nginx / Cloudflare)

For r1's pre-multi-region shape:

- **Auto-HTTPS out of the box.** No certbot cron, no renewal scripts, no "the cert expired and nobody noticed" postmortem.
- **Single static binary, single config file.** Operator hand-off is one paste.
- **HTTP/3 + HTTP/2 + TLS 1.3** without per-feature flags.
- **Active health-checks** on the upstream — drops `localhost:3000` from rotation if `/v1/healthz` starts returning non-200, which is the right signal for the wedged-listener-but-systemd-active failure mode.

When we add R2 / R3, Cloudflare in front of all three regions becomes the right layer for **DDoS protection + WAF + multi-region GeoIP routing**. Caddy stays as the per-region origin TLS terminator — Cloudflare → Caddy → API. The Caddyfile shape doesn't change between "naked Caddy" today and "Caddy behind Cloudflare" tomorrow; we'd just trust additional X-Forwarded-* CIDRs (Cloudflare's published edge ranges) in the API config.

## Operator install (R1)

Caddy is already installed + running on R1 as of 2026-05-05. To re-install on a fresh box:

```sh
# Caddy stable APT repo
apt-get install -y debian-keyring debian-archive-keyring apt-transport-https curl
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/gpg.key' \
  | gpg --dearmor -o /usr/share/keyrings/caddy-stable-archive-keyring.gpg
curl -1sLf 'https://dl.cloudsmith.io/public/caddy/stable/debian.deb.txt' \
  > /etc/apt/sources.list.d/caddy-stable.list
apt-get update
apt-get install -y caddy

# Drop the Caddyfile + reload
cp configs/caddy/Caddyfile.api /etc/caddy/Caddyfile
caddy validate --config /etc/caddy/Caddyfile
systemctl reload caddy
```

## API-side wiring

The API needs `trusted_proxy_cidrs = ["127.0.0.1/32"]` in `/etc/ratesengine.toml`'s `[api]` section so it honours Caddy's `X-Forwarded-*` headers (without that, request-logger + rate-limit middleware key on `127.0.0.1` for every request — a real correctness bug for per-client rate limiting). R1 has this set as of 2026-05-05.

## Real client IP under Cloudflare

The Caddyfile's global `servers { trusted_proxies static <CF CIDRs>; client_ip_headers CF-Connecting-IP X-Forwarded-For }` block resolves the real client IP from CF's edge headers and propagates it down to the API as `X-Forwarded-For: {client_ip}`. This is mandatory for any CF-fronted deployment — without it, every request looks like it's coming from a CF edge POP, breaking per-IP rate-limit buckets and access logs (a single CF edge hitting the burst threshold blocks every customer behind it).

The `trusted_proxies static <list>` form needs the CF IP ranges baked into the Caddyfile because we don't have the third-party `caddy-cloudflare-ip` plugin installed (would require an `xcaddy` rebuild). CF's published ranges change rarely; refresh on quarterly audits or when CF publishes a notice via:

```sh
curl -sS https://www.cloudflare.com/ips-v4 https://www.cloudflare.com/ips-v6
```

If we ever move Caddy to a non-CF-fronted box (direct internet exposure), DELETE the `trusted_proxies` block — leaving it would let an attacker spoof their IP by setting `CF-Connecting-IP` directly.

## DNS prerequisites for HTTPS

Auto-HTTPS via Let's Encrypt requires:

1. `api.ratesengine.net` A record → `136.243.90.96` (or AAAA for IPv6)
2. Ports 80 and 443 reachable from the public internet
3. The DNS lookup completes globally (≤ 1 hour after creation; usually faster)

Until the A record exists, Caddy retries acquisition every few minutes and logs `NXDOMAIN looking up A for api.ratesengine.net`. The `:443` listener is up and Caddy serves a self-signed fallback cert for testing on the IP directly. Once the DNS lands, the next ACME retry succeeds and the cert is auto-served from then on (Caddy renews automatically at 1/3-of-lifetime).

## Smoke tests

After deploy + DNS landing:

```sh
# Public TLS
curl -sf https://api.ratesengine.net/v1/healthz             # 200
curl -sI https://api.ratesengine.net/v1/healthz | grep -i strict-transport
# Should see: Strict-Transport-Security: max-age=31536000; includeSubDomains

# HTTP redirects to HTTPS
curl -sI http://api.ratesengine.net/v1/healthz | head -1
# Should see: HTTP/1.1 308 Permanent Redirect

# Active health-check working — kill the API and curl should 502
sudo systemctl stop ratesengine-api
sleep 15
curl -sI https://api.ratesengine.net/v1/healthz | head -1   # 502
sudo systemctl start ratesengine-api
```

## Future: showcase

The `Caddyfile.api` ships a commented-out `ratesengine.net` block ready to serve the static Next.js export from `/var/www/showcase` if/when the showcase moves off Cloudflare Pages. Today the showcase is Cloudflare Pages so the block stays inert.
