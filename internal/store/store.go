// Package store owns the SQLite metadata database: connection setup,
// embedded schema migrations, and durable implementations of the ports
// defined by the application (idempotency today, job persistence in M0-05).
//
// SQLite runtime contract (DEC-03): modernc.org/sqlite (pure Go), WAL
// journaling on a local filesystem only, a busy timeout to serialize the
// concurrent writers of a single process, and NORMAL synchronous mode which
// with WAL keeps durability at the checkpoint boundary without fsync per
// commit.
package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"
)

// connectionPRAGMAs are applied per pooled connection through the driver's
// DSN parameters, so settings survive connection recycling. journal_mode=WAL
// is persistent anyway, but declaring it here keeps the contract in one place.
// _txlock=immediate makes every transaction take its write lock up front, so
// job claims (SELECT candidate, then UPDATE) serialize instead of racing on
// the upgrade from a deferred read transaction.
const connectionPRAGMAs = "_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_txlock=immediate"

// Open opens (creating if needed) the metadata database at path.
// UNC paths are rejected: WAL relies on shared memory between connections,
// which network filesystems do not provide.
func Open(path string) (*sql.DB, error) {
	if strings.HasPrefix(filepath.Clean(path), `\\`) {
		return nil, fmt.Errorf("store: UNC path %q is not supported for a WAL SQLite database", path)
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("store: resolve database path: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return nil, fmt.Errorf("store: create database directory: %w", err)
	}
	// A file URI with forward slashes is the portable way to carry DSN
	// options on Windows ("file:///C:/...").
	dsn := "file:///" + filepath.ToSlash(abs) + "?" + connectionPRAGMAs
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: ping %s: %w", path, err)
	}
	return db, nil
}
