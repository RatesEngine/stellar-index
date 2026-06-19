---
title: SEV status-page update
last_verified: 2026-05-13
status: living doc
related:
  - docs/operations/sev-playbook.md
  - internal/incidents/data/_template.md
  - web/status/
---

# SEV status-page update

The customer-facing companion to the on-call escalation flow.
Every SEV that meets the visibility threshold below MUST be
posted to `stellarindex.io/status`; this runbook is the binding
how-to.

F-1211 (codex audit-2026-05-12): the prior version of this
runbook pointed at a `deploy/status-page/cstate/...` workflow
that no longer exists — the status page is now a custom
Next.js static export at `web/status/`, hosted on Cloudflare
Pages, that renders incidents from
`internal/incidents/data/<YYYY-MM-DD>-<slug>.md` files
embedded into the API binary at build time. This runbook is
rewritten around that path so a SEV operator following it
top-to-bottom no longer hits a dead-end.

## When to post

Post a status-page update for any incident that:

- a customer might notice from the outside (HTTP 5xx rate spike,
  latency SLO breach, ingest lag visible on `flags.stale`,
  prolonged data-freshness gap, etc.), AND
- has fired a SEV-1 or SEV-2 per [`sev-playbook.md`](../sev-playbook.md).

Operator-internal incidents (e.g. a build failure, a non-
production drift event, an internal monitoring blip that didn't
affect serving) MUST NOT post — the page exists to tell
customers about THEIR experience, not ours.

## Update cadence

Per Freighter F3.5 / F3.6 + the SEV playbook §2:

| Severity | First update | Subsequent updates |
|---|---|---|
| SEV-1 | At detection (≤ 15 min from SEV start) | Every hour |
| SEV-2 | At detection (≤ 30 min from SEV start) | Every 24 h |

If a SEV-2 escalates to SEV-1, switch to the hourly cadence at
the escalation timestamp; don't wait for the next hour boundary.

## Steps

### 1 — Open a fresh incident file

```sh
cd internal/incidents/data
SLUG=pricing-api-stale                     # short lowercase hyphen-separated
DATE=$(date -u +%Y-%m-%d)
cp _template.md "${DATE}-${SLUG}.md"
$EDITOR "${DATE}-${SLUG}.md"
```

`<slug>` is what appears in the incident URL
(`stellarindex.io/status/incident/${DATE}-${SLUG}`) so make it
informative; the front-matter `title` field is what the customer
reads on the index page.

### 2 — Fill the YAML frontmatter

Match the `_template.md` shape precisely. Required fields:

```yaml
title: "[SEV-1] Pricing API returning stale prices"
date: 2026-05-13
severity: SEV-1
status: investigating
started_at: 2026-05-13T18:00:00Z
resolved_at:                                 # leave empty until resolved
affected_components:
  - api
postmortem:                                  # leave empty until the postmortem is written
```

`affected_components:` values are operator-defined string
labels (e.g. `api`, `indexer`, `aggregator`, `storage`) — the
status page renders them as badges on the incident card. Pick
the same labels you'd use in Slack so customers and operators
read the same vocabulary.

`severity` accepts `SEV-1`, `SEV-2`, or `SEV-3` (`incidents.go`
maps them to display strings).

`status` is one of `investigating`, `identified`, `monitoring`,
`resolved`. The status page UI renders a colored pill from this
field.

### 3 — Write the first update body

Use the template's `## Identification` + `## Impact` +
`## Timeline` shape. The Timeline table is the append-only
event log customers refresh during the incident — never edit
prior entries.

Plain English from the customer POV. No jargon, no internal
component names that aren't already in their integration docs.

> **Bad:** "Aggregator is throwing PgError 53300 'too many
> connections' on the secondary; primary failover initiated."
>
> **Good:** "We're seeing elevated error rates on the pricing
> API; some requests are returning 5xx. Engineers are
> investigating; next update in 1 hour."

