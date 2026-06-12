package dashboardwebhooks

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/StellarAtlas/stellar-atlas/internal/api/v1/dashboardauth"
	"github.com/StellarAtlas/stellar-atlas/internal/platform"
)

// MaxWebhooksPerAccount caps how many endpoints one account can
// register. Tier-aware quotas can replace this once billing is
// wired (Phase 2); flat 10 prevents an enthusiastic operator from
// minting hundreds.
const MaxWebhooksPerAccount = 10

// Config wires the handlers' dependencies.
type Config struct {
	// Webhooks is the platform store powering CRUD + queue. In
	// production: `internal/platform/postgresstore.WebhookStore`.
	Webhooks platform.WebhookStore
	Logger   *slog.Logger
	Now      func() time.Time
}

func (c *Config) validate() error {
	if c.Webhooks == nil {
		return errors.New("dashboardwebhooks: Webhooks store is required")
	}
	if c.Logger == nil {
		c.Logger = slog.Default()
	}
	if c.Now == nil {
		c.Now = func() time.Time { return time.Now().UTC() }
	}
	return nil
}

// Handlers exposes the routes to be mounted in the v1 mux.
type Handlers struct{ cfg *Config }

// NewHandlers validates the config and returns a mount-ready
// Handlers.
func NewHandlers(cfg Config) (*Handlers, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	return &Handlers{cfg: &cfg}, nil
}

// Mount installs the dashboard webhook-management routes.
func (h *Handlers) Mount(mux *http.ServeMux) {
	mux.HandleFunc("GET /v1/dashboard/webhooks", h.HandleList)
	mux.HandleFunc("POST /v1/dashboard/webhooks", h.HandleCreate)
	mux.HandleFunc("PATCH /v1/dashboard/webhooks/{id}", h.HandleUpdate)
	mux.HandleFunc("DELETE /v1/dashboard/webhooks/{id}", h.HandleDelete)
	mux.HandleFunc("GET /v1/dashboard/webhooks/{id}/deliveries", h.HandleListDeliveries)
}

