---
title: Runbook — MinIO Prometheus scrape returns 403
last_verified: 2026-05-28
status: ratified
severity: P2
---

# Runbook — MinIO Prometheus scrape returns 403

## At a glance

| Field | Value |
| ----- | ----- |
| Symptom | Prometheus targets API shows `minio` job `down` with `lastError: server returned HTTP status 403 Forbidden`. |
| Severity | P2 (ticket) |
| Detected by | Operator inspection or `up{job="minio"} == 0` alert |
| Typical MTTR | 10 minutes (provision token + restart Prometheus) |
| Impact | MinIO observability gap: no bucket-usage, replication, or write-latency metrics scraped. Operator can't alert on disk exhaustion of the MinIO data partition until the token is wired. |

## Why this happens

MinIO's `/minio/v2/metrics/cluster` endpoint requires a bearer
token by default. `configs/prometheus/prometheus.r1.yml` already
points Prometheus at the right URL with
`bearer_token_file: /etc/prometheus/minio.token`, but the token
file isn't created automatically — it's an operator-mint step. If
no token has ever been provisioned, every scrape returns 403 and
the job stays `down`.

This is finding F-0045 / task #38 of audit-2026-05-26.

## Provisioning procedure

### 1. Mint a service account on MinIO

SSH to r1 and run `mc` against the local MinIO server. Replace
the placeholder host alias `local` with whatever
`/root/.mc/config.json` calls it (default `local`).

```sh
ssh root@136.243.90.96
mc admin user svcacct add local "<MINIO_ROOT_USER>" \
  --policy prometheus-read \
  --name "prometheus-metrics-scrape" \
  --description "Bearer-token scrape for /minio/v2/metrics/cluster"
```

If the `prometheus-read` policy doesn't yet exist, create it:

```sh
cat > /tmp/prometheus-policy.json <<'EOF'
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": ["admin:Prometheus"],
      "Resource": ["arn:aws:s3:::*"]
    }
  ]
}
EOF
mc admin policy create local prometheus-read /tmp/prometheus-policy.json
rm /tmp/prometheus-policy.json
```

The `svcacct add` command prints a `Secret Key` — that's the
bearer token. Copy it; you only see it once.

### 2. Write the token file

```sh
# On r1, as root.
umask 077
echo "<the-secret-key>" > /etc/prometheus/minio.token
chown prometheus:prometheus /etc/prometheus/minio.token
chmod 0400 /etc/prometheus/minio.token
```

The token file is `0400` owned by `prometheus:prometheus` so
only Prometheus can read it. Confirm:

```sh
ls -l /etc/prometheus/minio.token
# -r-------- 1 prometheus prometheus 40 May 28 09:00 /etc/prometheus/minio.token
```

### 3. Reload Prometheus

```sh
systemctl reload prometheus
```

Wait one scrape interval (15-30 s) and check the target health:

```sh
curl -sS http://localhost:9090/api/v1/targets \
  | jq '.data.activeTargets[] | select(.labels.job=="minio") | {health, lastError}'
```

Expected:

```json
{
  "health": "up",
  "lastError": ""
}
```

### 4. Confirm metrics flow

```sh
curl -sS http://localhost:9090/api/v1/query \
  --data-urlencode 'query=minio_cluster_capacity_usable_total_bytes' \
  | jq '.data.result | length'
# Expect: > 0
```

If the result is `0`, MinIO accepted the token (no 403) but isn't
returning metrics — usually means the `prometheus-read` policy
needs an additional permission. Re-check the policy JSON above
against the upstream MinIO docs.

## Failure modes

- **Still 403 after token file written.** The token doesn't
  match the service account that was minted, OR the
  service-account policy doesn't include `admin:Prometheus`. Re-mint
  the svcacct (it's free to do — just keep one alive at a time)
  and try again. Confirm the policy is attached via
  `mc admin user svcacct info local <svcacct-access-key>`.
- **Prometheus permission denied on the token file.** Symptom:
  `lastError: error reading bearer token file ... permission
  denied`. Fix: ownership (`chown prometheus:prometheus`) and
  mode (`0400`).
- **Scrape times out.** MinIO under heavy load can take >5 s to
  emit the metrics page. Bump `scrape_timeout` on the `minio`
  job in `prometheus.r1.yml`; reload Prometheus.
- **Service account revoked / token rotated.** Mint a new one and
  re-run steps 2-3.

## Long-term: Ansible

This procedure is currently manual because we don't ship a
single-host Ansible role that owns
`/etc/prometheus/minio.token` (the
`prometheus_pair`-shape role targets HA-pair multi-host per
F-0140). When that gap closes, the token-mint step lives in the
role and the manual procedure here becomes a fall-back.

Until then, `make verify-r1-sync` will surface any drift between
the `prometheus.r1.yml` scrape config and the running daemon's
view — but it can't generate the token file itself. That's an
operator step every time the MinIO svcacct rotates.

## Related

- ADR-0002 — self-hosted storage is S3-compatible (MinIO is the
  default).
- F-0045 (audit-2026-05-26) — original finding.
- `configs/prometheus/prometheus.r1.yml:148-159` — scrape stanza.
- F-0152 closure — sibling exporters (redis / postgres /
  pgbackrest) now installed; MinIO is the last one waiting on
  this manual token step.

## Changelog

- 2026-05-28 — initial draft (F-0045 procedure documentation).
