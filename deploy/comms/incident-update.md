<!--
Mid-incident customer-facing update. Append into the body of
the incident Markdown file at
`internal/incidents/data/<YYYY-MM-DD>-<slug>.md` under a
fresh "## Update — <UTC timestamp>" subheading. The
`web/status/` Cloudflare Pages build picks it up on every push
to `main`; `stellarindex-ops emit-incident --slug <slug>`
re-fires the customer-webhook fan-out so subscribed customers
get the new payload too. (F-1211, 2026-05-13: prose corrects
earlier external-issue tracker references — the project never
adopted that path.) Full authoring procedure:
`docs/operations/runbooks/sev-status-page-update.md`.

Send a fresh update every 30 minutes during an open incident,
even if the only thing to say is "still investigating". Silence
is worse than "no progress yet" — customers fill silence with
worse assumptions.

Tone: factual, time-stamped, present tense. No speculation
about root cause until the postmortem.
-->

# {{incident_title}}

**Status:** {{status}}                                     <!-- investigating / identified / mitigating / resolved -->
**Started:** {{utc_start}}
**Last update:** {{utc_now}}
**Affected surfaces:** {{affected_components}}

## What we're seeing

{{symptoms}}

<!-- Examples to anchor: "p95 latency on /v1/price has
exceeded 1s since 14:20 UTC. Other surfaces nominal."
"/v1/healthz returning 503 from r2; r1 + r3 unaffected."
"flags.frozen=true on USDC pairs across all regions since
17:42; underlying anomaly checker engaged on a stablecoin
depeg signal — investigating whether a real depeg or a
detector false positive." -->

## What we're doing

{{action}}

<!-- One sentence per action. Avoid the passive voice — say
who's doing what. "Failover to r2 is in progress" beats
"failover has been initiated"; "Diagnosing the divergence
checker" beats "Investigating the issue". -->

## Expected resolution

{{eta_or_unknown}}

<!-- "By 15:00 UTC" if mitigation has a known cadence; "ETA
unknown — next update at {{utc_now + 30m}}" if not. NEVER
"shortly", "soon", or other unbounded promises. -->

## Customer impact

{{impact_summary}}

<!-- Concrete: "Customers querying /v1/price for USDC pairs
will see flags.frozen=true and last-known-good values until
this resolves." or "No customer-visible impact; investigating
preemptively before page-worthy thresholds are reached." -->

---

*Next update at {{utc_next_update}}. Status page:
<https://stellarindex.io/status>.*
