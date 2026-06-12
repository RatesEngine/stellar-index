package timescale

import (
	"context"
	"errors"
	"fmt"
)

// ProtocolContract is the read-side projection of one protocol_contracts
// row — a factory-descended child contract for a gated decoder (ADR-0035).
type ProtocolContract struct {
	Source      string
	ContractID  string
	FactoryID   string
	FirstLedger uint32 // 0 when the seed source didn't carry it
}

// UpsertProtocolContract records (or refreshes) a factory-descended child
// contract for a gated source. Idempotent on (source, contract_id) — the
// live indexer's factory-creation handler calls this on every creation
// event without checking whether the row already exists, and the
// `seed-protocol-contracts` genesis walk re-upserts the same set.
//
// firstLedger may be 0 (unknown); it's stored as NULL in that case so a
// later seed that DOES know the ledger can fill it without being masked by
// a 0 sentinel.
func (s *Store) UpsertProtocolContract(ctx context.Context, source, contractID, factoryID string, firstLedger uint32) error {
	if source == "" || contractID == "" {
		return errors.New("timescale: UpsertProtocolContract: empty source or contract_id")
	}
	if factoryID == "" {
		return fmt.Errorf("timescale: UpsertProtocolContract %s/%s: empty factory_id", source, contractID)
	}
	var ledgerArg any
	if firstLedger != 0 {
		ledgerArg = int64(firstLedger)
	}
	const q = `
		INSERT INTO protocol_contracts (source, contract_id, factory_id, first_ledger, observed_at)
		VALUES ($1, $2, $3, $4, now())
		ON CONFLICT (source, contract_id) DO UPDATE SET
		    factory_id   = EXCLUDED.factory_id,
		    first_ledger = COALESCE(protocol_contracts.first_ledger, EXCLUDED.first_ledger),
		    observed_at  = EXCLUDED.observed_at
	`
	if _, err := s.db.ExecContext(ctx, q, source, contractID, factoryID, ledgerArg); err != nil {
		return fmt.Errorf("timescale: UpsertProtocolContract %s/%s: %w", source, contractID, err)
	}
	return nil
}

// LoadProtocolContracts returns every child contract C-strkey registered
// for source, as a flat slice. Used by the indexer / projector / audit
// commands at startup to warm a gated decoder's childgate.Registry.
//
// Returns an empty slice (not nil + error) when the source has no rows —
// the steady-state for a fresh deployment that hasn't run
// `ratesengine-ops seed-protocol-contracts -source <name>` yet. The gate
// then sees an empty registry and (correctly, per ADR-0035) drops every
// child event until seeded; running the genesis walk is a deploy
// precondition.
func (s *Store) LoadProtocolContracts(ctx context.Context, source string) ([]string, error) {
	if source == "" {
		return nil, errors.New("timescale: LoadProtocolContracts: empty source")
	}
	const q = `
		SELECT contract_id
		  FROM protocol_contracts
		 WHERE source = $1
	`
	rows, err := s.db.QueryContext(ctx, q, source)
	if err != nil {
		return nil, fmt.Errorf("timescale: LoadProtocolContracts %s: %w", source, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]string, 0, 64)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("timescale: LoadProtocolContracts %s scan: %w", source, err)
		}
		out = append(out, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("timescale: LoadProtocolContracts %s rows: %w", source, err)
	}
	return out, nil
}
