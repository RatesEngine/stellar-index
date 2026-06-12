package v1

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/auth"
	"github.com/StellarAtlas/stellar-atlas/internal/obs"
	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// StripeKeyManager is the v1 boundary for the Stripe webhook
// handler's key-mutation needs. Implementation:
// [auth.RedisAPIKeyStore] (which provides all three methods).
type StripeKeyManager interface {
	ListKeysForIdentifier(ctx context.Context, identifier string) ([]auth.APIKeyRecord, error)
	UpdateRateLimit(ctx context.Context, keyID string, newRateLimitPerMin int) (auth.APIKeyRecord, error)
}

// StripeEventStore is the dedupe-row seam used by the Stripe
// webhook handler. Methods are a strict subset of the
// [platform.BillingStore] interface — only the three Stripe-event
// methods. The handler accepts this narrower interface so a
// future deployment without a full BillingStore (e.g. tests, or
// a billing-disabled replica) can wire just the dedupe path.
//
// F-1227 (audit-2026-05-12): without this seam wired, Stripe's
// at-least-once delivery means a late-arriving duplicate event
// can re-upgrade a customer who was just manually downgraded.
type StripeEventStore interface {
	AppendStripeEvent(ctx context.Context, e platform.StripeEvent) error
	MarkStripeEventProcessed(ctx context.Context, stripeEventID string) error
	MarkStripeEventFailed(ctx context.Context, stripeEventID string, err string) error
}

// StripeWebhookConfig wires the handler. SigningSecret is the
// `whsec_…` value from the Stripe dashboard — used to validate the
// Stripe-Signature header per
// https://docs.stripe.com/webhooks#verify-events. Empty makes the
// handler reject every request 503 (no signing secret = no way to
// trust the payload).
//
// Manager handles the actual key-mutation (read keys for an
// identifier + lift their rate-limit). Production wiring is
// [auth.RedisAPIKeyStore] which provides both methods on
// [StripeKeyManager].
//
// Events, when non-nil, dedupes inbound events by stripe_event_id
// so retries are idempotent + a manual downgrade isn't silently
// re-upgraded by a delayed-redelivery of the original event.
// Nil = no dedupe (legacy behaviour); the handler logs a warning
// at startup so operators know.
type StripeWebhookConfig struct {
	SigningSecret string
	Manager       StripeKeyManager
	Events        StripeEventStore
	// Audit receives one platform.AuditEntry per successful tier
	// upgrade (one row per webhook event, NOT per upgraded key —
	// the row carries the upgraded-key count + identifier in
	// metadata so the dashboard can show "the upgrade happened"
	// without rendering N rows for a customer with N keys).
	//
	// Nil = no audit row written (pre-F-1240 behaviour preserved
	// for tests + deployments that haven't migrated to the
	// platform postgres schema). F-1240 (audit-2026-05-12).
	Audit StripeAuditSink

	// Platform, when non-nil, wires the F-1219 Stripe-side effects
	// onto the canonical platform stores: UpsertSubscription on
	// the BillingStore, account tier mutation on the AccountStore.
	// Without this the webhook only mutates Redis API-key
	// rate-limits — leaving the dashboard's view of the customer's
	// plan out of sync with what they paid for. F-1219 (codex
	// audit-2026-05-12). Nil disables the platform-side write
	// path (Redis key updates still happen via Manager).
	Platform *StripePlatformBridge

	// Now is overridable for tests; defaults to time.Now.
	Now func() time.Time
	// MaxAge is the maximum Stripe-Signature timestamp drift accepted
	// (rejects replays). Default 5 min.
	MaxAge time.Duration
}

// StripePlatformBridge groups the platform stores the Stripe
// webhook updates AFTER mutating the Redis keys. Each field is
// a narrow interface so deployments can wire subsets — tests
// commonly leave Accounts nil, for example.
type StripePlatformBridge struct {
	// Accounts looks up the account by Stripe customer ID and
	// is the surface for updating .Tier on a successful upgrade.
	Accounts platform.AccountStore
	// Billing receives the subscription upsert. The webhook
	// builds a Subscription record from the checkout session
	// metadata + customer mapping.
	Billing platform.BillingStore
	// APIKeys, when non-nil, surfaces the platform-side keys
	// belonging to the upgraded account so the Stripe handler
	// can lift their `RateLimitPerMin` in lock-step with the
	// Redis-backed legacy keys. F-1219 (codex audit-2026-05-13)
	// follow-up: pre-fix the upgrade only touched Redis-stored
	// `/v1/signup` keys, leaving Postgres-backed dashboard keys
	// stuck at the pre-paid budget. Nil disables the per-key
	// platform fan-out (deployments without a Postgres store
	// stay on the Redis-only path).
	APIKeys platform.APIKeyStore
	// TierMap maps the Stripe metadata.tier value (e.g. "pro")
	// to the platform.Tier the account should land on. Nil
	// defaults to {pro: TierPro, business: TierBusiness,
	// enterprise: TierEnterprise} so production wiring can
	// pass an empty bridge.
	TierMap map[string]platform.Tier
}

// StripeAuditSink is the narrow subset of [platform.AuditStore]
// the Stripe webhook needs. Declared as an interface so we don't
// drag the full audit-log surface into the v1 package's public
// signature; production wires
// `internal/platform/postgresstore.AuditStore`.
type StripeAuditSink interface {
	Append(ctx context.Context, e platform.AuditEntry) error
}

