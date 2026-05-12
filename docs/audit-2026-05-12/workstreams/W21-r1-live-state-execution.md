# W21 — R1 live state vs claimed state

## Scope

Live deployment behavior captured via SSH probes, reconciled
against documented state.

In scope:
- R1 systemd state
- R1 listening ports
- R1 disk / memory / CPU
- R1 access log patterns
- R1 Caddy / Cloudflare path
- R1 Postgres / Redis / MinIO / Galexie health
- R1 Prometheus / Alertmanager / Loki / Promtail health
- R1 SLA probe + smoke + heartbeat truth

Out of scope:
- R2 / R3 live state (EX-1208 — they don't exist today)

## Inputs

- `12-r1-live-probe-protocol.md` — protocol
- `evidence/r1-probes/r1-baseline-2026-05-12.md` — kick-off
  transcript
- `docs/operations/r1-deployment-state.md` — claimed state

## Probe checklist

Walk every section of `12-r1-live-probe-protocol.md` and
capture a transcript:

| § | Topic | Done | Transcript | Findings |
| --- | --- | --- | --- | --- |
| 1 | Service inventory | partial (kick-off) | r1-baseline-2026-05-12.md | |
| 2 | Timer inventory | partial (kick-off) | same | |
| 3 | Listening ports | partial (kick-off) | same | stellar-core on 11726 vs CLAUDE.md claim |
| 4 | Disk + memory | partial (kick-off) | same | MinIO 78%, mem 95%, swap full |
| 5 | Live access log analysis | partial (kick-off) | same | `/v1/coins/*` still 200ing |
| 6 | Smoke test | _todo_ | | `/opt/.../r1-smoke.sh` absent |
| 7 | Caddy + TLS | _todo_ | | |
| 8 | Postgres health | _todo_ | | |
| 9 | Redis health | _todo_ | | |
| 10 | MinIO + Galexie | _todo_ | | |
| 11 | Prometheus + Alertmanager + Loki | _todo_ | | |
| 12 | SLA probe + heartbeat truth | _todo_ | | |

## Reconciliation (R12 pass)

Compare `docs/operations/r1-deployment-state.md` claims to
live R1:

| Documented claim | Live state | Verdict | Finding |
| --- | --- | --- | --- |
| stellar-core removed 2026-04-23 | port 11726 still bound to stellar-core process (kick-off probe) | contradicted | _open finding_ |
| smoke timer fires every 5min via `scripts/dev/r1-smoke.sh` | timer fires; script at `/opt/...` is absent | contradicted | _open finding_ |
| `/v1/coins` and `/v1/currencies` removed in rc.48 | live binary still serves 200s | _verify after deploy completes_ | _depends on deploy timing_ |
| MinIO retention vs disk usage | 78% at audit time | _verify alert threshold_ | _potential headroom finding_ |
| memory budget | ~95% with swap full | _verify expected_ | _potential finding_ |
| stellar-core / galexie roles per ADR-0016 | galexie running on 6061 | _verify_ | |
| Prometheus 2.45.3 + node-exporter + Loki + promtail | all observed | confirmed | |

## Per-disk audit

- `/var/lib/postgresql` 40G/1.5T — what's the daily growth?
- `/var/lib/minio` 4.9T/6.4T — what's the retention policy?
- `/var/lib/galexie` 7G/1.5T — what's expected steady state?

## Per-process memory ranking

- `ps aux --sort=-rss | head -10` — capture top consumers
- explain why memory is at 95%
- explain why swap is fully consumed

## Per-service CPU baseline

- aggregator CPU per minute
- indexer CPU per minute
- API CPU per request

## Adversarial vectors

- C1.*, C2.*, C5.* (infra layer probes)
- E1.1..E1.4 privileged commands (audit-trail check)

## Cross-workstream dependencies

- W06 owns stellar-core / stellar-rpc residue question
- W11 owns `/v1/coins/*` slow-404 / still-200 question
- W14 owns alertmanager + monitoring on R1
- W18 owns systemd unit provisioning

## Closure criteria

- Every probe-protocol section has a transcript
- R12 reconciliation table complete
- Per-disk / per-process / per-service tables complete
- All discrepancies between docs and live state filed as findings
