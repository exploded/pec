// Package store owns the SQLite history database: schema, connection setup,
// and the sqlc-generated queries in the db subpackage.
package store

import (
	"database/sql"
	_ "embed"
	"fmt"

	_ "modernc.org/sqlite"

	"github.com/exploded/pec/internal/store/db"
)

//go:embed schema.sql
var schemaSQL string

// Store wraps the database and generated queries.
type Store struct {
	DB *sql.DB
	Q  *db.Queries
}

// Open opens (creating if needed) the database and applies the schema. The
// schema is CREATE IF NOT EXISTS throughout, so this is idempotent.
func Open(path string) (*Store, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=foreign_keys(1)&_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", path)
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// SQLite serialises writers; one connection avoids SQLITE_BUSY for this
	// workload's tiny volumes. Never raise it.
	d.SetMaxOpenConns(1)
	if _, err := d.Exec(schemaSQL); err != nil {
		d.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	return &Store{DB: d, Q: db.New(d)}, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.DB.Close() }