The status page is a **public** surface. Don't leak internal
infrastructure detail; don't speculate about cause until the
"Identified" stage; don't blame upstream providers by name in
the early updates.

### 4 — Commit + push

```sh
git checkout -b sev-${DATE}-${SLUG}
git add internal/incidents/data/${DATE}-${SLUG}.md
git commit -m "incident: ${DATE}-${SLUG} (SEV-${N})"
git push -u origin sev-${DATE}-${SLUG}
gh pr create --title "incident: ${DATE}-${SLUG}" --body ""
gh pr merge --squash --auto
```

The `web/status` Cloudflare Pages deploy fires automatically on
the merge into `main` and renders the new incident at
`stellarindex.io/status` within a minute or two. Verify the
incident lands on the index page before stepping away.

### 5 — Customer webhook fan-out (optional but expected)

After the incident `.md` is merged AND the API binary is
redeployed (the corpus is `go:embed`-baked into the binary),
fan out the `incident.sev1` webhook so dashboard subscribers
get a callback. F-1249 (codex audit-2026-05-12) on R1:

```sh
ssh root@r1 -- /usr/local/bin/stellarindex-ops emit-incident \
  -config /etc/stellarindex.toml \
  -slug ${DATE}-${SLUG} \
  -event sev1
```

When the SEV closes, after the same merge + deploy cycle
flips the corpus's `status: resolved`:

```sh
ssh root@r1 -- /usr/local/bin/stellarindex-ops emit-incident \
  -config /etc/stellarindex.toml \
  -slug ${DATE}-${SLUG} \
  -event resolved
```

The command refuses semantically-impossible combinations
(sev1 on a non-SEV-1 entry, resolved on an investigating
entry) before any network I/O, so an operator typo can't
fire the wrong webhook.

## Append updates as the incident progresses

For each subsequent update (per the cadence table above):

```sh
$EDITOR internal/incidents/data/${DATE}-${SLUG}.md
# Append a new row to the Timeline table; flip `status:` if appropriate
git add ... && git commit -m "incident: ${DATE}-${SLUG} update HH:MM" && \
  git push && gh pr merge --squash --auto
```

The Cloudflare Pages deploy re-runs and the new row appears
on the page on the next refresh.

## Resolution

When the SEV is closed:

1. Set `status: resolved` and stamp `resolved_at:` in the
   frontmatter.
2. Append the final `## Timeline` row marking `**Resolved.**`.
3. Fill the `## What we did` section.
4. Commit + push (same flow as above).
5. After the deploy lands, fire `emit-incident -event resolved`
   so dashboard subscribers get the close-out callback.
6. Postmortem follow-up: when the postmortem lands at
   `docs/operations/postmortems/${DATE}-${SLUG}.md`, update
   the incident's frontmatter `postmortem:` field to point at
   it (the status page renders a "Read the full postmortem"
   link from that field).

## Workstation-down fallback

If the operator's workstation is unavailable but they can
reach R1 via SSH, the incident can be drafted directly on the
host via the deployed binary's writeable copy of the corpus —
but this WILL be lost on the next deploy because the embedded
corpus rebuilds from `git`. The drafted file MUST be committed
upstream within the same hour. There is no "host-only" path
that survives a deploy; the corpus is the source of truth in
git, full stop.

## Related

- [`sev-playbook.md`](../sev-playbook.md) — the SEV ladder, on-
  call rotation, severity definitions.
- [`internal/incidents/data/_template.md`](../../../internal/incidents/data/_template.md)
  — the canonical incident frontmatter + body shape.
- [`web/status/`](../../../web/status/) — the Next.js static-
  export status-page source (incidents render from
  `incidents.ts` which build-time-loads the corpus).
- [`internal/incidents/incidents.go`](../../../internal/incidents/incidents.go)
  — the `go:embed` loader that bakes the corpus into the API
  binary for `/v1/incidents`.