// stripeTierMap controls which tier a Stripe metadata.tier value
// upgrades to. Keep in lock-step with the /signup page tier table:
//
//	starter   →  1000 req/min  (free; not actually meaningful here)
//	pro       → 10000 req/min
//	business  → 50000 req/min
//	enterprise → caller specifies via metadata.rate_limit_per_min override
var stripeTierMap = map[string]int{
	"starter":  1000,
	"pro":      10000,
	"business": 50000,
}

// stripeEvent is the minimal Stripe event shape we consume.
// Stripe's full event shape is enormous; we only inspect the
// fields the webhook flow needs.
type stripeEvent struct {
	ID   string `json:"id"`
	Type string `json:"type"`
	Data struct {
		Object stripeCheckoutSession `json:"object"`
	} `json:"data"`
}

// stripeCheckoutSession is the shape we consume from BOTH
// `checkout.session.completed` events (the original supported
// type) and `customer.subscription.*` events (F-1219 follow-up).
//
// JSON keys overlap conveniently:
//
//   - checkout.session.completed.data.object has: id,
//     client_reference_id, customer, customer_email,
//     payment_status, subscription, metadata
//   - customer.subscription.{updated,deleted}.data.object has:
//     id, customer, status, current_period_end,
//     cancel_at_period_end, metadata
//
// The `id` field doubles as both checkout-session id and
// subscription id depending on event type. The handler maps
// according to ev.Type so a future Stripe schema change to one
// shape doesn't poison the other path.
type stripeCheckoutSession struct {
	ID                string            `json:"id"`
	ClientReferenceID string            `json:"client_reference_id"` // we set this to the customer's identifier
	Customer          string            `json:"customer"`            // Stripe customer ID (cus_…)
	CustomerEmail     string            `json:"customer_email"`
	PaymentStatus     string            `json:"payment_status"`
	Subscription      string            `json:"subscription"` // Stripe subscription ID (sub_…) when this is a subscription checkout
	Metadata          map[string]string `json:"metadata"`

	// customer.subscription.* fields below. Zero values when the
	// event is a checkout.session.completed.
	Status             string `json:"status"`               // active | past_due | canceled | unpaid | …
	CurrentPeriodStart int64  `json:"current_period_start"` // Unix seconds
	CurrentPeriodEnd   int64  `json:"current_period_end"`   // Unix seconds
	CancelAtPeriodEnd  bool   `json:"cancel_at_period_end"`
	CanceledAt         int64  `json:"canceled_at"` // Unix seconds; zero = not canceled

	// invoice.paid fields below. Stripe Invoice JSON shape has its
	// own period_start / period_end window separate from the
	// subscription's; we reuse `Status` ("paid"/"open") and the
	// Customer + Subscription string IDs above, plus these two
	// extra fields for the invoice's own period (which matches
	// the subscription's current period when this invoice is the
	// recurring renewal). F-1219 follow-up (codex audit-2026-05-12).
	PeriodStart int64 `json:"period_start"` // Unix seconds (invoice.paid)
	PeriodEnd   int64 `json:"period_end"`   // Unix seconds (invoice.paid)
	Paid        bool  `json:"paid"`         // invoice.paid → true
}

