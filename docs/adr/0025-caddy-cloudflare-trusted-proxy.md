---
adr: 0025
title: Caddy trusts Cloudflare for client-IP signal via CIDR-pinned static list
status: Accepted
date: 2026-05-10
supersedes: []
superseded_by: null
---

# ADR-0025: Caddy trusts Cloudflare for client-IP signal via CIDR-pinned static list

## Context

R1's reverse-proxy topology is `Cloudflare edge → Caddy (origin
TLS terminator) → ratesengine-api (localhost:3000)`. Customers
hit `https://api.ratesengine.net`, which resolves to a CF
anycast IP; CF terminates TLS at the edge then proxies to
Caddy on r1's `:443`; Caddy terminates again locally and
proxies to the API on `:3000`.

The bug we caught on 2026-05-10 (PR #1239): Caddy's
`reverse_proxy` directive was setting

```
header_up X-Forwarded-For {remote_host}
```

which OVERWRITES `X-Forwarded-For` with the immediate TCP
peer's IP — a CF edge POP, not the customer. The API's
`api.trusted_proxy_cidrs = ["127.0.0.1/32"]` config then
trusts Caddy's downstream value, so every API access-log line
shows `remote_ip = <CF edge>` and every per-IP rate-limit
bucket aggregates traffic from that CF edge across all
customers behind it. Per-IP rate limiting was therefore
broken — one bursty customer behind a CF edge could trip the
limit and block every other customer routed through that
same POP.

We need the API to see the **real customer IP** for both
access logging and rate limiting. CF publishes the canonical
real-client IP in `CF-Connecting-IP` (and a chain in
`X-Forwarded-For` whose left-most entry is the same). The
naive fix — "trust `CF-Connecting-IP` unconditionally" — is
unsafe: any actor able to reach r1's `:443` directly (bypassing
CF's anycast IP via the box's underlying IP) could spoof their
client IP by setting that header. We need conditional trust.

Options for "the immediate peer is actually CF":

1. Caddy's third-party `caddy-cloudflare-ip` plugin — auto-
   fetches CF's IP ranges and refreshes them periodically.
   Requires an `xcaddy` build (the apt-shipped Caddy on r1 is
   the stock build, no plugin).
2. Static CIDR list in the Caddyfile, refreshed manually on
   audit cadence.
3. Per-region firewall (allow `:443` only from CF IP ranges).
4. Trust X-Forwarded-For chain with a per-hop CIDR check
   (Caddy's built-in `trusted_proxies` directive).

Cloudflare's published IP ranges
(`https://www.cloudflare.com/ips-v4` + `ips-v6`) change rarely
— typically zero updates per quarter, occasional additions for
new POPs. CF promises stability and posts notices for breaking
changes.

## Decision

Caddy's global config sets `servers { trusted_proxies static
<CF IPv4 + IPv6 CIDRs>; client_ip_headers CF-Connecting-IP
X-Forwarded-For }`. The reverse_proxy directive forwards
`{client_ip}` (Caddy's resolved real-client IP after
trusted-proxy validation) instead of `{remote_host}` (the
immediate TCP peer).

Trust is **CIDR-pinned to CF's published ranges**, baked into
the Caddyfile under source control. The API's
`trusted_proxy_cidrs = ["127.0.0.1/32"]` config stays the same
— Caddy is still the only proxy the API trusts; what changes
is that Caddy's downstream `X-Forwarded-For` now carries the
real client IP rather than the CF edge IP.

CF range refresh is **manual quarterly** — the in-repo Caddy
README documents the curl commands and the audit cadence. We
do not adopt the third-party plugin because (a) installing it
requires an `xcaddy` rebuild that adds a tooling dependency
and a per-deploy compile step, and (b) the manual refresh
cadence is cheap (single curl + diff per quarter).

## Consequences

- **Positive:** Per-IP rate limiting works as documented —
  per-customer buckets, not per-CF-edge buckets. Access logs
  show real customer IPs (useful for abuse triage,
  incident-response correlation, and answering customer
  "did my call hit you" questions). The same Caddyfile shape
  works for R2 / R3 when they ship.
- **Negative:** CF range list lives in source control and
  needs manual refresh. We accept the staleness risk because
  CF's range churn is low; a missed refresh manifests as a
  customer occasionally seeing their IP show up as a CF edge
  IP — degrading rate-limit accuracy for that single
  customer-edge pair, not breaking anything.
- **Operational impact:** Quarterly task on the operator
  rotation: re-fetch CF ranges, diff, update Caddyfile if
  needed, deploy. Documented in `configs/caddy/README.md`.
  CF-published advisory of an upcoming range change should
  trigger an out-of-cycle refresh.
- **Downstream design impact:** R2 / R3 inherit the same
  topology — CF edge → Caddy → API per region. The Ansible
  role that ships Caddy carries the same `trusted_proxies
  static` block. If we ever expose the API directly (without
  CF in front, e.g. for an internal probe path or a SEP-10
  customer that can't traverse CF), the operator MUST
  delete the `trusted_proxies` block on that listener —
  otherwise an attacker can spoof their client IP. The
  Caddyfile comment + this ADR call this out.

## Alternatives considered

1. **`caddy-cloudflare-ip` third-party plugin (auto-refresh)**
   — rejected because it requires an `xcaddy` rebuild on every
   Caddy version bump, adding a tooling dependency for
   marginal benefit (CF range churn is low; manual quarterly
   refresh costs minutes per quarter). Reconsider if CF starts
   churning ranges more often, or if we move to a Docker
   image build pipeline that already runs `xcaddy`.
2. **Per-region firewall (only allow `:443` from CF ranges)**
   — rejected as defence-only. It blocks direct-IP probes
   (good) but doesn't help Caddy resolve the real client IP
   (the CF-Connecting-IP header still needs to be honoured
   conditionally). Worth doing in addition, but not instead.
   Tracked separately as a hardening follow-up.
3. **Trust CF-Connecting-IP unconditionally** — rejected
   because it lets any actor reaching r1 directly spoof their
   client IP. The trust must be conditional on the immediate
   peer being CF.
4. **Move TLS termination to CF only (no Caddy at the origin)**
   — rejected because CF's "Full (strict)" TLS mode requires
   a valid origin certificate, and we want auto-renewal at
   the origin too (so a single misconfigured CF setting
   doesn't strand us with HTTP-only origin traffic). Caddy's
   Let's Encrypt automation gives us that with no operator
   ceremony.

## References

- Related ADRs:
  - [ADR-0008](0008-ha-topology.md) — overall HA topology
    that this slots into.
  - [ADR-0015](0015-last-closed-bucket-rate-serving.md) —
    cross-region byte-identical contract that depends on
    accurate per-region rate limiting (which this ADR
    restores).
- Configs:
  - `configs/caddy/Caddyfile.api` — the live config that
    embodies this decision.
  - `configs/caddy/README.md` — operator-facing summary
    including the quarterly CIDR-refresh procedure.
- External references:
  - <https://www.cloudflare.com/ips-v4> + `ips-v6` — the
    canonical IP-range list to refresh from.
  - <https://developers.cloudflare.com/fundamentals/reference/http-headers/> —
    `CF-Connecting-IP` documentation.
  - <https://caddyserver.com/docs/caddyfile/options#trusted-proxies>
    — Caddy v2.7+ `trusted_proxies` directive reference.
- Implementation: PR #1239 — `fix(caddy): resolve real client
  IP from Cloudflare edge`.
