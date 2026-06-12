package timescale

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/blend"
)

// InsertBlendAdminEvent appends one Blend admin / pool-config /
// pool-factory lifecycle event (set_admin / update_pool /
// queue_set_reserve / cancel_set_reserve / set_reserve / set_status
// / deploy) to the blend_admin hypertable. Idempotent on the PK
// (contract_id, ledger, tx_hash, op_index, event_kind,
// ledger_close_time).
//
// Promoted typed columns: admin / asset / target — populated when
// the event kind carries them; NULL otherwise (see per-kind doc
// in migration 0042 + blend.AdminEvent godoc). Event-type-specific
// remainder (update_pool body, queue_set_reserve ReserveConfig,
// set_reserve index, set_status status+by_admin) lands in the
// attributes jsonb column.
//
// i128 amounts (update_pool.min_collateral, ReserveConfig.supply_cap)
// are decimal strings inside jsonb per ADR-0003 — NUMERIC inside
// jsonb is lossy, but a decimal string round-trips at full
// precision.
func (s *Store) InsertBlendAdminEvent(ctx context.Context, e blend.AdminEvent) error {
	if e.ContractID == "" {
		return errors.New("timescale: InsertBlendAdminEvent: ContractID is empty")
	}
	if e.TxHash == "" {
		return errors.New("timescale: InsertBlendAdminEvent: TxHash is empty")
	}
	if !isBlendAdminKind(e.Kind) {
		return fmt.Errorf("timescale: InsertBlendAdminEvent: invalid Kind %q", e.Kind)
	}

	attrs := buildAdminAttributes(e)
	attrsJSON, err := json.Marshal(attrs)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendAdminEvent: marshal attributes: %w", err)
	}

	const q = `
        INSERT INTO blend_admin (
            contract_id, ledger, tx_hash, op_index, event_index, ledger_close_time,
            event_kind, admin, asset, target,
            attributes
        ) VALUES (
            $1, $2, $3, $4, $5, $6,
            $7, $8, $9, $10,
            $11
        )
        ON CONFLICT (contract_id, ledger, tx_hash, op_index, event_kind, event_index, ledger_close_time) DO NOTHING
    `
	_, err = s.db.ExecContext(ctx, q,
		e.ContractID, int(e.Ledger), e.TxHash, int(e.OpIndex), int(e.EventIndex), e.Timestamp.UTC(),
		e.Kind,
		nullString(e.Admin), nullString(e.Asset), nullString(e.Target),
		attrsJSON,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertBlendAdminEvent %s@%d: %w", e.ContractID, e.Ledger, err)
	}
	return nil
}

// buildAdminAttributes builds the jsonb payload per event kind.
func buildAdminAttributes(e blend.AdminEvent) map[string]any {
	attrs := map[string]any{}
	switch e.Kind {
	case blend.EventUpdatePool:
		attrs["backstop_take_rate"] = e.BackstopTakeRate
		attrs["max_positions"] = e.MaxPositions
		attrs["min_collateral"] = bigIntOrEmpty(e.MinCollateral)
	case blend.EventQueueSetReserve:
		if e.ReserveConfig != nil {
			attrs["metadata"] = e.ReserveConfig
		}
	case blend.EventSetReserve:
		attrs["index"] = e.ReserveIndex
	case blend.EventSetStatus:
		attrs["status"] = e.NewStatus
		attrs["by_admin"] = e.ByAdmin
	}
	return attrs
}

// isBlendAdminKind reports whether kind is one of the seven admin
// event kinds (including the pool-factory `deploy`). Mirrors the
// CHECK constraint in migration 0042.
func isBlendAdminKind(kind string) bool {
	switch kind {
	case blend.EventSetAdmin,
		blend.EventUpdatePool,
		blend.EventQueueSetReserve,
		blend.EventCancelSetReserve,
		blend.EventSetReserve,
		blend.EventSetStatus,
		blend.EventDeploy:
		return true
	}
	return false
}
