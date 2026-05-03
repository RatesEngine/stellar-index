<!--
Pre-launch maintenance heads-up. Send a day ahead of any
planned change — release cut, region-switching test,
infrastructure migration. Skip for routine PR merges; this is
for changes that *might* affect customer-visible behaviour.

Channels:
  - Status page: open a "scheduled maintenance" issue
  - Customer email: opt-in list per the customer-comms log

Subject: "Rates Engine — scheduled maintenance {{utc_start}}"
-->

# Scheduled maintenance — {{title}}

**When:** {{utc_start}} — {{utc_end}} ({{duration}}).
**Surfaces:** {{affected_components}}
**Expected customer impact:** {{impact}}

<!-- Examples:
- impact: "None — change is internal config; cache continues
  serving from existing closed buckets."
- impact: "Brief blip (≤30s) on /v1/price/stream
  reconnects as the API binary restarts. Use SSE
  Last-Event-ID to resume cleanly."
- impact: "Read-only mode for ~5min while we run a
  migration; writes (POST /v1/account/keys) will 503."
-->

## What we're changing

{{description}}

## Why

{{rationale}}

## Rollback plan

If anything misbehaves we revert per
<https://github.com/RatesEngine/rates-engine/blob/main/docs/operations/rollback.md>.
Status page reflects the live state as we go.

## Questions

Reply to this email or open an issue at
<https://github.com/RatesEngine/rates-engine/issues>.

— {{your_name}}