// handleStripeWebhook serves POST /v1/webhooks/stripe.
//
// Validates the Stripe-Signature header per the documented
// HMAC-SHA256 scheme, parses the event, and on
// `checkout.session.completed` upgrades every key belonging to the
// identifier in `client_reference_id` to the per-tier
// rate-limit. Idempotent on Stripe's side via Stripe's at-least-
// once delivery + the webhook handler's read-then-write semantics
// (subsequent identical events re-set the same RateLimitPerMin).
//
// Stripe metadata fields consumed:
//
//	tier                    one of starter / pro / business
//	rate_limit_per_min      optional integer override (Enterprise)
//
// Both are operator-set on the Stripe Checkout session create call.
//
// Returns 200 + body `{"ok": true, "upgraded": N}` on success.
// Stripe replays webhooks until it gets a 2xx; non-2xx triggers
// retries.
func (s *Server) handleStripeWebhook(w http.ResponseWriter, r *http.Request) { //nolint:funlen // extends past 100 lines by the F-1219 platform-side bridge; splitting fragments the linear "validate → look up → upgrade → audit → mark-processed" flow that operators read top-to-bottom during incident review.
	ev, ok := s.parseStripeWebhook(w, r)
	if !ok {
		return
	}
	if !s.stripeDedupeOK(w, r, ev) {
		return
	}

	// F-1219 follow-up (codex audit-2026-05-12) — customer.subscription.*
	// events refine the placeholder CurrentPeriodEnd the
	// checkout.session.completed path stamped at +30d. These also
	// carry the canceled_at + cancel_at_period_end fields we use
	// to surface "subscription ending" state on the dashboard
	// without polling Stripe.
	switch ev.Type {
	case "customer.subscription.updated", "customer.subscription.deleted":
		s.handleStripeSubscriptionEvent(r.Context(), ev)
		s.markStripeEventProcessed(r.Context(), ev.ID)
		writeJSON(w, map[string]any{"ok": true, "applied": ev.Type}, Flags{})
		return
	case "invoice.paid":
		// F-1219 final piece (codex audit-2026-05-12): recurring
		// invoices refresh the subscription's period bounds so
		// the dashboard surface doesn't drift to the placeholder
		// +30d window the checkout.session.completed path stamped.
		s.handleStripeInvoicePaid(r.Context(), ev)
		s.markStripeEventProcessed(r.Context(), ev.ID)
		writeJSON(w, map[string]any{"ok": true, "applied": ev.Type}, Flags{})
		return
	case "checkout.session.completed":
		// handled below
	default:
		s.logger.Info("stripe webhook: ignored event type",
			"type", ev.Type, "event_id", ev.ID)
		// Mark the dedupe row as processed so a retry of this
		// (intentionally-ignored) type doesn't re-trigger.
		s.markStripeEventProcessed(r.Context(), ev.ID)
		writeJSON(w, map[string]any{"ok": true, "ignored": ev.Type}, Flags{})
		return
	}

	session := ev.Data.Object
	if session.PaymentStatus != "paid" {
		s.logger.Info("stripe webhook: checkout.session.completed but payment_status != paid",
			"event_id", ev.ID, "session_id", session.ID, "payment_status", session.PaymentStatus)
		// Mark processed — an unpaid session is a terminal verdict
		// for this event, not a retry candidate.
		s.markStripeEventProcessed(r.Context(), ev.ID)
		writeJSON(w, map[string]any{"ok": true, "ignored": "unpaid"}, Flags{})
		return
	}

	identifier := strings.TrimSpace(session.ClientReferenceID)
	if identifier == "" {
		s.logger.Error("stripe webhook: client_reference_id missing",
			"event_id", ev.ID, "session_id", session.ID, "email", session.CustomerEmail)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/stripe-missing-identifier",
			"client_reference_id missing", http.StatusBadRequest,
			"Stripe Checkout sessions must set client_reference_id to the customer's signup identifier (e.g. signup-abc123); the webhook can't route the upgrade without it")
		return
	}

	tierName := strings.ToLower(strings.TrimSpace(session.Metadata["tier"]))
	rateLimit, ok := stripeTierMap[tierName]
	if !ok {
		// Allow an explicit override via metadata.rate_limit_per_min
		// for Enterprise / custom plans.
		if override := strings.TrimSpace(session.Metadata["rate_limit_per_min"]); override != "" {
			n, err := strconv.Atoi(override)
			if err != nil || n < 0 {
				s.logger.Error("stripe webhook: bad rate_limit_per_min override",
					"event_id", ev.ID, "value", override, "err", err)
				writeProblem(w, r,
					"https://api.ratesengine.net/errors/stripe-bad-metadata",
					"Bad rate_limit_per_min metadata", http.StatusBadRequest,
					"metadata.rate_limit_per_min must be a non-negative integer")
				return
			}
			rateLimit = n
		} else {
			s.logger.Error("stripe webhook: unknown tier + no override",
				"event_id", ev.ID, "tier", tierName,
				"valid_tiers", "pro|business")
			writeProblem(w, r,
				"https://api.ratesengine.net/errors/stripe-bad-metadata",
				"Unknown tier", http.StatusBadRequest,
				"metadata.tier must be one of pro/business, OR metadata.rate_limit_per_min must be set")
			return
		}
	}

	// Upgrade every key the customer holds. This is idempotent —
	// Stripe at-least-once delivery means the same event may arrive
	// multiple times; we always set the same target rate-limit.
	keys, err := s.stripe.Manager.ListKeysForIdentifier(r.Context(), identifier)
	if err != nil {
		s.logger.Error("stripe webhook: list keys failed",
			"err", err, "identifier", identifier, "event_id", ev.ID)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/internal",
			"Internal error", http.StatusInternalServerError,
			"could not look up customer keys; Stripe will retry")
		return
	}
	if len(keys) == 0 {
		s.logger.Warn("stripe webhook: no keys for identifier (customer paid but never signed up?)",
			"identifier", identifier, "event_id", ev.ID,
			"email", session.CustomerEmail)
		// Acknowledge — there's nothing to upgrade. Operator triages
		// out-of-band (refund? ask customer to sign up?). Refusing
		// would just trigger Stripe retries. Mark processed so a
		// retry of THIS event doesn't keep firing the no-keys log
		// line — operators reconcile from the warning, not the
		// retry stream.
		s.markStripeEventProcessed(r.Context(), ev.ID)
		writeJSON(w, map[string]any{"ok": true, "upgraded": 0, "note": "no keys for identifier"}, Flags{})
		return
	}

	upgraded := s.upgradeAllKeys(r.Context(), keys, rateLimit, identifier, ev.ID)

	// F-1219 (codex audit-2026-05-12): platform-store side effects.
	// Look up the account by Stripe customer ID, upsert the
	// subscription, and bump the account tier. Best-effort: a
	// failure here is logged but does NOT 5xx the webhook —
	// Stripe retries would just keep applying the same Redis
	// rate-limit (already done above) without making the
	// platform-store path any healthier. Operators surface failures
	// via the `ratesengine_stripe_platform_sync_errors_total{operation}`
	// counter (incremented per error site) — any non-zero value is
	// alertable as "Stripe bridge degraded; customer dashboard
	// state drifting from billing state."
	s.applyPlatformSideEffects(r.Context(), ev, session, tierName)

	s.logger.Info("stripe webhook: customer upgraded",
		"identifier", identifier, "event_id", ev.ID,
		"tier", tierName, "rate_limit_per_min", rateLimit,
		"keys_total", len(keys), "keys_upgraded", upgraded)

	// F-1240: durable audit row for the tier upgrade. One row per
	// event (not per key) — the metadata carries the upgraded-key
	// count + identifier so the dashboard surface can render "the
	// upgrade happened" without N rows for a customer holding N
	// keys. Best-effort; Append errors are logged but never block
	// the webhook ack (audit-log unavailability must not turn a
	// successful Stripe upgrade into a retry storm).
	s.recordStripeUpgradeAudit(r.Context(), ev, identifier, tierName, rateLimit, len(keys), upgraded)

	// F-1227: mark the dedupe row processed so a delayed
	// re-delivery of the same event doesn't re-run the upgrade
	// after a manual operator-side downgrade. Best-effort —
	// the upgrade itself is idempotent for the same target
	// rate-limit, so a missed mark just means the next retry
	// re-applies the same value.
	s.markStripeEventProcessed(r.Context(), ev.ID)

	writeJSON(w, map[string]any{
		"ok":                 true,
		"upgraded":           upgraded,
		"keys_total":         len(keys),
		"rate_limit_per_min": rateLimit,
	}, Flags{})
}

