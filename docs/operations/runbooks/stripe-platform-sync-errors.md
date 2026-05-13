---
title: Runbook — stripe-platform-sync-errors
last_verified: 2026-05-13
status: draft
severity: P3
---

# Runbook — `ratesengine_stripe_platform_sync_errors`

## At a glance

| Field | Value |
| ----- | ----- |
| Alert | `ratesengine_stripe_platform_sync_errors` (P3 / ticket) |
| Detected by | Prometheus rule in `deploy/monitoring/rules/api.yml` (and the R1 single-host overlay at `configs/prometheus/rules.r1/api.yml` once added). |
| Typical MTTR | 15–60 min: the failing layer is named in the metric label, and Stripe redelivers automatically once the underlying store is healthy. |
| Impact | Customer's API is on the new tier (Redis rate-limit applied), but their dashboard view of account tier / subscription / per-key budget is drifting from billing reality. No data loss — Stripe redelivers webhooks for ≤ 30 days. |

## What this fires on

The Stripe webhook handler at `internal/api/v1/stripe_webhook.go`
runs two paths in sequence:

1. **Redis rate-limit update** — the customer's API keys get
   their per-minute budget lifted. This is the user-visible half
   and 5xx-bubbles back to Stripe so the platform retries.
2. **Platform-store side effects** — Postgres-backed account row
   gets `tier` updated, `subscriptions` row upserted, every
   dashboard-created Postgres API key for the account gets its
   `rate_limit_per_min` lifted. This path is **best-effort and
   does NOT 5xx** — Stripe retries would just keep applying the
   same Redis rate-limit (already done) without making the
   platform-store path any healthier.

Each failure inside path (2) increments
`ratesengine_stripe_platform_sync_errors_total{operation=…}`.
Any non-zero rate over 15 min trips this alert.

The `operation` label tells you which store call is failing:

| Operation | Meaning |
| --------- | ------- |
| `get_account` | `platform.AccountStore.GetByStripeCustomerID(...)` failed. Could be: no account row for that Stripe customer ID (signup never finished plumbing the customer ID through), or a transient Postgres error. |
| `upsert_subscription` | `platform.BillingStore.UpsertSubscription(...)` failed. Postgres-side write error or schema drift. |
| `account_update` | `platform.AccountStore.Update(...)` failed when bumping `account.Tier`. Same root causes as upsert. |
| `list_keys` | `platform.APIKeyStore.ListForAccount(...)` failed. Postgres read failure. The per-key lift is skipped entirely; tier+sub already updated. |
| `key_update` | A specific `platform.APIKeyStore.Update(...)` failed for one key. Other keys in the same fan-out continue. |

## Quick diagnosis (≤ 5 min)

```sh
# Which operation is failing, and how often?
curl -s http://r1:9100/metrics | grep ratesengine_stripe_platform_sync_errors_total

# Recent webhook activity in the API logs.
ssh r1 'journalctl -u ratesengine-api --since "30 min ago" \
  | grep -E "stripe webhook" | tail -50'
```

The logs carry `event_id` (Stripe event ID), `account_id`,
`stripe_customer_id`, and the underlying error. If `get_account`
is the failing op and the error is a clean `not found`, the
customer's signup never linked their Stripe customer ID — that's
a signup-flow bug, not a transient Postgres issue.

## Fix paths

### `get_account` failures dominant

Most likely: a customer paid via Checkout but no account row exists
that maps to their Stripe customer ID. Check the signup flow:

```sql
-- On R1 Postgres:
SELECT id, email, stripe_customer_id, created_at
FROM accounts
WHERE email = '<customer-email>' OR stripe_customer_id = '<cus_...>';
```

If the row exists but `stripe_customer_id` is NULL, the
checkout-session-completed handler should have populated it. Patch
the row by hand if the customer is waiting:

```sql
UPDATE accounts
SET stripe_customer_id = '<cus_...>'
WHERE id = '<account-id>';
```

Then re-trigger the Stripe webhook in the Stripe dashboard
(Developers → Webhooks → resend the failed event).

### `upsert_subscription` / `account_update` / `list_keys` / `key_update` failures dominant

Postgres-side problem. Check connection pool, replication lag, or
schema drift:

```sh
ssh r1 'sudo -u postgres psql ratesengine -c "\d accounts"'
ssh r1 'sudo -u postgres psql ratesengine -c "SELECT count(*) FROM pg_stat_activity WHERE datname='\''ratesengine'\'';"'
```

Check whether the most recent migration applied cleanly:

```sh
ssh r1 'ratesengine-migrate version'
```

If a migration is pending, applying it usually clears the alert:

```sh
ssh r1 'ratesengine-migrate up'
```

### Persistent low-rate errors (one specific key/account)

If the `key_update` rate is very low and tied to one
`account_id`, it's likely one corrupted row (e.g. a NUMERIC field
out of range, a constraint violation). Find it:

```sql
SELECT id, account_id, prefix, rate_limit_per_min, revoked_at, created_at
FROM api_keys
WHERE account_id = '<acct-id>'
ORDER BY created_at DESC;
```

The webhook handler skips revoked keys + already-at-target keys, so
a row that consistently fails `Update` is almost always a constraint
violation introduced by a manual fix elsewhere.

## After the alert clears

1. Confirm the metric rate has dropped to 0 over a fresh 15 min
   window: `rate(ratesengine_stripe_platform_sync_errors_total[15m])`.
2. Spot-check that affected accounts caught up: their `tier` field
   matches their Stripe subscription, and their dashboard keys
   show the lifted `rate_limit_per_min`.
3. Stripe redelivers events for ≤ 30 days, so most missed updates
   self-heal once the store comes back. For events older than that,
   manually replay the relevant Stripe events from the dashboard.

## Related

- `internal/api/v1/stripe_webhook.go` — implementation; search
  for `obs.StripePlatformSyncErrorsTotal` to find the five
  failure sites covered by the per-`operation` label.
- [`customer-webhook-delivery-failing.md`](customer-webhook-delivery-failing.md)
  — the OTHER webhook health surface. This runbook is for the
  INBOUND deliveries (Stripe → us); the customer-webhook one is
  for OUTBOUND deliveries (us → customer). Operators paged on
  either should know about both — the OUTBOUND worker can be
  green while customer dashboards are stale because of an
  INBOUND Stripe-bridge degradation.
- F-1219 audit register entry — context for the platform-bridge
  fan-out the metric instruments.

## Why this exists

`ratesengine_stripe_platform_sync_errors_total` was introduced on
2026-05-13 to close the long-standing TODO from the F-1219 wave-32
remediation. The webhook had always logged platform-store failures
but never surfaced them as a metric, leaving operators blind to a
silently-degraded billing/dashboard sync. The counter pairs with
the audit-log row the same code path writes (`audit_log` table,
`event_type='stripe.upgrade'`), giving operators both an alertable
signal and a durable per-event trail.