// webhookDTO is the wire shape the dashboard reads.
//
// F-1244 (codex audit-2026-05-13): the prior docstring claimed
// "the plaintext is shown to the customer once at create time +
// never persisted." That mixed two distinct properties: the
// plaintext IS persisted as the canonical
// `customer_webhooks.secret_hash` bytea (the delivery worker
// needs the same bytes to sign requests). The "shown once"
// property is API-surface visibility — the plaintext is
// returned by `POST /v1/dashboard/webhooks` exactly once and
// never served back through any subsequent read. SecretHash
// stays out of this DTO so the dashboard can't accidentally
// re-expose the bytes; rotation happens by delete + recreate.
// See [platform.CustomerWebhook] for the at-rest model.
type webhookDTO struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	URL       string    `json:"url"`
	Events    []string  `json:"events"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

func toDTO(w platform.CustomerWebhook) webhookDTO {
	return webhookDTO{
		ID:        w.ID.String(),
		Name:      w.Name,
		URL:       w.URL,
		Events:    w.Events,
		Enabled:   w.Enabled,
		CreatedAt: w.CreatedAt,
		UpdatedAt: w.UpdatedAt,
	}
}

type deliveryDTO struct {
	ID                 string    `json:"id"`
	EventType          string    `json:"event_type"`
	AttemptCount       int       `json:"attempt_count"`
	NextAttemptAt      time.Time `json:"next_attempt_at,omitempty"`
	DeliveredAt        time.Time `json:"delivered_at,omitempty"`
	LastError          string    `json:"last_error,omitempty"`
	LastResponseStatus int       `json:"last_response_status,omitempty"`
	CreatedAt          time.Time `json:"created_at"`
}

func toDeliveryDTO(d platform.WebhookDelivery) deliveryDTO {
	return deliveryDTO{
		ID:                 d.ID.String(),
		EventType:          d.EventType,
		AttemptCount:       d.AttemptCount,
		NextAttemptAt:      d.NextAttemptAt,
		DeliveredAt:        d.DeliveredAt,
		LastError:          d.LastError,
		LastResponseStatus: d.LastResponseStatus,
		CreatedAt:          d.CreatedAt,
	}
}

type listResponse struct {
	Webhooks []webhookDTO `json:"webhooks"`
}

// HandleList returns every webhook for the session's account,
// newest first.
func (h *Handlers) HandleList(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboardauth.SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", r.URL.Path)
		return
	}
	hooks, err := h.cfg.Webhooks.ListWebhooksForAccount(r.Context(), sc.Account.ID)
	if err != nil {
		h.cfg.Logger.Error("list webhooks", "err", err, "account_id", sc.Account.ID)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	out := listResponse{Webhooks: make([]webhookDTO, 0, len(hooks))}
	for _, hk := range hooks {
		out.Webhooks = append(out.Webhooks, toDTO(hk))
	}
	writeJSON(w, http.StatusOK, out)
}

type createRequest struct {
	Name    string   `json:"name"`
	URL     string   `json:"url"`
	Events  []string `json:"events"`
	Enabled *bool    `json:"enabled,omitempty"` // pointer so absent → true default
}

type createResponse struct {
	Webhook webhookDTO `json:"webhook"`
	// Secret is the HMAC-SHA-256 signing key plaintext, returned
	// once at create + never again. The customer stores it
	// server-side + uses it to verify the X-RatesEngine-Signature
	// header on inbound webhook POSTs.
	Secret string `json:"secret"`
}

// HandleCreate registers a new webhook endpoint.
func (h *Handlers) HandleCreate(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboardauth.SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", r.URL.Path)
		return
	}
	if !canManage(sc.User.Role) {
		writeProblem(w, http.StatusForbidden, "your role can't manage webhooks", r.URL.Path)
		return
	}

	req, status, problem := parseCreateRequest(r.Context(), r)
	if problem != "" {
		writeProblem(w, status, problem, r.URL.Path)
		return
	}

	// F-1248 (codex audit-2026-05-12): the handler used to do a
	// `ListWebhooksForAccount` precheck here before the insert,
	// which was raceable — N parallel HandleCreate at-the-limit
	// requests could each pass the precheck. The store now
	// enforces `maxPerAccount` atomically inside the INSERT, so
	// the precheck only remains as a fast-path UX nicety: it
	// surfaces the same 409 message without spending a write
	// budget. The atomic insert is the actual gate.
	if status, problem := h.checkQuota(r, sc.Account.ID); problem != "" {
		writeProblem(w, status, problem, r.URL.Path)
		return
	}

	secret, err := generateSecret()
	if err != nil {
		h.cfg.Logger.Error("generate webhook secret", "err", err)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}

	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	rec := platform.CustomerWebhook{
		AccountID: sc.Account.ID,
		Name:      req.Name,
		URL:       req.URL,
		// SecretHash is the HMAC signing key, not a hash. See
		// [platform.CustomerWebhook] doc — the field name is a
		// historical artefact tracked by F-1244 (codex
		// audit-2026-05-12). The customer receives the plaintext
		// `secret` exactly once in the response below and never
		// again; rotation = delete + recreate.
		SecretHash: []byte(secret),
		Events:     req.Events,
		Enabled:    enabled,
	}
	out, err := h.cfg.Webhooks.CreateWebhook(r.Context(), rec, MaxWebhooksPerAccount)
	if err != nil {
		// F-1248: race-window loser. The atomic gate inside
		// CreateWebhook returns ErrWebhookQuotaExceeded when the
		// account hits the cap between the precheck and the
		// INSERT.
		if errors.Is(err, platform.ErrWebhookQuotaExceeded) {
			writeProblem(w, http.StatusConflict,
				fmt.Sprintf("account already has %d webhooks (max %d)", MaxWebhooksPerAccount, MaxWebhooksPerAccount),
				r.URL.Path)
			return
		}
		h.cfg.Logger.Error("create webhook in postgres", "err", err, "account_id", sc.Account.ID)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	writeJSON(w, http.StatusCreated, createResponse{Webhook: toDTO(out), Secret: secret})
}

type updateRequest struct {
	Name    *string  `json:"name,omitempty"`
	URL     *string  `json:"url,omitempty"`
	Events  []string `json:"events,omitempty"`
	Enabled *bool    `json:"enabled,omitempty"`
}

// HandleUpdate patches mutable fields. SecretHash + AccountID are
// immutable; rotation lives behind a separate endpoint when it
// lands.
func (h *Handlers) HandleUpdate(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboardauth.SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", r.URL.Path)
		return
	}
	if !canManage(sc.User.Role) {
		writeProblem(w, http.StatusForbidden, "your role can't manage webhooks", r.URL.Path)
		return
	}
	id, ok := parseAndAuthorise(w, r, h, sc.Account.ID)
	if !ok {
		return
	}
	current, err := h.cfg.Webhooks.GetWebhook(r.Context(), id)
	if err != nil {
		// Should never happen — parseAndAuthorise just looked it
		// up — but guard anyway.
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 8<<10))
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "request body too large", r.URL.Path)
		return
	}
	var req updateRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeProblem(w, http.StatusBadRequest, "invalid JSON: "+err.Error(), r.URL.Path)
		return
	}

	if req.Name != nil {
		current.Name = *req.Name
	}
	if req.URL != nil {
		if err := validateWebhookURL(r.Context(), *req.URL); err != nil {
			writeProblem(w, http.StatusBadRequest, err.Error(), r.URL.Path)
			return
		}
		current.URL = *req.URL
	}
	if len(req.Events) > 0 {
		if err := validateEvents(req.Events); err != nil {
			writeProblem(w, http.StatusBadRequest, err.Error(), r.URL.Path)
			return
		}
		current.Events = req.Events
	}
	if req.Enabled != nil {
		current.Enabled = *req.Enabled
	}

	if err := h.cfg.Webhooks.UpdateWebhook(r.Context(), current); err != nil {
		h.cfg.Logger.Error("update webhook", "err", err, "id", id)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	updated, _ := h.cfg.Webhooks.GetWebhook(r.Context(), id)
	writeJSON(w, http.StatusOK, toDTO(updated))
}

// HandleDelete removes the webhook + cascades to deliveries.
// Idempotent — deleting an absent ID returns 204.
func (h *Handlers) HandleDelete(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboardauth.SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", r.URL.Path)
		return
	}
	if !canManage(sc.User.Role) {
		writeProblem(w, http.StatusForbidden, "your role can't manage webhooks", r.URL.Path)
		return
	}
	id, ok := parseAndAuthorise(w, r, h, sc.Account.ID)
	if !ok {
		return
	}
	if err := h.cfg.Webhooks.DeleteWebhook(r.Context(), id); err != nil {
		h.cfg.Logger.Error("delete webhook", "err", err, "id", id)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

type deliveriesResponse struct {
	Deliveries []deliveryDTO `json:"deliveries"`
}

// HandleListDeliveries returns recent attempts for one webhook.
func (h *Handlers) HandleListDeliveries(w http.ResponseWriter, r *http.Request) {
	sc, ok := dashboardauth.SessionFromContext(r.Context())
	if !ok {
		writeProblem(w, http.StatusUnauthorized, "authentication required", r.URL.Path)
		return
	}
	id, ok := parseAndAuthorise(w, r, h, sc.Account.ID)
	if !ok {
		return
	}
	const defaultLimit = 100
	deliveries, err := h.cfg.Webhooks.ListDeliveries(r.Context(), id, defaultLimit)
	if err != nil {
		h.cfg.Logger.Error("list deliveries", "err", err, "id", id)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return
	}
	out := deliveriesResponse{Deliveries: make([]deliveryDTO, 0, len(deliveries))}
	for _, d := range deliveries {
		out.Deliveries = append(out.Deliveries, toDeliveryDTO(d))
	}
	writeJSON(w, http.StatusOK, out)
}

// ─── helpers ────────────────────────────────────────────────────

func canManage(role platform.Role) bool {
	switch role {
	case platform.RoleOwner, platform.RoleAdmin, platform.RoleMember:
		return true
	default:
		return false
	}
}

// parseAndAuthorise extracts the {id} path value, scopes it to the
// session's account (404 otherwise — don't leak presence). On
// failure writes the response and returns ok=false.
func parseAndAuthorise(w http.ResponseWriter, r *http.Request, h *Handlers, accountID uuid.UUID) (uuid.UUID, bool) {
	raw := r.PathValue("id")
	if raw == "" {
		writeProblem(w, http.StatusBadRequest, "missing id", r.URL.Path)
		return uuid.Nil, false
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		writeProblem(w, http.StatusBadRequest, "id is not a valid uuid", r.URL.Path)
		return uuid.Nil, false
	}
	current, err := h.cfg.Webhooks.GetWebhook(r.Context(), id)
	if err != nil {
		if errors.Is(err, platform.ErrNotFound) {
			writeProblem(w, http.StatusNotFound, "webhook not found", r.URL.Path)
			return uuid.Nil, false
		}
		h.cfg.Logger.Error("get webhook", "err", err, "id", id)
		writeProblem(w, http.StatusInternalServerError, "internal error", r.URL.Path)
		return uuid.Nil, false
	}
	if current.AccountID != accountID {
		// Don't leak existence — same wire shape as not-found.
		writeProblem(w, http.StatusNotFound, "webhook not found", r.URL.Path)
		return uuid.Nil, false
	}
	return id, true
}

func parseCreateRequest(ctx context.Context, r *http.Request) (createRequest, int, string) {
	body, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, 8<<10))
	if err != nil {
		return createRequest{}, http.StatusBadRequest, "request body too large (max 8 KiB)"
	}
	var req createRequest
	if err := json.Unmarshal(body, &req); err != nil {
		return createRequest{}, http.StatusBadRequest, "invalid JSON: " + err.Error()
	}
	req.Name = strings.TrimSpace(req.Name)
	req.URL = strings.TrimSpace(req.URL)
	if req.Name == "" || len(req.Name) > 200 {
		return createRequest{}, http.StatusBadRequest, "name must be 1–200 chars"
	}
	if err := validateWebhookURL(ctx, req.URL); err != nil {
		return createRequest{}, http.StatusBadRequest, err.Error()
	}
	if err := validateEvents(req.Events); err != nil {
		return createRequest{}, http.StatusBadRequest, err.Error()
	}
	return req, 0, ""
}

func validateWebhookURL(ctx context.Context, raw string) error {
	if raw == "" {
		return errors.New("url is required")
	}
	if !strings.HasPrefix(raw, "https://") {
		return errors.New("url must start with https:// (TLS required for HMAC integrity)")
	}
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("url is malformed: %w", err)
	}
	// F-1245 (codex audit-2026-05-12): SSRF defence-in-depth.
	// Reject embedded credentials, non-https schemes (already
	// caught above but defensive), and resolve the hostname to
	// confirm it isn't in a private / loopback / link-local /
	// reserved range. DNS-rebinding is also countered at delivery
	// time in the worker's dial-control hook.
	if u.User != nil {
		return errors.New("url must not embed userinfo")
	}
	if u.Hostname() == "" {
		return errors.New("url must have a hostname")
	}
	if err := rejectInternalHost(ctx, u.Hostname()); err != nil {
		return err
	}
	return nil
}

// rejectInternalHost resolves `host` and returns a non-nil error
// if any resolved address is in a non-public range. Used at
// registration time; the delivery worker performs the same check
// at send time to defeat DNS rebinding (the resolution can change
// between when the URL is saved and when the callback fires).
//
// Hostnames in RFC 2606 / RFC 6761 reserved TLDs (.example, .test,
// .invalid, .localhost) bypass the resolution check at registration
// — those names are guaranteed not to resolve to real
// infrastructure, and the delivery worker will reject them at send
// time when name resolution genuinely fails. This keeps tests
// terse without weakening the production check.
func rejectInternalHost(parent context.Context, host string) error {
	if isReservedTLD(host) {
		return nil
	}
	// Literal IP gets checked directly; named host gets resolved.
	if ip := net.ParseIP(host); ip != nil {
		if isInternalIP(ip) {
			return fmt.Errorf("url host %q resolves to an internal address — webhook destinations must be publicly routable", host)
		}
		return nil
	}
	ctx, cancel := context.WithTimeout(parent, 3*time.Second)
	defer cancel()
	addrs, lookupErr := net.DefaultResolver.LookupIPAddr(ctx, host)
	if lookupErr != nil {
		// Registration tolerates "doesn't resolve right now" so a
		// temporary DNS hiccup doesn't reject an otherwise-valid
		// URL. The delivery worker re-resolves at send time and
		// will surface the failure as a delivery error then.
		return nil //nolint:nilerr // intentional: tolerate transient DNS at registration
	}
	for _, ipa := range addrs {
		if isInternalIP(ipa.IP) {
			return fmt.Errorf("url host %q resolves to an internal address (%s) — webhook destinations must be publicly routable", host, ipa.IP.String())
		}
	}
	return nil
}

// isReservedTLD reports whether `host` ends in an RFC 2606 /
// RFC 6761 reserved TLD that's guaranteed not to resolve to real
// infrastructure. Case-insensitive; matches the TLD suffix only.
func isReservedTLD(host string) bool {
	h := strings.ToLower(host)
	for _, tld := range []string{".example", ".test", ".invalid", ".localhost"} {
		if h == tld[1:] || strings.HasSuffix(h, tld) {
			return true
		}
	}
	return false
}

// isInternalIP reports whether `ip` is in any range that customer
// webhooks must NOT target — loopback, link-local, private
// (RFC1918), unique-local IPv6 (RFC4193), multicast, or unspecified.
// The IP-block list is deliberately conservative; expand only with
// security review.
func isInternalIP(ip net.IP) bool {
	if ip == nil {
		return true
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() ||
		ip.IsMulticast() || ip.IsUnspecified() || ip.IsPrivate() {
		return true
	}
	// IPv4 carrier-grade NAT space (100.64.0.0/10) — RFC6598, not
	// covered by IsPrivate.
	if v4 := ip.To4(); v4 != nil {
		if v4[0] == 100 && (v4[1]&0xC0) == 64 {
			return true
		}
		// 169.254.169.254 cloud metadata is already IsLinkLocalUnicast.
		// 0.0.0.0/8 reserved.
		if v4[0] == 0 {
			return true
		}
	}
	return false
}

// validEventTypes pins the closed event set the worker fans out.
// Mirrors the constants in `internal/platform/webhook.go` —
// keeping the list local to the handler avoids importing the
// constants for a value comparison.
var validEventTypes = map[string]struct{}{
	string(platform.WebhookEventIncidentSEV1):     {},
	string(platform.WebhookEventIncidentResolved): {},
	string(platform.WebhookEventAnomalyFreeze):    {},
	string(platform.WebhookEventDivergenceFiring): {},
}

func validateEvents(events []string) error {
	if len(events) == 0 {
		return errors.New("events must contain at least one entry")
	}
	for _, e := range events {
		if _, ok := validEventTypes[e]; !ok {
			return fmt.Errorf("event %q is not in the supported set "+
				"(incident.sev1, incident.resolved, anomaly.freeze, divergence.firing)", e)
		}
	}
	return nil
}

func (h *Handlers) checkQuota(r *http.Request, accountID uuid.UUID) (int, string) {
	hooks, err := h.cfg.Webhooks.ListWebhooksForAccount(r.Context(), accountID)
	if err != nil {
		h.cfg.Logger.Error("checkQuota: list webhooks", "err", err, "account_id", accountID)
		return http.StatusInternalServerError, "internal error"
	}
	if len(hooks) >= MaxWebhooksPerAccount {
		return http.StatusConflict, fmt.Sprintf("account already has %d webhooks (max %d)", len(hooks), MaxWebhooksPerAccount)
	}
	return 0, ""
}

// generateSecret mints a 32-byte URL-safe secret returned once to
// the customer. They store it server-side and use it to verify
// the X-RatesEngine-Signature header on inbound POSTs.
func generateSecret() (string, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return "", fmt.Errorf("read entropy: %w", err)
	}
	// Hex avoids URL-safe-base64 padding edge cases for customers
	// who store the secret in a config file.
	return "wsec_" + hex.EncodeToString(buf[:]), nil
}

// ─── response helpers (mirror dashboardkeys' pattern) ──────────

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeProblem(w http.ResponseWriter, status int, detail, instance string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"type":     "https://api.ratesengine.net/errors/dashboard",
		"title":    http.StatusText(status),
		"status":   status,
		"detail":   detail,
		"instance": instance,
	})
}

// shaPlaceholder keeps crypto/sha256 import live in case future
// rotation logic lands here. (Rotation today is a stub on the
// store side per WebhookStore.RotateWebhookSecret.)
var _ = sha256.New
