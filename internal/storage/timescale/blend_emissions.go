package timescale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/blend"
)

// InsertBlendEmissionEvent appends one Blend emission / credit-
// risk event (gulp / claim / reserve_emission_update /
// gulp_emissions / bad_debt / defaulted_debt) to the
// blend_emissions hypertable. Idempotent on the PK
// (pool, ledger, tx_hash, op_index, event_kind, ledger_close_time).
//
// The amount column is i128 → NUMERIC via decimal string per
// ADR-0003. Asset / User are NULL when the event kind doesn't
// carry them (see per-kind doc in migration 0042 + the
// blend.EmissionEvent godoc).
//
// reserve_emission_update extras (res_token_id, eps, expiration)
// and claim's reserve_token_ids are serialised into the
// attributes jsonb column rather than promoted to typed columns —
// they're per-kind specific and don't warrant a dedicated column
// each (same shape as cctp_events / migration 0038).
func (s *Store) InsertBlendEmissionEvent(ctx context.Context, e blend.EmissionEvent) error {
	if e.Pool == "" {
		return errors.New("timescale: InsertBlendEmissionEvent: Pool is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertBlendEmissionEvent: TxHash is empty")
	}
	if !isBlendEmissionKind(e.Kind) {
		return fmt.Errorf("timescale: InsertBlendEmissionEvent: invalid Kind %q", e.Kind)
	}

	attrs := buildEmissionAttributes(e)
	attrsJSON, err := json.Marshal(attrs)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendEmissionEvent: marshal attributes: %w", err)
	}

	const q = `
        INSERT INTO blend_emissions (
            pool, ledger, tx_hash, op_index, event_index, ledger_close_time,
            event_kind, amount, asset, user_address,
            attributes
        ) VALUES (
            $1, $2, $3, $4, $5, $6,
            $7, $8, $9, $10,
            $11
        )
        ON CONFLICT (pool, ledger, tx_hash, op_index, event_kind, event_index, ledger_close_time) DO NOTHING
    `
	_, err = s.db.ExecContext(ctx, q,
		e.Pool, int(e.Ledger), e.TxHash, int(e.OpIndex), int(e.EventIndex), e.Timestamp.UTC(),
		e.Kind,
		nullNumeric(bigIntOrEmpty(e.Amount)),
		nullString(e.Asset), nullString(e.User),
		attrsJSON,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendEmissionEvent %s@%d: %w", e.Pool, e.Ledger, err)
	}
	return nil
}

// buildEmissionAttributes builds the jsonb payload for the
// attributes column on a per-kind basis. Empty (i.e. {}) for
// kinds whose fields are all promoted to typed columns.
func buildEmissionAttributes(e blend.EmissionEvent) map[string]any {
	attrs := map[string]any{}
	switch e.Kind {
	case blend.EventReserveEmissions:
		attrs["res_token_id"] = e.ResTokenID
		attrs["eps"] = e.EmissionsPerSec
		attrs["expiration"] = e.Expiration
	case blend.EventClaim:
		if len(e.ReserveTokenIDs) > 0 {
			attrs["reserve_token_ids"] = e.ReserveTokenIDs
		}
	}
	return attrs
}

// isBlendEmissionKind reports whether kind is one of the six
// emission / credit-risk event kinds. Mirrors the CHECK constraint
// in migration 0042.
func isBlendEmissionKind(kind string) bool {
	switch kind {
	case blend.EventGulp,
		blend.EventClaim,
		blend.EventReserveEmissions,
		blend.EventGulpEmissions,
		blend.EventBadDebt,
		blend.EventDefaultedDebt:
		return true
	}
	return false
}

// bigIntOrEmpty returns the decimal string of n, or empty string
// if n is nil. Empty string is what nullNumeric treats as NULL.
func bigIntOrEmpty(n interface{ String() string }) string {
	// Concrete *big.Int may be a typed-nil interface; check the
	// concrete pointer rather than the interface value.
	if n == nil {
		return ""
	}
	// big.Int's String() is defined on the value receiver, and
	// (*big.Int)(nil).String() does NOT panic in modern Go (it
	// returns "<nil>"). Catch that explicit sentinel + the empty
	// case as "no amount".
	s := n.String()
	if s == "" || s == "<nil>" {
		return ""
	}
	return s
}
