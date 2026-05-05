package postgresstore

import (
	"database/sql"

	_ "github.com/lib/pq"
)

// Store is the shared *sql.DB handle every concrete store wraps.
// Constructed by Open and threaded through {Account,User,Token,
// APIKey,Audit,Billing,Webhook}Store.
//
// Safe for concurrent use; *sql.DB is internally pooled.
type Store struct {
	db *sql.DB
}

// New wraps an existing *sql.DB. Used by tests that already
// have a container handle and by production wiring that opens
// the pool once and shares it across stores.
func New(db *sql.DB) *Store {
	return &Store{db: db}
}

// DB exposes the underlying handle for the rare case a caller
// needs to run a transaction across multiple stores. The Account
// + User stores accept a non-nil *sql.Tx via the Tx-suffixed
// methods (added per-need; not all impls have one yet).
func (s *Store) DB() *sql.DB { return s.db }
