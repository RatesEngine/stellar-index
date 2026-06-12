package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	_ "github.com/lib/pq" // postgres driver (ADR-0006)

	"github.com/StellarAtlas/stellar-atlas/internal/config"
	"github.com/StellarAtlas/stellar-atlas/internal/customerwebhook"
	"github.com/StellarAtlas/stellar-atlas/internal/incidents"
	"github.com/StellarAtlas/stellar-atlas/internal/platform"
	"github.com/StellarAtlas/stellar-atlas/internal/platform/postgresstore"
)

// findIncidentForEmit locates the incident named by `slug` in the
// embedded corpus and validates that emitting `eventType` makes
// sense given the incident's current frontmatter status/severity.
//
// Refusals are operator-friendly: the most common finger-trouble
// (emitting `resolved` against a still-firing incident; emitting
// `sev1` against a non-SEV-1 or already-resolved entry) is caught
// before any network I/O so the operator can re-check their
// inputs rather than chase a downstream silent fan-out.
func findIncidentForEmit(all []incidents.Incident, slug string, eventType platform.WebhookEventType) (*incidents.Incident, error) {
	var found *incidents.Incident
	for i := range all {
		if all[i].Slug == slug {
			found = &all[i]
			break
		}
	}
	if found == nil {
		return nil, fmt.Errorf("no incident with slug %q in the embedded corpus — add internal/incidents/data/%s.md and rebuild", slug, slug)
	}
	switch eventType {
	case platform.WebhookEventIncidentResolved:
		if found.Status != incidents.StatusResolved {
			return nil, fmt.Errorf("incident %q status is %q in the corpus — set frontmatter status=resolved and resolved_at before emitting `resolved`", slug, found.Status)
		}
	case platform.WebhookEventIncidentSEV1:
		if found.Status == incidents.StatusResolved {
			return nil, fmt.Errorf("incident %q is already resolved in the corpus — emit `resolved` instead of `sev1`", slug)
		}
		if found.Severity != incidents.SeverityMajor {
			return nil, fmt.Errorf("incident %q severity is %q in the corpus — only SEV-1 incidents emit incident.sev1", slug, found.Severity)
		}
	}
	return found, nil
}

