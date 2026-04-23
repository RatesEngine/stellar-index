package timescale

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	_ "github.com/lib/pq"
)

// Store is the handle on our TimescaleDB connection pool.
// Safe for concurrent use.
type Store struct {
	db *sql.DB
}

// Open initialises a connection pool. Ping'd before returning so a
// bad DSN fails fast.
//
// Configuration:
//   - max open conns: 25 (conservative; tune per deployment).
//   - max idle conns: 5.
//   - conn max lifetime: 30 min — full re-dial ceiling. Beats
//     Patroni's typical rolling-restart interval so a swapped
//     primary never keeps a stale conn longer than this.
//   - conn max idle time: 5 min — bound the window where an idle
//     conn the DB-side has already killed (pg_terminate_backend,
//     firewall tcp-timeout, Patroni failover) might still be
//     handed out. Without this, an idle conn can live until
//     ConnMaxLifetime, forcing a retry at serve-time.
func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("timescale: sql.Open: %w", err)
	}
	db.SetMaxOpenConns(25)
	db.SetMaxIdleConns(5)
	db.SetConnMaxLifetime(30 * time.Minute)
	db.SetConnMaxIdleTime(5 * time.Minute)

	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if err := db.PingContext(pctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("timescale: ping: %w", err)
	}
	return &Store{db: db}, nil
}

// Close releases the connection pool. Safe to call more than once.
func (s *Store) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// DB returns the underlying *sql.DB for packages that need raw
// access (e.g. the migrate binary). Prefer the typed methods.
func (s *Store) DB() *sql.DB { return s.db }

// ─── Error helpers ─────────────────────────────────────────────────

// ErrNotFound indicates a row we expected to exist did not.
var ErrNotFound = errors.New("timescale: not found")