// parseStripeWebhook handles the auth + body-shape validation for
// the Stripe webhook handler. Returns the parsed event + ok=true
// on success; on failure the response has already been written and
// ok=false signals the caller to bail. Extracted so handleStripeWebhook
// stays under the gocognit threshold.
func (s *Server) parseStripeWebhook(w http.ResponseWriter, r *http.Request) (stripeEvent, bool) {
	if s.stripe == nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/stripe-not-configured",
			"Stripe webhook not configured", http.StatusServiceUnavailable,
			"this deployment has no Stripe signing secret wired — set [api.stripe].signing_secret to enable webhooks")
		return stripeEvent{}, false
	}
	if s.stripe.SigningSecret == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/stripe-not-configured",
			"Stripe webhook signing secret is empty", http.StatusServiceUnavailable,
			"signing secret unset — webhooks rejected to prevent unauthenticated upgrades")
		return stripeEvent{}, false
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1<<20)) // 1 MiB
	if err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/body-too-large",
			"Request body too large", http.StatusBadRequest,
			"Stripe webhook body must be under 1 MiB")
		return stripeEvent{}, false
	}

	sigHeader := r.Header.Get("Stripe-Signature")
	if sigHeader == "" {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/stripe-signature-missing",
			"Stripe-Signature header missing", http.StatusBadRequest,
			"every Stripe webhook delivery must carry a Stripe-Signature header; absence implies the request didn't come from Stripe")
		return stripeEvent{}, false
	}

	if err := verifyStripeSignature(sigHeader, body, s.stripe.SigningSecret, s.stripeNow(), s.stripeMaxAge()); err != nil {
		// Cap the header preview so log lines stay sane.
		preview := sigHeader
		if len(preview) > 40 {
			preview = preview[:40]
		}
		s.logger.Warn("stripe webhook signature verification failed",
			"err", err, "signature_header", preview)
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/stripe-signature-invalid",
			"Stripe-Signature invalid", http.StatusUnauthorized,
			"signature verification failed; ensure the signing secret matches the dashboard's whsec_… value")
		return stripeEvent{}, false
	}

	var ev stripeEvent
	if err := json.Unmarshal(body, &ev); err != nil {
		writeProblem(w, r,
			"https://api.ratesengine.net/errors/invalid-body",
			"Malformed Stripe event", http.StatusBadRequest,
			"could not parse webhook body as a Stripe event")
		return stripeEvent{}, false
	}
	return ev, true
}

// handleStripeSubscriptionEvent processes customer.subscription.
// {updated,deleted} events — the F-1219 follow-up that refines
// the placeholder CurrentPeriodEnd the checkout.session.completed
// path stamps at +30d. Also wires CancelAtPeriodEnd + CanceledAt
// so the dashboard can surface "subscription ending Jan 31" or
// "subscription canceled" without polling Stripe.
//
// Idempotent — Stripe at-least-once delivery means the same event
// arrives multiple times; UpsertSubscription is keyed on
// stripe_subscription_id, so re-applying the same payload is a
// no-op. On `customer.subscription.deleted` we additionally
// downgrade the account tier back to Free.
//
// Best-effort: errors log + count but don't 5xx the webhook
// (Stripe retries don't make platform-store outages better).
// Returns without effect when Platform isn't wired.
func (s *Server) handleStripeSubscriptionEvent(ctx context.Context, ev stripeEvent) {
	if s.stripe == nil || s.stripe.Platform == nil {
		return
	}
	bridge := s.stripe.Platform
	obj := ev.Data.Object
	if obj.Customer == "" || obj.ID == "" {
		s.logger.Warn("stripe webhook: subscription event missing required fields",
			"event_id", ev.ID, "type", ev.Type)
		return
	}
	if bridge.Accounts == nil {
		return
	}
	acct, err := bridge.Accounts.GetByStripeCustomerID(ctx, obj.Customer)
	if err != nil {
		obs.StripePlatformSyncErrorsTotal.WithLabelValues("get_account").Inc()
		s.logger.Warn("stripe webhook: subscription event GetByStripeCustomerID failed",
			"event_id", ev.ID, "stripe_customer_id", obj.Customer, "err", err)
		return
	}
	if bridge.Billing != nil {
		var canceledAt time.Time
		if obj.CanceledAt > 0 {
			canceledAt = time.Unix(obj.CanceledAt, 0).UTC()
		}
		tierName := strings.ToLower(strings.TrimSpace(obj.Metadata["tier"]))
		sub := platform.Subscription{
			AccountID:            acct.ID,
			StripeSubscriptionID: obj.ID,
			Plan:                 stripePlanFromTier(tierName),
			CurrentPeriodStart:   time.Unix(obj.CurrentPeriodStart, 0).UTC(),
			CurrentPeriodEnd:     time.Unix(obj.CurrentPeriodEnd, 0).UTC(),
			CancelAtPeriodEnd:    obj.CancelAtPeriodEnd,
			CanceledAt:           canceledAt,
		}
		if err := bridge.Billing.UpsertSubscription(ctx, sub); err != nil {
			obs.StripePlatformSyncErrorsTotal.WithLabelValues("upsert_subscription").Inc()
			s.logger.Warn("stripe webhook: subscription event UpsertSubscription failed",
				"event_id", ev.ID, "account_id", acct.ID, "err", err)
		}
	}
	// Tier roll-down on subscription deletion: bump the account
	// back to Free so the F-1212 tier-clamp prevents the customer
	// from minting paid-tier keys after their plan ends.
	// `customer.subscription.updated` events with a non-active
	// status (past_due, unpaid) keep the tier intact — Stripe
	// drives a separate `customer.subscription.deleted` when the
	// plan actually terminates.
	if ev.Type == "customer.subscription.deleted" && acct.Tier != platform.TierFree {
		acct.Tier = platform.TierFree
		if err := bridge.Accounts.Update(ctx, acct); err != nil {
			obs.StripePlatformSyncErrorsTotal.WithLabelValues("account_update").Inc()
			s.logger.Warn("stripe webhook: tier downgrade-to-free failed",
				"event_id", ev.ID, "account_id", acct.ID, "err", err)
		}
	}
}