// emitIncident fans out an `incident.sev1` or `incident.resolved`
// webhook to every subscribed dashboard hook for the given slug.
//
// F-1249 (codex audit-2026-05-12): pre-fix the platform shipped
// dashboard CRUD for `incident.sev1` / `incident.resolved`
// subscriptions but no production caller enqueued deliveries.
// Status-page content (the source of truth for incidents) lives
// in `internal/incidents/data/*.md` and is embedded at build-time,
// so there is no in-process "state transition" to hook from.
// Instead we expose an operator-triggered emit step that's part
// of the documented SEV runbook:
//
//  1. Operator drafts `internal/incidents/data/<slug>.md`
//     (status=investigating, severity=SEV-1, no resolved_at).
//  2. Operator merges + redeploys the API/aggregator binaries.
//  3. Operator runs `ratesengine-ops emit-incident -slug <slug>
//     -event sev1`.
//  4. When the SEV closes, operator updates the .md to
//     status=resolved with a `resolved_at` stamp.
//  5. Operator redeploys + runs `ratesengine-ops emit-incident
//     -slug <slug> -event resolved`.
//
// Best-effort: the underlying `customerwebhook.Fanout.Publish`
// logs and drops per-subscriber failures. The command returns
// non-zero only on hard inputs errors (bad slug, no Postgres,
// missing config). A zero-subscriber fan-out is a successful
// no-op — informational stderr line only.
//
// Usage:
//
//	ratesengine-ops emit-incident \
//	  -config /etc/ratesengine.toml \
//	  -slug 2026-05-12-redis-blip \
//	  -event sev1
//
// `-event` accepts `sev1` and `resolved` as ergonomic aliases for
// the wire-level event names `incident.sev1` and
// `incident.resolved`.
func emitIncident(args []string) error {
	fs := flag.NewFlagSet("emit-incident", flag.ContinueOnError)
	cfgPath := fs.String("config", "", "Path to TOML config file (required)")
	slug := fs.String("slug", "",
		"Incident slug — matches the filename in internal/incidents/data/ minus .md (required)")
	event := fs.String("event", "",
		"`sev1` or `resolved` (required) — emits incident.sev1 or incident.resolved")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *cfgPath == "" {
		return errors.New("-config is required")
	}
	if strings.TrimSpace(*slug) == "" {
		return errors.New("-slug is required")
	}

	var eventType platform.WebhookEventType
	switch strings.ToLower(strings.TrimSpace(*event)) {
	case "sev1", "incident.sev1":
		eventType = platform.WebhookEventIncidentSEV1
	case "resolved", "incident.resolved":
		eventType = platform.WebhookEventIncidentResolved
	default:
		return fmt.Errorf("-event must be `sev1` or `resolved` (got %q)", *event)
	}

	cfg, err := config.LoadWithEnv(*cfgPath)
	if err != nil {
		return err
	}
	if strings.TrimSpace(cfg.Storage.PostgresDSN) == "" {
		return errors.New("storage.postgres_dsn is empty — webhook fan-out requires Postgres")
	}

	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))

	// Load the incident corpus and find the slug. Going through
	// `incidents.Load` keeps the payload aligned with what the
	// dashboard explorer + /v1/incidents API render, so a hook
	// subscriber sees the same title/severity/components as the
	// public status page.
	all, err := incidents.Load(logger)
	if err != nil {
		return fmt.Errorf("load incidents: %w", err)
	}
	found, err := findIncidentForEmit(all, *slug, eventType)
	if err != nil {
		return err
	}

	db, err := sql.Open("postgres", cfg.Storage.PostgresDSN)
	if err != nil {
		return fmt.Errorf("postgres open: %w", err)
	}
	defer func() { _ = db.Close() }()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("postgres ping: %w", err)
	}

	store := postgresstore.NewWebhookStore(postgresstore.New(db))
	fanout := customerwebhook.NewFanout(store, logger.With("component", "webhook-fanout"))
	if fanout == nil {
		return errors.New("webhook fan-out not configured (store ctor returned nil)")
	}

	payloadFields := map[string]any{
		"event":               string(eventType),
		"slug":                found.Slug,
		"title":               found.Title,
		"severity":            string(found.Severity),
		"status":              string(found.Status),
		"started_at":          found.StartedAt.UTC().Format(time.RFC3339Nano),
		"affected_components": found.AffectedComponents,
		"at":                  time.Now().UTC().Format(time.RFC3339Nano),
	}
	if found.ResolvedAt != nil {
		payloadFields["resolved_at"] = found.ResolvedAt.UTC().Format(time.RFC3339Nano)
	}
	if found.PostmortemRef != "" {
		payloadFields["postmortem"] = found.PostmortemRef
	}
	payload := customerwebhook.MarshalPayload(logger, payloadFields)
	if payload == nil {
		return errors.New("payload marshal failed (see WARN log above)")
	}

	// Count subscribers up-front so the operator gets a deterministic
	// stderr line — Fanout.Publish itself is intentionally quiet on
	// the no-subscriber path (the freeze + divergence hot paths fire
	// constantly and shouldn't log per-event when nobody's listening).
	subs, err := store.ListWebhooksSubscribedTo(ctx, eventType)
	if err != nil {
		return fmt.Errorf("list subscribers: %w", err)
	}

	fanout.Publish(ctx, eventType, payload)

	fmt.Fprintf(os.Stderr, "emit-incident: event=%s slug=%s subscribers=%d\n",
		eventType, *slug, len(subs))
	if len(subs) == 0 {
		fmt.Fprintln(os.Stderr, "emit-incident: no dashboard hooks subscribed — fan-out was a no-op")
	}
	return nil
}
