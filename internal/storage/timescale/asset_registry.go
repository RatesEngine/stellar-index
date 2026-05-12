package timescale

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// assetRegistryDedupeTTL throttles per-asset upserts so the
// classic_assets row's `last_seen_*` + `observation_count` keep
// advancing while still bounding DB pressure. F-1243 (codex
// audit-2026-05-12): the prior `sync.Map` of asset_id → struct{}
// short-circuited every subsequent trade in the same process,
// leaving the row frozen at first observation. A coarse TTL
// caps the upsert rate to one per asset per window while
// guaranteeing the row advances under sustained trading.
//
// 60 seconds is generous enough to keep the indexer hot path
// out of the registry table (one upsert per asset per minute
// is trivial Postgres load even at 1000s of assets) and tight
// enough that the dashboard's "last seen" column always reflects
// activity within the last minute.
const assetRegistryDedupeTTL = 60 * time.Second

// assetRegistryDedupe is a process-lifetime cache of (asset_id →
// last successful upsert time). The next trade for the same
// asset within `assetRegistryDedupeTTL` skips the DB round-trip;
// trades outside the window upsert again so `last_seen_*` +
// `observation_count` advance. F-1243 (codex audit-2026-05-12).
//
// Process-lifetime is intentional: the indexer restarts often
// enough that we'll re-touch the row periodically. A persistent
// cross-process cache (Redis) would be over-engineering — the
// upsert is idempotent and the TTL bounds the worst-case load.
var assetRegistryDedupe sync.Map // key: asset_id (string) → time.Time (last upsert)

// issuerRegistryDedupe stays as a process-lifetime sentinel
// cache — the issuers table has no `last_seen` columns so
// there's nothing to advance after the first INSERT. The
// per-row DDL difference from classic_assets is documented in
// migrations 0023 (classic_assets) vs 0022 (issuers).
var issuerRegistryDedupe sync.Map

// registerClassicAssetSeen ensures a `classic_assets` row exists
// for the supplied classic asset, with last_seen_* bumped to the
// trade's ledger + timestamp and observation_count incremented.
//
// Hooked into InsertTrade after the trades-table INSERT succeeds.
// Per migration 0023's docblock, classic_assets is supposed to be
// "auto-populated by an observer (Phase 4) that hooks every trade
// + every ChangeTrust op + every payment-crossing-an-issuer op".
// This is the trade-side half — the only path the indexer reliably
// hits. ChangeTrust + payment hooks land later if needed.
//
// Returns nil error on any of: success, no-op (asset is non-
// classic), already-deduped within this process. Errors are
// logged-and-swallowed at the caller so a registry write failure
// can't sink the trade insert.
func (s *Store) registerClassicAssetSeen(
	ctx context.Context,
	asset canonical.Asset,
	ledger uint32,
	observedAt time.Time,
) error {
	if asset.Type != canonical.AssetClassic {
		return nil
	}
	assetID := asset.String()
	// F-1243 (codex audit-2026-05-12): TTL-based dedupe. The
	// prior `sync.Map` of bare sentinels froze the row at first
	// observation; now we only skip the upsert when the last
	// successful one was within `assetRegistryDedupeTTL`. Out-of-
	// window trades fire the upsert again so `last_seen_*` and
	// `observation_count` advance.
	if shouldSkipAssetRegistryUpsert(assetID, time.Now()) {
		return nil
	}

	// Issuer first — issuers row has no FK from classic_assets but
	// keeping the order consistent makes it easier to reason about
	// race-free reads from the API: every classic_assets row has a
	// matching issuers row by the time it's queryable.
	if err := s.registerIssuerSeen(ctx, asset.Issuer); err != nil {
		return err
	}

	// first_seen_* uses LEAST so chunked / parallel backfill that
	// processes ledgers out of order cannot leave a higher value
	// behind. Without this, replaying an older window after the
	// row already exists would leave first_seen_ledger pinned at
	// the original (later) ledger — wrong by definition. F-1239.
	const q = `
		INSERT INTO classic_assets (
			asset_id, code, issuer_g_strkey, slug,
			first_seen_at, first_seen_ledger,
			last_seen_at,  last_seen_ledger,
			observation_count
		) VALUES (
			$1, $2, $3, NULL,
			$4, $5, $4, $5, 1
		)
		ON CONFLICT (asset_id) DO UPDATE SET
			first_seen_at     = LEAST(classic_assets.first_seen_at, EXCLUDED.first_seen_at),
			first_seen_ledger = LEAST(classic_assets.first_seen_ledger, EXCLUDED.first_seen_ledger),
			last_seen_at      = GREATEST(classic_assets.last_seen_at, EXCLUDED.last_seen_at),
			last_seen_ledger  = GREATEST(classic_assets.last_seen_ledger, EXCLUDED.last_seen_ledger),
			observation_count = classic_assets.observation_count + 1
	`
	if _, err := s.db.ExecContext(ctx, q,
		assetID, asset.Code, asset.Issuer,
		observedAt.UTC(), int(ledger),
	); err != nil {
		return fmt.Errorf("timescale: registerClassicAssetSeen %s: %w", assetID, err)
	}
	assetRegistryDedupe.Store(assetID, time.Now())
	return nil
}

// shouldSkipAssetRegistryUpsert returns true when `now` falls
// within `assetRegistryDedupeTTL` of the last recorded upsert
// for `assetID`. Returns false on no-cache (first time) and on
// expired-cache (TTL elapsed). Extracted as a pure function so
// the F-1243 TTL-gate semantics can be unit-tested without
// standing up a Postgres container.
func shouldSkipAssetRegistryUpsert(assetID string, now time.Time) bool {
	cached, ok := assetRegistryDedupe.Load(assetID)
	if !ok {
		return false
	}
	lastUpsert, ok := cached.(time.Time)
	if !ok {
		return false
	}
	return now.Sub(lastUpsert) < assetRegistryDedupeTTL
}

// registerIssuerSeen ensures a row exists in the `issuers` table
// for the supplied G-strkey. Idempotent + dedupe-cached.
//
// Only writes the g_strkey field — home_domain, auth flags, and
// SEP-1 payload come from a separate AccountEntry observer (per
// ADR-0021) which already exists for operator-configured watched
// accounts. Without that observer running, the curated
// known-issuer fallback at internal/api/v1/known_issuers.go fills
// home_domain + org_name at the wire boundary for the top
// anchors.
func (s *Store) registerIssuerSeen(ctx context.Context, gStrkey string) error {
	if gStrkey == "" {
		return nil
	}
	if _, seen := issuerRegistryDedupe.Load(gStrkey); seen {
		return nil
	}
	const q = `
		INSERT INTO issuers (g_strkey)
		VALUES ($1)
		ON CONFLICT (g_strkey) DO NOTHING
	`
	if _, err := s.db.ExecContext(ctx, q, gStrkey); err != nil {
		return fmt.Errorf("timescale: registerIssuerSeen %s: %w", gStrkey, err)
	}
	issuerRegistryDedupe.Store(gStrkey, struct{}{})
	return nil
}
