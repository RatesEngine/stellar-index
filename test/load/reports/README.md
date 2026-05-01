# `test/load/reports/` — generated artefacts (gitignored)

k6 run outputs and the markdown summaries that feed the SLA proof
report (Task #77) land here. Per-run files are gitignored; this
README is the only checked-in artefact, so the directory exists.

## Layout

```
reports/
├── README.md                       (this file)
└── sla-proof-<YYYY-MM-DD>.md       per-run summary; the canonical
                                    artefact promoted to docs/operations/
```

After a `make test-load-mixed` run the operator copies the resulting
`sla-proof-<date>.md` into `docs/operations/` to land it as the
month's proof-of-SLA. The Grafana snapshot link inside the markdown
is the durable graphical evidence.

## What's NOT here

- Raw k6 JSON output — Prometheus is the durable store
  (`--out experimental-prometheus-rw`).
- Grafana dashboard config — that's in `deploy/monitoring/grafana/`.
