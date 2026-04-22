// Binary ratesengine-migrate applies and rolls back TimescaleDB
// schema migrations under migrations/. Thin wrapper over
// golang-migrate/migrate with our project's env-based DSN resolution
// and safety rails.
//
// Subcommands:
//
//	ratesengine-migrate up              Apply every pending migration.
//	ratesengine-migrate down [N]        Roll back last N migrations (default 1).
//	ratesengine-migrate status          Show current + target version.
//	ratesengine-migrate version         Build version.
//	ratesengine-migrate help            Print usage.
//
// DSN resolution order: --dsn flag, then RATESENGINE_POSTGRES_DSN env,
// then fail. We intentionally do NOT fall back to defaults here —
// running migrations against "whatever DB happens to be local" is
// how people wipe production.
//
// Locking: golang-migrate grabs a Postgres advisory lock before
// applying, so two concurrent runners serialise safely.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"strconv"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/postgres"
	_ "github.com/golang-migrate/migrate/v4/source/file"

	"github.com/RatesEngine/rates-engine/internal/version"
)

func main() {
	fs := flag.NewFlagSet("ratesengine-migrate", flag.ContinueOnError)
	dsn := fs.String("dsn", "", "Postgres DSN (overrides RATESENGINE_POSTGRES_DSN env)")
	dir := fs.String("migrations", "migrations", "Path to the migrations directory")
	fs.Usage = func() { printUsage(fs) }

	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}

	args := fs.Args()
	if len(args) == 0 {
		printUsage(fs)
		os.Exit(2)
	}

	resolvedDSN := *dsn
	if resolvedDSN == "" {
		resolvedDSN = os.Getenv("RATESENGINE_POSTGRES_DSN")
	}

	switch args[0] {
	case "up":
		if resolvedDSN == "" {
			die("no DSN: set RATESENGINE_POSTGRES_DSN or pass -dsn")
		}
		if err := cmdUp(*dir, resolvedDSN); err != nil {
			die("up: %v", err)
		}
	case "down":
		n := 1
		if len(args) > 1 {
			parsed, err := strconv.Atoi(args[1])
			if err != nil || parsed < 1 {
				die("down: N must be a positive integer (got %q)", args[1])
			}
			n = parsed
		}
		if resolvedDSN == "" {
			die("no DSN: set RATESENGINE_POSTGRES_DSN or pass -dsn")
		}
		if err := cmdDown(*dir, resolvedDSN, n); err != nil {
			die("down: %v", err)
		}
	case "status":
		if resolvedDSN == "" {
			die("no DSN: set RATESENGINE_POSTGRES_DSN or pass -dsn")
		}
		if err := cmdStatus(*dir, resolvedDSN); err != nil {
			die("status: %v", err)
		}
	case "version", "--version", "-v":
		fmt.Println(version.String())
	case "help", "--help", "-h":
		printUsage(fs)
	default:
		fmt.Fprintf(os.Stderr, "unknown subcommand %q\n", args[0])
		printUsage(fs)
		os.Exit(2)
	}
}

func newMigrator(dir, dsn string) (*migrate.Migrate, error) {
	src := "file://" + dir
	m, err := migrate.New(src, dsn)
	if err != nil {
		return nil, fmt.Errorf("open migrator: %w", err)
	}
	return m, nil
}

func cmdUp(dir, dsn string) error {
	m, err := newMigrator(dir, dsn)
	if err != nil {
		return err
	}
	defer closeSilent(m)

	if err := m.Up(); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			fmt.Println("already at latest version — nothing to do")
			return nil
		}
		return err
	}
	v, dirty, vErr := m.Version()
	if vErr != nil {
		return fmt.Errorf("post-up version: %w", vErr)
	}
	fmt.Printf("migrated to version %d (dirty=%v)\n", v, dirty)
	return nil
}

func cmdDown(dir, dsn string, n int) error {
	m, err := newMigrator(dir, dsn)
	if err != nil {
		return err
	}
	defer closeSilent(m)

	if err := m.Steps(-n); err != nil {
		if errors.Is(err, migrate.ErrNoChange) {
			fmt.Println("already at version 0 — nothing to roll back")
			return nil
		}
		return err
	}
	v, dirty, vErr := m.Version()
	if vErr != nil {
		if errors.Is(vErr, migrate.ErrNilVersion) {
			fmt.Println("rolled back to version 0 (nothing applied)")
			return nil
		}
		return fmt.Errorf("post-down version: %w", vErr)
	}
	fmt.Printf("rolled back to version %d (dirty=%v)\n", v, dirty)
	return nil
}

func cmdStatus(dir, dsn string) error {
	m, err := newMigrator(dir, dsn)
	if err != nil {
		return err
	}
	defer closeSilent(m)

	v, dirty, err := m.Version()
	if err != nil {
		if errors.Is(err, migrate.ErrNilVersion) {
			fmt.Println("current version: 0 (no migrations applied)")
			return nil
		}
		return err
	}
	fmt.Printf("current version: %d (dirty=%v)\n", v, dirty)
	return nil
}

func closeSilent(m *migrate.Migrate) {
	srcErr, dbErr := m.Close()
	if srcErr != nil {
		fmt.Fprintf(os.Stderr, "warn: close source: %v\n", srcErr)
	}
	if dbErr != nil {
		fmt.Fprintf(os.Stderr, "warn: close db: %v\n", dbErr)
	}
}

func die(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "ratesengine-migrate: "+format+"\n", args...)
	os.Exit(1)
}

func printUsage(fs *flag.FlagSet) {
	fmt.Fprintf(os.Stderr, `ratesengine-migrate %s

Apply + manage TimescaleDB schema migrations.

Usage:
  ratesengine-migrate [-dsn DSN] [-migrations DIR] <subcommand> [args]

Subcommands:
  up              Apply every pending migration.
  down [N]        Roll back last N migrations (default 1).
  status          Show current applied version.
  version         Build version.
  help            This help.

Flags:
`, version.String())
	fs.PrintDefaults()
	fmt.Fprintf(os.Stderr, `
Environment:
  RATESENGINE_POSTGRES_DSN   Postgres DSN, used when -dsn is not set.
                             Example: postgres://user:pass@host:5432/db?sslmode=disable

Examples:
  export RATESENGINE_POSTGRES_DSN="postgres://ratesengine@localhost/ratesengine?sslmode=disable"
  ratesengine-migrate up
  ratesengine-migrate status
  ratesengine-migrate down 1
`)
}