// handleStripeInvoicePaid processes invoice.paid events. Stripe
// fires this on every successful recurring charge (monthly /
// annual renewal). We use it to refresh the subscription row's
// CurrentPeriod{Start,End} so the dashboard's "renews on Feb 15"
// surface tracks the real billing cadence instead of the +30d
// placeholder the checkout.session.completed path stamped.
//
// Idempotent — Stripe at-least-once delivery means the same
// invoice.paid arrives multiple times; UpsertSubscription is
// keyed on stripe_subscription_id and re-applies the same window
// as a no-op. Best-effort: errors log + count but don't 5xx
// the webhook. Returns without effect when Platform isn't wired
// OR when the invoice doesn't reference a subscription
// (one-shot invoices for credits, manual adjustments, etc.).
//
// F-1219 final piece (codex audit-2026-05-12).
func (s *Server) handleStripeInvoicePaid(ctx context.Context, ev stripeEvent) {
	if s.stripe == nil || s.stripe.Platform == nil {
		return
	}
	bridge := s.stripe.Platform
	obj := ev.Data.Object
	// Subscription-less invoices (credits, manual charges) don't
	// affect any subscription row. Acknowledge silently.
	if obj.Subscription == "" {
		return
	}
	if obj.Customer == "" || bridge.Accounts == nil || bridge.Billing == nil {
		return
	}
	acct, err := bridge.Accounts.GetByStripeCustomerID(ctx, obj.Customer)
	if err != nil {
		obs.StripePlatformSyncErrorsTotal.WithLabelValues("get_account").Inc()
		s.logger.Warn("stripe webhook: invoice.paid GetByStripeCustomerID failed",
			"event_id", ev.ID, "stripe_customer_id", obj.Customer, "err", err)
		return
	}
	// invoice.paid carries the invoice's own period window; the
	// subscription's current period matches when this invoice is
	// the recurring renewal (the common case). period_start may
	// be zero on a fresh invoice for a just-created subscription;
	// fall through to the wall-clock minus one period gap by
	// stamping the invoice's stripeNow().
	start := time.Unix(obj.PeriodStart, 0).UTC()
	end := time.Unix(obj.PeriodEnd, 0).UTC()
	if obj.PeriodStart == 0 {
		start = s.stripeNow()
	}
	if obj.PeriodEnd == 0 {
		// Defensive: an invoice with no period_end is malformed
		// for our purposes. Log + skip rather than stamp a
		// nonsense window.
		s.logger.Warn("stripe webhook: invoice.paid has no period_end",
			"event_id", ev.ID, "subscription", obj.Subscription)
		return
	}
	tierName := strings.ToLower(strings.TrimSpace(obj.Metadata["tier"]))
	sub := platform.Subscription{
		AccountID:            acct.ID,
		StripeSubscriptionID: obj.Subscription,
		Plan:                 stripePlanFromTier(tierName),
		CurrentPeriodStart:   start,
		CurrentPeriodEnd:     end,
	}
	if err := bridge.Billing.UpsertSubscription(ctx, sub); err != nil {
		obs.StripePlatformSyncErrorsTotal.WithLabelValues("upsert_subscription").Inc()
		s.logger.Warn("stripe webhook: invoice.paid UpsertSubscription failed",
			"event_id", ev.ID, "account_id", acct.ID,
			"stripe_subscription_id", obj.Subscription, "err", err)
	}
}

