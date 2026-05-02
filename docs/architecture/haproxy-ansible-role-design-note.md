---
title: HAProxy ansible role — design note
last_verified: 2026-05-02
status: shipped (Task #72 / #82 — configs/ansible/roles/haproxy)
related:
  - docs/architecture/ha-plan.md §3.1 (api-tier topology)
  - docs/architecture/patroni-ansible-role-design-note.md (sister role)
  - docs/architecture/redis-sentinel-ansible-role-design-note.md (sister role)
  - docs/operations/runbooks/api-pod-down.md (the runbook this role makes work)
---

# HAProxy ansible role — design note

> Bootstraps the third launch-critical sub-role of Task #72 after
> Patroni (#344) and Redis Sentinel (#350). HAProxy is the
> api-tier load balancer fronting the `ratesengine-api` pool;
> keepalived provides the HA between two HAProxy hosts.

## Scope (decided)

- **In scope**: HAProxy + keepalived deployed to 2 hosts as the
  public-facing api-tier load balancer per ha-plan §3.1.
- **Out of scope**:
  - **PgBouncer** — the api↔db connection pooler. Lives in a
    separate `pgbouncer` role (follow-up; not blocking launch
    since the api binary's `pgxpool` provides per-pod pooling
    with the right tuning). PgBouncer adds a shared layer that
    benefits write coalescing and connection-storm protection,
    but the 3-pod api fleet at expected QPS doesn't need it for
    launch.
  - **HAProxy in front of Redis** — clients are Sentinel-aware;
    no LB needed (per ADR-0024).
  - **Cross-region routing** — single-region today; multi-region
    is post-launch (per ha-plan §11).

## Topology

Per ha-plan §3.1:

```
                    public DNS A record →  VIP
                                            │
                  ┌─────────────────────────┴─────────────────────────┐
                  │       keepalived (VRRP, BACKUP→MASTER on failure)  │
                  └─────────────────────────┬─────────────────────────┘
                                            │
                  ┌──────────────────────────┴──────────────────────────┐
                  │                                                     │
           ┌──────┴──────┐                                       ┌──────┴──────┐
           │  HAProxy-A  │   ←──── VRRP ────→                    │  HAProxy-B  │
           │   priority 100                                       │   priority 99
           └──────┬──────┘                                       └──────┬──────┘
                  │                                                     │
                  └─────────────────────────┬───────────────────────────┘
                                            │
                            ┌───────────────┴───────────────┐
                            │  ratesengine-api × 3 pods     │
                            │  (stateless; /readyz health)  │
                            └───────────────────────────────┘
```

- Two `lb-01` / `lb-02` hosts each running HAProxy + keepalived.
- Public DNS points at the keepalived VIP.
- HAProxy backends are the 3 `ratesengine-api` pods.
- Health-check path: `GET /v1/readyz` (deep check — Timescale +
  Redis reachability per `internal/api/v1/server.go`).

## Why HAProxy + keepalived (not nginx, not k8s ingress)

| Consideration | Choice | Why |
|---|---|---|
| L7 features needed? | minimal — health-check, retry, slowstart | HAProxy has the cleanest health-check semantics + observability for this shape |
| TLS termination? | yes (single edge) | Both nginx and HAProxy do this; HAProxy's stats endpoint is more useful for ops |
| Failover model | VRRP via keepalived | Operationally well-understood, no DNS-flip ambiguity, sub-second |
| Run on Kubernetes? | no | We deploy on Hetzner / Vultr / AWS-bare hosts (per ha-plan); k8s adds an SPF + ops surface we don't need at single-region scale |

## Configuration shape

`haproxy.cfg` (rendered per host):

```haproxy
global
    log stdout format raw local0
    maxconn 8000
    user haproxy
    group haproxy
    daemon
    stats socket /run/haproxy/admin.sock mode 660 level admin expose-fd listeners

defaults
    mode http
    log global
    option httplog
    option forwardfor
    timeout connect 5s
    timeout client  60s
    timeout server  60s
    retries 3

frontend api_https
    bind *:443 ssl crt /etc/haproxy/certs/{{ haproxy_tls_cert_filename }}
    bind *:80
    redirect scheme https code 301 if !{ ssl_fc }
    default_backend api_pool

backend api_pool
    balance roundrobin
    option httpchk GET /v1/readyz
    http-check expect status 200
    default-server inter 5s fall 3 rise 2 slowstart 10s
{% for h in groups['ratesengine_api'] %}
    server {{ h }} {{ hostvars[h].ansible_host }}:{{ ratesengine_api_port }} check
{% endfor %}

frontend stats
    bind 127.0.0.1:8404
    stats enable
    stats uri /
    stats refresh 10s
```

`keepalived.conf` (priority differs per host):

```keepalived
vrrp_script chk_haproxy {
    script "/usr/bin/pgrep haproxy"
    interval 2
    fall 2
    rise 2
}

vrrp_instance VI_API {
    state {{ keepalived_initial_state }}
    interface {{ keepalived_iface }}
    virtual_router_id {{ keepalived_vrid }}
    priority {{ keepalived_priority }}
    advert_int 1
    authentication {
        auth_type PASS
        auth_pass {{ keepalived_vrrp_password }}
    }
    virtual_ipaddress {
        {{ haproxy_vip }}/{{ keepalived_vip_prefix_length }}
    }
    track_script {
        chk_haproxy
    }
}
```

## Inventory model

```yaml
all:
  children:
    haproxy_lb:
      hosts:
        lb-01: { ansible_host: 10.0.0.31, keepalived_priority: 100, keepalived_initial_state: MASTER }
        lb-02: { ansible_host: 10.0.0.32, keepalived_priority: 99,  keepalived_initial_state: BACKUP }
      vars:
        haproxy_vip: 10.0.0.30
        keepalived_iface: eth0
        keepalived_vip_prefix_length: 24
        keepalived_vrid: 51
        # vault: keepalived_vrrp_password
    ratesengine_api:
      hosts:
        api-01: { ansible_host: 10.0.0.41 }
        api-02: { ansible_host: 10.0.0.42 }
        api-03: { ansible_host: 10.0.0.43 }
      vars:
        ratesengine_api_port: 3000
```

## Health-check semantics

`/v1/readyz` is the right check because:
- Returns 200 only when Timescale + Redis are both reachable.
- A pod that's *running* but can't read from Timescale shouldn't
  receive traffic — `/v1/healthz` would mark it healthy
  (process-alive); `/v1/readyz` correctly marks it unready.
- 5s interval, 3 fails before drain, 2 successes before re-add
  — gives a real outage 15s of detection latency, but a flaky
  pod won't oscillate.
- `slowstart 10s` ramps weight from 0 → 100% over 10s after
  re-add, preventing a cold pod from getting hammered.

## Failover scenarios

| Scenario | Detection | RTO | Notes |
|---|---|---|---|
| 1 api pod dies | HAProxy after 15s (3 × 5s `inter`) | 0 (others serve) | `slowstart` re-adds gradually after recovery |
| 1 HAProxy host dies | keepalived VRRP within 1-3s | ≤3s | VIP migrates to surviving host |
| Both HAProxy hosts die | n/a | manual | very rare; same blast radius as a region outage |
| HAProxy process dies but host alive | `chk_haproxy` script after 2-4s | 1-4s | keepalived demotes; surviving host's keepalived takes over VIP |

## What this role decides vs what's pinned by ha-plan

| Decision | Source |
|---|---|
| 2 LB hosts (not 3+) | ha-plan §3.1 — 2 is enough at single-region scale |
| keepalived VRRP for HA | ha-plan §3.1 |
| Health-check path `/v1/readyz` | ha-plan + `internal/api/v1/server.go` (existing endpoint) |
| HAProxy version | this role: bookworm-stable; matches Ubuntu 24.04 default |
| TLS cert provisioning | this role decides: operator drops cert in `/etc/haproxy/certs/`; cert-bot integration is a follow-up role (not bundled here to keep scope tight) |
| Stats endpoint | this role: 127.0.0.1:8404, loopback-only (no external auth needed) |

## Layout

```
configs/ansible/roles/haproxy/
├── README.md                          per-role docs
├── defaults/main.yml                  inventory-overridable defaults
├── handlers/main.yml                  reload-haproxy, restart-keepalived
├── meta/main.yml                      no dependencies
├── tasks/
│   ├── main.yml
│   ├── 01-preflight.yml               OS check, sysctl tuning (net.ipv4.ip_nonlocal_bind=1 for VIP)
│   ├── 02-install.yml                 apt install haproxy + keepalived
│   ├── 03-haproxy-configure.yml       render haproxy.cfg + reload on change
│   ├── 04-keepalived-configure.yml    render keepalived.conf + restart on change
│   ├── 05-systemd.yml                 systemd hardening drop-ins (loopback creds, NoNewPrivileges)
│   ├── 06-firewall.yml                allow 80/443 (public) + 8404 (loopback only)
│   └── 07-monitoring.yml              haproxy_exporter + textfile metrics for keepalived state
└── templates/
    ├── haproxy.cfg.j2
    ├── keepalived.conf.j2
    └── haproxy-systemd-override.conf.j2
```

Pattern matches the Patroni and Redis-Sentinel roles for consistency.

## Edge cases / gotchas

1. **VRRP needs `net.ipv4.ip_nonlocal_bind=1`** to allow HAProxy
   to bind to the VIP before keepalived assigns it. Set in
   preflight.

2. **VRRP traffic is multicast 224.0.0.18**; some clouds block
   this by default. ha-plan §3.1 calls out Hetzner allows VRRP
   on private VLANs; AWS does NOT (must use unicast VRRP, set
   `unicast_peer { ... }` per peer).

3. **HAProxy reload vs restart**: config change should `reload`
   (zero-drop), not `restart`. Handler uses `systemctl reload`.

4. **keepalived split-brain risk**: if both LBs see each other
   as down (network partition), both think they're MASTER → both
   bind the VIP → upstream gets confused. Mitigations:
   - VRRP `auth_type PASS` prevents foreign keepalived from
     joining the VRID.
   - `unicast_peer` (when used) avoids reliance on multicast.
   - Operationally we accept a brief split window during
     partition; the api pods themselves are fine, only LB-tier
     traffic is affected.

5. **TLS cert rotation**: cert-bot / acme.sh / certbot integration
   is a separate concern. This role just expects the cert at the
   configured path; reload picks up new certs without restart.

6. **Stats endpoint exposure**: bound to 127.0.0.1 only. If an
   operator wants remote stats access, SSH-tunnel; don't expose
   8404 publicly.

## Effort breakdown

| Step | Estimate |
|---|---|
| `defaults/main.yml` + inventory model docs | 1 h |
| `01-preflight.yml` (sysctl + assertions) | 1 h |
| `02-install.yml` | 1 h |
| `03-haproxy-configure.yml` + `04-keepalived-configure.yml` | 3 h |
| `05-systemd.yml` (override drop-ins) | 1 h |
| `06-firewall.yml` (nftables drop-in) | 1 h |
| `07-monitoring.yml` (haproxy_exporter + keepalived textfile scraper) | 2 h |
| `templates/haproxy.cfg.j2` + `keepalived.conf.j2` + override | 2 h |
| `README.md` (operator-facing) | 1 h |
| Local Vagrant 2-VM smoke test | 2 h |
| CHANGELOG | 0.5 h |
| **Total** | **~15 h, ~2 days** |

## Once HAProxy lands, what changes elsewhere

`docs/operations/runbooks/api-pod-down.md` (file may not yet
exist; create alongside if missing): mitigation steps shift from
"wait for the operator to remove the pod from DNS" to "wait for
HAProxy's 15s health-check window."

Coverage matrix #11–#16 row narrows by one: HAProxy goes from
"only `archival-node` exists today" to "Patroni + Redis Sentinel
+ HAProxy shipped; Prometheus + Loki remaining."

## Open questions for the implementer

1. **Should the role install `haproxy_exporter` or rely on
   HAProxy's built-in Prometheus endpoint?** HAProxy 2.4+ has
   `prometheus-exporter` built in; just enable + add to scrape
   config. Recommend: built-in. No external binary needed.

2. **VRRP `auth_pass` length**: keepalived truncates passwords
   beyond 8 bytes silently. Document this in the role README so
   operators don't generate a 32-char vault entry that
   effectively becomes the first 8 chars.

3. **VIP ARP behaviour after failover**: keepalived sends
   gratuitous ARP on transition. Some clouds discard these;
   document the per-cloud caveat in operator docs.
