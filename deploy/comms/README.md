# Customer-comms templates

Pre-written messages for the launch sprint. Each template uses
`{{...}}` placeholders the operator fills in at send time. The
point of pre-baking is **drafting under stress is bad drafting**
— at 02:00 during an incident, or 09:00 on cutover morning, the
operator should be picking the right template, not staring at
a blank page.

## Templates

| File | When to send | Where |
|---|---|---|
| [`launch-announcement.md`](launch-announcement.md) | T-0 (immediately after the cut completes) | Public — email to RFP contacts, Slack `#rates-engine-public`, project handle |
| [`onboarding-email.md`](onboarding-email.md) | First customer signup post-launch | Direct reply to the request, or in-app onboarding |
| [`incident-update.md`](incident-update.md) | Mid-incident customer-facing update | Status-page issue body + email to affected customers |
| [`maintenance-window.md`](maintenance-window.md) | Pre-cut maintenance heads-up | Status page + customer email a day ahead of any planned change |
| [`rollback-update.md`](rollback-update.md) | If a release is rolled back | Public-facing follow-up to the launch-announcement thread |

## Conventions

- **`{{customer_name}}`** — the customer's name or org. Keep
  one customer per send for personalisation; bulk announcements
  drop personalisation entirely (use "Hi all" or similar).
- **`{{incident_id}}`** — the GitHub issue number on
  `RatesEngine/ratesengine-status` (Upptime auto-creates these
  on probe failure; manual incidents follow the same shape per
  [`status-page-setup.md`](../../docs/operations/status-page-setup.md)).
- **`{{tag}}`** — the CalVer release tag (e.g. `2026.07.15.1`).
- **`{{utc_time}}`** — RFC-3339 UTC timestamp; e.g.
  `2026-05-03T14:23:00Z`.
- **`{{api_url}}`** — `https://api.ratesengine.net/v1` for
  prod; staging URL for non-prod sends.

## Edit-then-commit cycle

After every customer-comms send, copy the actual sent text
back into a dated postmortem-style doc under
`docs/operations/comms-log/YYYY-MM-DD-<slug>.md` (create the
directory on first use). Keeps an audit trail of what was
actually said vs. the template — useful if a customer
references the message in a future support request.

## Cross-references

- [`docs/operations/launch-day-checklist.md`](../../docs/operations/launch-day-checklist.md) §T-0
  — calls `launch-announcement.md`.
- [`docs/operations/rollback.md`](../../docs/operations/rollback.md) §Post-rollback
  — calls `rollback-update.md`.
- [`docs/operations/sev-playbook.md`](../../docs/operations/sev-playbook.md)
  — incident escalation; calls `incident-update.md`.