// applyPlatformSideEffects runs the F-1219 (codex audit-2026-05-12)
// platform-store fan-out after a successful Redis rate-limit
// update: look up the account by Stripe customer ID, upsert the
// subscription row, and bump the account.Tier. All steps are
// best-effort — a failure is logged + counted but never 5xxs the
// webhook handler. Stripe retries would just re-apply the same
// Redis rate-limit without making the platform side any healthier;
// operator reconciliation handles the gap.
//
// Skipped silently when Platform is nil (legacy / billing-disabled
// deployments). Skipped per-step when the corresponding store is
// nil (tests commonly leave Accounts nil but wire Billing).
func (s *Server) applyPlatformSideEffects(ctx context.Context, ev stripeEvent, session stripeCheckoutSession, tierName string) {
	if s.stripe == nil || s.stripe.Platform == nil {
		return
	}
	bridge := s.stripe.Platform

	// Resolve the account from the Stripe customer ID. Empty
	// CustomerID is a Stripe-side bug (Checkout sessions always
	// carry one for a paid event) — log + bail.
	if bridge.Accounts == nil {
		s.logger.Debug("stripe webhook: platform.Accounts not wired — skipping tier update",
			"event_id", ev.ID)
	}
	var account platform.Account
	var haveAccount bool
	if bridge.Accounts != nil && session.Customer != "" {
		acct, err := bridge.Accounts.GetByStripeCustomerID(ctx, session.Customer)
		if err != nil {
			obs.StripePlatformSyncErrorsTotal.WithLabelValues("get_account").Inc()
			s.logger.Warn("stripe webhook: GetByStripeCustomerID failed",
				"event_id", ev.ID, "stripe_customer_id", session.Customer, "err", err)
		} else {
			account = acct
			haveAccount = true
		}
	}

	// UpsertSubscription on the platform billing store. Only
	// fires when we resolved the account AND the bridge has a
	// Billing store wired.
	if haveAccount && bridge.Billing != nil && session.Subscription != "" {
		now := s.stripeNow()
		plan := stripePlanFromTier(tierName)
		sub := platform.Subscription{
			AccountID:            account.ID,
			StripeSubscriptionID: session.Subscription,
			Plan:                 plan,
			// CurrentPeriodStart / CurrentPeriodEnd / CancelAtPeriodEnd
			// come from invoice.paid / customer.subscription.updated
			// events; checkout.session.completed doesn't carry them.
			// Production wiring stamps `now` + 30d as a placeholder
			// — the next subscription-update event refines.
			CurrentPeriodStart: now,
			CurrentPeriodEnd:   now.Add(30 * 24 * time.Hour),
		}
		if err := bridge.Billing.UpsertSubscription(ctx, sub); err != nil {
			obs.StripePlatformSyncErrorsTotal.WithLabelValues("upsert_subscription").Inc()
			s.logger.Warn("stripe webhook: UpsertSubscription failed",
				"event_id", ev.ID, "account_id", account.ID,
				"stripe_subscription_id", session.Subscription, "err", err)
		}
	}

	// F-1219 (codex audit-2026-05-13) follow-up: lift the
	// account-tier + every Postgres-backed dashboard key for
	// the account, so a customer who minted keys from the
	// dashboard gets the same upgrade as a customer who came
	// in via /v1/signup.
	if haveAccount {
		s.applyAccountTierAndKeyUpgrade(ctx, ev, bridge, account, tierName)
	}
}

// applyAccountTierAndKeyUpgrade runs the per-account half of
// the F-1219 platform fan-out: bump `account.Tier` if it
// differs from the new plan, then lift every active platform-
// backed API key up to the per-minute rate-limit budget the
// new tier promises. Both steps are best-effort + idempotent.
// Extracted from `applyPlatformSideEffects` to keep that
// function's cognitive complexity under the linter cap.
func (s *Server) applyAccountTierAndKeyUpgrade(ctx context.Context, ev stripeEvent, bridge *StripePlatformBridge, account platform.Account, tierName string) {
	// Update the account tier so the dashboard reflects the new
	// plan + the F-1212 tier-rate-limit clamp picks up the right
	// ceiling for future key mints.
	newTier := stripeTierMapPlatform(bridge.TierMap, tierName)
	if newTier != "" && account.Tier != newTier {
		account.Tier = newTier
		if err := bridge.Accounts.Update(ctx, account); err != nil {
			obs.StripePlatformSyncErrorsTotal.WithLabelValues("account_update").Inc()
			s.logger.Warn("stripe webhook: account tier update failed",
				"event_id", ev.ID, "account_id", account.ID, "tier", newTier, "err", err)
		}
	}
	// Lift the per-key rate-limit on Postgres-backed keys.
	// F-1219 wave 55 (codex audit-2026-05-13).
	if bridge.APIKeys != nil {
		s.upgradePlatformAPIKeys(ctx, ev, bridge.APIKeys, account, tierName)
	}
}

// upgradePlatformAPIKeys lifts every Postgres-backed dashboard
// key for `account` to the rate-limit budget the customer's new
// tier promises. F-1219 wave 55 (codex audit-2026-05-13).
//
// Idempotent: keys already at-or-above the new budget are
// skipped (so a re-delivered Stripe event doesn't downgrade a
// key the operator manually lifted further). The
// `auth.Tier.MaxRateLimitPerMin` ceiling is the source of
// truth for "the budget this tier promises".
func (s *Server) upgradePlatformAPIKeys(ctx context.Context, ev stripeEvent, store platform.APIKeyStore, account platform.Account, tierName string) {
	target := stripeTierBudget(tierName)
	if target <= 0 {
		s.logger.Debug("stripe webhook: no platform-key budget for tier; skipping per-key upgrade",
			"event_id", ev.ID, "tier", tierName)
		return
	}
	keys, err := store.ListForAccount(ctx, account.ID)
	if err != nil {
		obs.StripePlatformSyncErrorsTotal.WithLabelValues("list_keys").Inc()
		s.logger.Warn("stripe webhook: ListForAccount failed; skipping per-key upgrade",
			"event_id", ev.ID, "account_id", account.ID, "err", err)
		return
	}
	upgraded := 0
	for i := range keys {
		k := keys[i]
		if !k.RevokedAt.IsZero() {
			continue
		}
		if k.RateLimitPerMin >= target {
			continue
		}
		k.RateLimitPerMin = target
		if err := store.Update(ctx, k); err != nil {
			obs.StripePlatformSyncErrorsTotal.WithLabelValues("key_update").Inc()
			s.logger.Warn("stripe webhook: platform-key Update failed",
				"event_id", ev.ID, "account_id", account.ID,
				"key_id", k.ID, "err", err)
			continue
		}
		upgraded++
	}
	if upgraded > 0 {
		s.logger.Info("stripe webhook: lifted platform-backed dashboard keys",
			"event_id", ev.ID, "account_id", account.ID,
			"tier", tierName, "rate_limit_per_min", target,
			"keys_upgraded", upgraded)
	}
}

