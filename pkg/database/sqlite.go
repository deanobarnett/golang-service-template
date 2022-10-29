package database

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"os"
	"sort"
	"time"

	"github.com/jmoiron/sqlx"

	_ "github.com/mattn/go-sqlite3"
)

//go:embed migration/*.sql
var migrationFS embed.FS

type Sqlite struct {
	db *sqlx.DB

	ctx    context.Context
	cancel func()
}

func New(dsn string) (*Sqlite, error) {
	sqlxDB, err := sqlx.Connect("sqlite3", dsn)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())
	db := &Sqlite{sqlxDB, ctx, cancel}

	db.db.SetMaxOpenConns(25)
	db.db.SetMaxIdleConns(25)
	db.db.SetConnMaxIdleTime(5 * time.Minute)
	db.db.SetConnMaxLifetime(2 * time.Hour)

	// WAL mode is required for concurrent writes.
	if _, err := db.db.Exec(`PRAGMA journal_mode = wal;`); err != nil {
		return nil, fmt.Errorf("enable wal: %w", err)
	}

	// Safe in WAL mode. Sync only called when the WAL becomes full.
	// https://www.sqlite.org/pragma.html#pragma_synchronous
	if _, err := db.db.Exec(`PRAGMA synchronous = NORMAL;`); err != nil {
		return nil, fmt.Errorf("foreign keys pragma: %w", err)
	}

	// Enable foreign key constraints.
	if _, err := db.db.Exec(`PRAGMA foreign_keys = ON;`); err != nil {
		return nil, fmt.Errorf("foreign keys pragma: %w", err)
	}

	// Busy timeout waits for queries to finish if there is an active lock.
	if _, err := db.db.Exec(`PRAGMA busy_timeout = 5000;`); err != nil {
		return nil, fmt.Errorf("foreign keys pragma: %w", err)
	}

	// Disable auto checkpointing when replication is enabled. This prevents other
	// processes from checkpointing before litesteams has a chance to replicate
	// the WAL file.
	if os.Getenv("LITESTREAM_ACCESS_KEY") != "" {
		if _, err := db.db.Exec(`PRAGMA wal_autocheckpoint = 0;`); err != nil {
			return nil, fmt.Errorf("foreign keys pragma: %w", err)
		}
	}

	if err := db.migrate(); err != nil {
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return db, nil
}

// Close closes the database connection.
func (db *Sqlite) Close() error {
	// Close database.
	if db != nil {
		// Cancel background context.
		db.cancel()
		return db.db.Close()
	}
	return nil
}

// migrate sets up migration tracking and executes pending migration files.
//
// Migration files are embedded in the database/migration folder and are executed
// in lexigraphical order.
//
// Once a migration is run, its name is stored in the 'migrations' table so it
// is not re-executed. Migrations run in a transaction to prevent partial
// migrations.
func (db *Sqlite) migrate() error {
	// Ensure the 'migrations' table exists so we don't duplicate migrations.
	if _, err := db.db.Exec(`CREATE TABLE IF NOT EXISTS migrations (name TEXT PRIMARY KEY);`); err != nil {
		return fmt.Errorf("cannot create migrations table: %w", err)
	}

	names, err := fs.Glob(migrationFS, "migration/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)

	// Loop over all migration files and execute them in order.
	for _, name := range names {
		if err := db.migrateFile(name); err != nil {
			return fmt.Errorf("migration error: name=%q err=%w", name, err)
		}
	}
	return nil
}

// migrate runs a single migration file within a transaction. On success, the
// migration file name is saved to the "migrations" table to prevent re-running.
func (db *Sqlite) migrateFile(name string) error {
	tx, err := db.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// Ensure migration has not already been run.
	var n int
	if err := tx.QueryRow(`SELECT COUNT(*) FROM migrations WHERE name = ?`, name).Scan(&n); err != nil {
		return err
	} else if n != 0 {
		return nil
	}

	if buf, err := fs.ReadFile(migrationFS, name); err != nil {
		return err
	} else if _, err := tx.Exec(string(buf)); err != nil {
		return err
	}

	// Insert record into migrations to prevent re-running migration.
	if _, err := tx.Exec(`INSERT INTO migrations (name) VALUES (?)`, name); err != nil {
		return err
	}

	fmt.Printf("migration success: %s\n", name)

	return tx.Commit()
}
