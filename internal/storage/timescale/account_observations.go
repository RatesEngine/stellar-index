package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/StellarAtlas/stellar-atlas/internal/sources/accounts"
)

// InsertAccountObservation appends one [accounts.Observation] to
// `account_observations`. Per the migration, identity is
// (account_id, ledger, observed_at) — the partition column is
// dragged into the PK because Timescale requires it.
//
// Last-writer-wins on conflict: an account touched multiple times
// in the same ledger (e.g. fee + op + op) writes successive rows
// for the same (account_id, ledger), and the AccountEntry post-
// state is monotonic within a ledger so the latest write is the
// authoritative final state. Implemented as ON CONFLICT DO UPDATE
// with the new values overriding the old; observed_at stays the
// same (it's the ledger close time, not write time).
//
// Defensive guards: rejects zero-value AccountID + nil Balance
// before touching the database. These are precondition violations
// upstream (the observer always populates both) but a misbehaving
// caller would otherwise hit a NOT NULL violation deeper in the
// stack with a less-obvious error message.
func (s *Store) InsertAccountObservation(ctx context.Context, o accounts.Observation) error {
	if o.AccountID == "" {
		return errors.New("timescale: InsertAccountObservation: AccountID is empty")
	}
	if o.Balance == nil {
		return fmt.Errorf("timescale: InsertAccountObservation: AccountID=%s Balance is nil", o.AccountID)
	}
	const q = `
        INSERT INTO account_observations (
            account_id, ledger, observed_at,
            balance_stroops, home_domain, flags, seq_num, is_removal
        ) VALUES (
            $1, $2, $3,
            $4, $5, $6, $7, $8
        )
        ON CONFLICT (account_id, ledger, observed_at) DO UPDATE SET
            balance_stroops = EXCLUDED.balance_stroops,
            home_domain     = EXCLUDED.home_domain,
            flags           = EXCLUDED.flags,
            seq_num         = EXCLUDED.seq_num,
            is_removal      = EXCLUDED.is_removal
    `
	var homeDomain sql.NullString
	if o.HomeDomain != "" {
		homeDomain = sql.NullString{String: o.HomeDomain, Valid: true}
	}
	balance := o.Balance.String() // numeric column accepts decimal text

	_, err := s.db.ExecContext(ctx, q,
		o.AccountID,
		int(o.Ledger),
		o.ObservedAt.UTC(),
		balance,
		homeDomain,
		int(o.Flags),
		o.SeqNum,
		o.IsRemoval,
	)
	if err != nil {
		return fmt.Errorf("timescale: InsertAccountObservation %s@%d: %w", o.AccountID, o.Ledger, err)
	}
	return nil
}

// AccountObservation is the read-side row shape — same fields as
// [accounts.Observation] but with sql-friendly types where the
// columns are NULL-able (HomeDomain is *string).
//
// Returned by the readers. Why not reuse accounts.Observation
// directly: that type is the wire shape the dispatcher emits;
// keeping the read-side type separate means evolving one shouldn't
// force the other to follow.
type AccountObservation struct {
	AccountID  string
	Ledger     uint32
	ObservedAt time.Time
	Balance    *big.Int
	HomeDomain *string
	Flags      uint32
	SeqNum     int64
	IsRemoval  bool
}

// LatestAccountObservationAtOrBefore returns the most-recent
// observation for accountID with ledger ≤ asOf. Returns
// [ErrNotFound] when the account has no observations in scope
// (caller falls back to operator-static config per ADR-0021).
//
// Used by the LCMReserveBalanceReader + LCMHomeDomainResolver
// shipping in the next PR.
//
// Schema constraint: `account_observations.ledger` is `integer`
// (postgres int4 = signed 32-bit, max 2,147,483,647). The Go
// signature accepts uint32 for symmetry with Stellar's ledger
// type, but values > MaxInt32 must be capped before reaching
// `pq` — otherwise the driver returns
// `pq: value "..." is out of range for type integer (22003)`
// and the lookup fails. Callers passing "no upper bound" should
// use `math.MaxInt32`, NOT `^uint32(0)`. This function defensively
// caps high values so a regression in any caller doesn't surface
// as a flood of resolver errors. (At Stellar's current ~62M
// ledger and ~5 ledgers/sec there's ~13 years of headroom before
// MaxInt32 is reached; switching to bigint is a future migration.)
func (s *Store) LatestAccountObservationAtOrBefore(ctx context.Context, accountID string, asOfLedger uint32) (AccountObservation, error) {
	const maxInt32 = uint32(math.MaxInt32)
	if asOfLedger > maxInt32 {
		asOfLedger = maxInt32
	}
	const q = `
        SELECT account_id, ledger, observed_at,
               balance_stroops::text, home_domain, flags, seq_num, is_removal
          FROM account_observations
         WHERE account_id = $1
           AND ledger <= $2
         ORDER BY ledger DESC
         LIMIT 1
    `
	var (
		row      AccountObservation
		balRaw   string
		homeStr  sql.NullString
		flagsInt int
		ledger   int
	)
	err := s.db.QueryRowContext(ctx, q, accountID, int(asOfLedger)).Scan(
		&row.AccountID,
		&ledger,
		&row.ObservedAt,
		&balRaw,
		&homeStr,
		&flagsInt,
		&row.SeqNum,
		&row.IsRemoval,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return AccountObservation{}, ErrNotFound
	}
	if err != nil {
		return AccountObservation{}, fmt.Errorf("timescale: LatestAccountObservationAtOrBefore %s@%d: %w", accountID, asOfLedger, err)
	}
	bal, ok := new(big.Int).SetString(balRaw, 10)
	if !ok {
		return AccountObservation{}, fmt.Errorf("timescale: LatestAccountObservationAtOrBefore: parse balance %q for %s", balRaw, accountID)
	}
	row.Ledger = uint32(ledger)
	row.Balance = bal
	row.Flags = uint32(flagsInt)
	if homeStr.Valid {
		s := homeStr.String
		row.HomeDomain = &s
	}
	return row, nil
}