// stripeTierBudget returns the per-minute rate-limit ceiling
// the named Stripe metadata.tier value promises. Mirrors
// `stripeTierMap` (which carries the same ladder for the
// Redis-side upgrade) so the two upgrade paths can't drift.
func stripeTierBudget(tierName string) int {
	if v, ok := stripeTierMap[tierName]; ok {
		return v
	}
	return 0
}

// stripePlanFromTier maps the Stripe `metadata.tier` string into
// the platform billing-plan enum. Unknown tier → empty string
// (caller treats that as "skip subscription write" because the
// shape can't be honestly recorded).
func stripePlanFromTier(tierName string) platform.SubscriptionPlan {
	switch tierName {
	case "starter":
		return platform.PlanStarter
	case "pro":
		return platform.PlanPro
	case "business":
		return platform.PlanBusiness
	case "enterprise":
		return platform.PlanEnterprise
	}
	return ""
}

// stripeTierMapPlatform looks up the operator-supplied tier map
// first, then falls back to the canonical mapping. Returns empty
// when no mapping applies (caller skips the tier update).
func stripeTierMapPlatform(overrides map[string]platform.Tier, tierName string) platform.Tier {
	if overrides != nil {
		if t, ok := overrides[tierName]; ok {
			return t
		}
	}
	switch tierName {
	case "starter":
		return platform.TierStarter
	case "pro":
		return platform.TierPro
	case "business":
		return platform.TierBusiness
	case "enterprise":
		return platform.TierEnterprise
	}
	return ""
}

// upgradeAllKeys runs the per-key UpdateRateLimit loop and
// returns the count of successful upgrades. Per-key errors are
// logged + counted but don't fail the loop — partial success is
// better than triggering Stripe retries that re-attempt already-
// successful upgrades. When NO upgrade succeeds and at least one
// failed, marks the dedupe row with the first error so operators
// can find chronically-stuck events via
// `SELECT * FROM stripe_event_log WHERE error IS NOT NULL`.
// Extracted from handleStripeWebhook to keep that function under
// the gocognit threshold.
func (s *Server) upgradeAllKeys(
	ctx context.Context,
	keys []auth.APIKeyRecord,
	rateLimit int,
	identifier, eventID string,
) int {
	upgraded := 0
	var firstErr error
	for _, k := range keys {
		if _, err := s.stripe.Manager.UpdateRateLimit(ctx, k.KeyID, rateLimit); err != nil {
			s.logger.Error("stripe webhook: upgrade failed for one key",
				"err", err, "key_id", k.KeyID, "identifier", identifier, "event_id", eventID)
			if firstErr == nil {
				firstErr = fmt.Errorf("UpdateRateLimit %s: %w", k.KeyID, err)
			}
			continue
		}
		upgraded++
	}
	if upgraded == 0 && firstErr != nil {
		s.markStripeEventFailed(ctx, eventID, firstErr.Error())
	}
	return upgraded
}

// stripeDedupeOK runs the F-1227 dedupe check. Returns true when
// the handler should continue with side effects, false when it
// already wrote the response (duplicate-ack or 500). Extracted
// from handleStripeWebhook to keep that function under the
// gocognit threshold.
//
// Behaviour matrix:
//
//	Events == nil           → ok=true (legacy no-dedupe path)
//	AppendStripeEvent: nil  → ok=true (first delivery; row claimed)
//	  ↳ ErrAlreadyProcessed → ok=false (write 200 dup-ack)
//	  ↳ other error         → ok=false (write 500; Stripe retries)
func (s *Server) stripeDedupeOK(w http.ResponseWriter, r *http.Request, ev stripeEvent) bool {
	if s.stripe == nil || s.stripe.Events == nil {
		return true
	}
	err := s.stripe.Events.AppendStripeEvent(r.Context(), platform.StripeEvent{
		StripeEventID: ev.ID,
		Type:          ev.Type,
		ReceivedAt:    s.stripeNow(),
		// Payload deliberately omitted — the row is for dedupe,
		// not audit replay; the full payload lives in the Stripe
		// dashboard.
	})
	if err == nil {
		return true
	}
	if errors.Is(err, platform.ErrAlreadyProcessed) {
		s.logger.Info("stripe webhook: duplicate event acked",
			"event_id", ev.ID, "type", ev.Type)
		writeJSON(w, map[string]any{
			"ok":        true,
			"duplicate": true,
			"event_id":  ev.ID,
		}, Flags{})
		return false
	}
	s.logger.Error("stripe webhook: AppendStripeEvent failed",
		"err", err, "event_id", ev.ID)
	writeProblem(w, r,
		"https://api.ratesengine.net/errors/internal",
		"Internal error", http.StatusInternalServerError,
		"could not record event for dedupe; Stripe will retry")
	return false
}

