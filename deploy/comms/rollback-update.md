<!--
Sent when a release is rolled back. Goes out as a follow-up
to the original launch-announcement thread (or the prior
release's announcement, for a non-launch rollback).

Tone: honest. State what was broken, what was rolled back,
what the customer-visible impact was, what's next. No
speculation about root cause until the postmortem ships.

Subject: "Rates Engine — {{tag}} rolled back ({{symptom}})"
-->

# {{tag}} rolled back

We rolled back the {{tag}} release at {{utc_rollback}} after
detecting {{symptom}}.

## What was wrong

{{what_was_wrong}}

<!-- Concrete: "p99 latency on /v1/price exceeded 1s sustained
for 6 minutes" or "Confidence scores on USDC pairs collapsed
to zero due to a bug in the divergence-cache lookup". -->

## What's running now

The previous release ({{previous_tag}}) is back in service.
Customer-visible impact during the affected window
({{utc_problem_start}} – {{utc_rollback}}):
{{customer_impact}}.

<!-- Examples:
- "Some /v1/price requests returned 503 with a clear envelope
  (no 5xx leak). flags.frozen=false throughout — no
  bad data was published."
- "All requests succeeded but with degraded confidence scores
  visible in the response envelope. Triangulation paths and
  oracle data were unaffected."
-->

## Why we rolled back instead of fixing forward

{{rollback_rationale}}

<!-- "The bad data window grows linearly with time-running.
Restoring known-good is cheaper than waiting for a fix to
land + go through CI." or "The bug was in a hot path; the
right place to land the fix is the next release, not a
hot-patch on top of {{tag}}." -->

## What's next

A postmortem will be published at
<https://github.com/RatesEngine/rates-engine/blob/main/docs/operations/postmortems/>
within {{postmortem_eta}}, including:

- The root-cause mechanism.
- The action items + owners that prevent recurrence.
- The action item that adds the missing test / detector that
  would have caught this pre-launch.

Until then, things are stable on {{previous_tag}}. Status:
<https://status.ratesengine.net>.

— {{your_name}}
