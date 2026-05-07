package timescale

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/RatesEngine/rates-engine/internal/canonical"
)

// assetRegistryDedupe is a process-lifetime cache of asset_ids that
// have already been touched by registerClassicAssetSeen. Avoids
// hitting the DB once per trade with the same upsert when the
// dispatcher streams thousands of trades for the same pair —
// the first touch in a process registers the (asset, issuer);
// every subsequent touch for the same asset_id is a no-op.
//
// Process-lifetime is intentional: the indexer restarts often
// enough that we'll re-touch the row periodically (which is fine
// because each touch ON CONFLICT updates last_seen_*). A persistent
// cross-process cache (Redis) would be over-engineering.
var assetRegistryDedupe sync.Map // key: asset_id (string) → struct{}{}

// issuerRegistryDedupe is the analogous cache for issuer rows.
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
	if _, seen := assetRegistryDedupe.Load(assetID); seen {
		return nil
	}

	// Issuer first — issuers row has no FK from classic_assets but
	// keeping the order consistent makes it easier to reason about
	// race-free reads from the API: every classic_assets row has a
	// matching issuers row by the time it's queryable.
	if err := s.registerIssuerSeen(ctx, asset.Issuer); err != nil {
		return err
	}

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
	assetRegistryDedupe.Store(assetID, struct{}{})
	return nil
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