// markStripeEventProcessed bumps processed_at on the dedupe row.
// Best-effort: a failure here just means a later retry will see
// the row with processed_at=zero and re-attempt the work — which
// the upgrade path is already idempotent against. We log loudly
// so operators can spot a chronic dedupe-store failure.
func (s *Server) markStripeEventProcessed(ctx context.Context, eventID string) {
	if s.stripe == nil || s.stripe.Events == nil || eventID == "" {
		return
	}
	if err := s.stripe.Events.MarkStripeEventProcessed(ctx, eventID); err != nil {
		s.logger.Warn("stripe webhook: MarkStripeEventProcessed failed (best-effort)",
			"err", err, "event_id", eventID)
	}
}

// recordStripeUpgradeAudit writes one audit_log row per successful
// tier upgrade. One row per event (not per key) — the metadata
// carries the upgraded-key count + identifier so the dashboard can
// render "the upgrade happened" without N rows for a customer
// holding N keys. F-1240 (audit-2026-05-12).
//
// Best-effort: a failure here is logged at WARN but never blocks
// the webhook ack. Audit-log unavailability MUST NOT turn a
// successful Stripe upgrade into a retry storm.
func (s *Server) recordStripeUpgradeAudit(
	ctx context.Context,
	ev stripeEvent,
	identifier, tier string,
	rateLimit, keysTotal, keysUpgraded int,
) {
	if s.stripe == nil || s.stripe.Audit == nil {
		return
	}
	meta, err := json.Marshal(map[string]any{
		"identifier":         identifier,
		"tier":               tier,
		"rate_limit_per_min": rateLimit,
		"keys_total":         keysTotal,
		"keys_upgraded":      keysUpgraded,
		"stripe_event_id":    ev.ID,
		"stripe_event_type":  ev.Type,
	})
	if err != nil {
		// Unreachable — map[string]any of primitives never fails to
		// marshal. Surface as WARN if it ever does.
		s.logger.Warn("stripe webhook: audit metadata marshal failed (skipping audit row)",
			"err", err, "event_id", ev.ID)
		return
	}
	entry := platform.AuditEntry{
		ActorKind:  platform.ActorWebhook,
		Action:     "plan.upgrade",
		TargetKind: "stripe_event",
		TargetID:   ev.ID,
		Metadata:   meta,
		Timestamp:  s.stripeNow(),
	}
	if err := s.stripe.Audit.Append(ctx, entry); err != nil {
		s.logger.Warn("stripe webhook: audit append failed (best-effort)",
			"err", err, "event_id", ev.ID, "identifier", identifier)
	}
}

// markStripeEventFailed records the error on the dedupe row so
// operators can see why this event keeps re-processing without
// completing. Best-effort like markStripeEventProcessed — the
// next retry will hit the same code path either way.
func (s *Server) markStripeEventFailed(ctx context.Context, eventID, msg string) {
	if s.stripe == nil || s.stripe.Events == nil || eventID == "" {
		return
	}
	if err := s.stripe.Events.MarkStripeEventFailed(ctx, eventID, msg); err != nil {
		s.logger.Warn("stripe webhook: MarkStripeEventFailed failed (best-effort)",
			"err", err, "event_id", eventID)
	}
}

// stripeNow returns the configured clock or time.Now.
func (s *Server) stripeNow() time.Time {
	if s.stripe != nil && s.stripe.Now != nil {
		return s.stripe.Now()
	}
	return time.Now()
}

func (s *Server) stripeMaxAge() time.Duration {
	if s.stripe != nil && s.stripe.MaxAge > 0 {
		return s.stripe.MaxAge
	}
	return 5 * time.Minute
}

// verifyStripeSignature implements the documented Stripe webhook
// signature scheme:
//
//	Stripe-Signature: t=<unix-ts>,v1=<hex(hmac-sha256(secret, "<ts>.<body>"))>
//
// Per https://docs.stripe.com/webhooks#verify-events. Multiple
// `v1=` entries can appear (Stripe rolls signing secrets); we
// accept if ANY matches.
func verifyStripeSignature(header string, body []byte, secret string, now time.Time, maxAge time.Duration) error {
	var ts int64
	var sigs []string
	for _, part := range strings.Split(header, ",") {
		k, v, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok {
			continue
		}
		switch k {
		case "t":
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return fmt.Errorf("malformed timestamp: %w", err)
			}
			ts = n
		case "v1":
			sigs = append(sigs, v)
		}
	}
	if ts == 0 {
		return errors.New("missing t= timestamp")
	}
	if len(sigs) == 0 {
		return errors.New("missing v1= signature")
	}

	// Replay protection: reject anything outside the maxAge window.
	signedAt := time.Unix(ts, 0)
	skew := now.Sub(signedAt)
	if skew < 0 {
		skew = -skew
	}
	if skew > maxAge {
		return fmt.Errorf("timestamp drift %s exceeds %s", skew, maxAge)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(strconv.FormatInt(ts, 10)))
	mac.Write([]byte("."))
	mac.Write(body)
	want := hex.EncodeToString(mac.Sum(nil))

	for _, got := range sigs {
		if hmac.Equal([]byte(got), []byte(want)) {
			return nil
		}
	}
	return errors.New("no v1= matched expected HMAC")
}
