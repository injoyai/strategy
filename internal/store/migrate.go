package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/database"

	"github.com/injoyai/strategy/internal/store/migrations"
)

// migrationsTableName is the version table managed by migrationsStore. It
// deliberately differs from goose's default (goose_db_version) so the schema
// records the checksum of every applied script, letting the service refuse
// to start when an already-applied migration changed on disk.
const migrationsTableName = "schema_migrations"

// Migrate applies all embedded migrations. It fails if a previously applied
// script no longer matches its recorded checksum (tampered or re-ordered
// history), or if a recorded version has no file in the embedded filesystem.
func Migrate(ctx context.Context, db *sql.DB, log *slog.Logger) error {
	if err := verifyAppliedChecksums(ctx, db); err != nil {
		return err
	}
	fsys := migrations.FS()
	provider, err := goose.NewProvider(
		goose.DialectCustom,
		db,
		fsys,
		goose.WithStore(&migrationsStore{fsys: fsys}),
	)
	if err != nil {
		return fmt.Errorf("store: create migration provider: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return fmt.Errorf("store: apply migrations: %w", err)
	}
	if len(results) > 0 {
		applied := make([]int64, 0, len(results))
		for _, r := range results {
			applied = append(applied, r.Source.Version)
		}
		log.Info("migrations applied", slog.Any("versions", applied))
	}
	return nil
}

// verifyAppliedChecksums compares each recorded migration checksum against
// the embedded file of the same version. A fresh database (no version table)
// passes trivially. This check runs before goose so a changed history is
// reported as a startup refusal, never as a half-applied migration.
func verifyAppliedChecksums(ctx context.Context, db *sql.DB) error {
	exists, err := tableExists(ctx, db, migrationsTableName)
	if err != nil {
		return fmt.Errorf("store: check version table: %w", err)
	}
	if !exists {
		return nil
	}
	rows, err := db.QueryContext(ctx,
		`SELECT version, checksum FROM `+migrationsTableName+` WHERE version > 0 ORDER BY version`)
	if err != nil {
		return fmt.Errorf("store: list applied migrations: %w", err)
	}
	defer rows.Close()
	type applied struct {
		version  int64
		checksum string
	}
	var records []applied
	for rows.Next() {
		var r applied
		if err := rows.Scan(&r.version, &r.checksum); err != nil {
			return fmt.Errorf("store: scan applied migrations: %w", err)
		}
		records = append(records, r)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("store: list applied migrations: %w", err)
	}
	fsys := migrations.FS()
	for _, r := range records {
		sum, err := migrationFileChecksum(fsys, r.version)
		if err != nil {
			return fmt.Errorf("store: applied migration %d cannot be verified: %w", r.version, err)
		}
		if sum != r.checksum {
			return fmt.Errorf(
				"store: applied migration %d changed on disk (recorded %s, embedded %s); refusing to start",
				r.version, r.checksum, sum)
		}
	}
	return nil
}

// tableExists checks sqlite_master. It accepts goose's DBTxConn (satisfied by
// both *sql.DB and *sql.Tx) so migrationsStore.TableExists can reuse it
// inside goose transactions.
func tableExists(ctx context.Context, db database.DBTxConn, name string) (bool, error) {
	var n int
	err := db.QueryRowContext(ctx,
		`SELECT COUNT(1) FROM sqlite_master WHERE type = 'table' AND name = ?`, name).Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}

// migrationFileChecksum locates the embedded file for a version and returns
// its hex SHA-256. Version files follow the goose convention of a numeric
// prefix ("<version>_<name>.sql"); the prefix may be zero-padded to any
// width, so files are resolved by parsing, not by a fixed-width glob.
func migrationFileChecksum(fsys fs.FS, version int64) (string, error) {
	matches, err := fs.Glob(fsys, "*.sql")
	if err != nil {
		return "", fmt.Errorf("scan migration files: %w", err)
	}
	var found string
	for _, name := range matches {
		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			continue
		}
		v, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			continue
		}
		if v == version {
			if found != "" {
				return "", fmt.Errorf("multiple migration files match version %d", version)
			}
			found = name
		}
	}
	if found == "" {
		return "", fmt.Errorf("no migration file matches version %d", version)
	}
	data, err := fs.ReadFile(fsys, found)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", found, err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// migrationsStore implements database.Store on top of the schema_migrations
// table. It is required because goose's default store keeps no checksum, and
// the custom checksum column is what powers the tamper-detection above.
//
// The store is injected via goose.WithStore, which mandates DialectCustom.
type migrationsStore struct {
	fsys fs.FS
}

var _ database.Store = (*migrationsStore)(nil)

// TableExists is an optional method probed by goose (via the controller
// wrapper) to skip version-table creation; implementing it avoids the
// fallback query noise on fresh databases.
func (s *migrationsStore) TableExists(ctx context.Context, db database.DBTxConn) (bool, error) {
	return tableExists(ctx, db, migrationsTableName)
}

func (s *migrationsStore) Tablename() string { return migrationsTableName }

func (s *migrationsStore) CreateVersionTable(ctx context.Context, db database.DBTxConn) error {
	_, err := db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS `+migrationsTableName+` (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL,
		checksum   TEXT    NOT NULL
	)`)
	if err != nil {
		return fmt.Errorf("create %s table: %w", migrationsTableName, err)
	}
	return nil
}

// Insert records an applied version together with the checksum of its
// embedded file. Version 0 is goose's bootstrap row and has no file, so it
// carries an empty checksum.
func (s *migrationsStore) Insert(ctx context.Context, db database.DBTxConn, req database.InsertRequest) error {
	checksum := ""
	if req.Version > 0 {
		var err error
		if checksum, err = migrationFileChecksum(s.fsys, req.Version); err != nil {
			return err
		}
	}
	_, err := db.ExecContext(ctx, `INSERT INTO `+migrationsTableName+` (version, applied_at, checksum)
		VALUES (?, ?, ?)`, req.Version, time.Now().UTC().UnixNano(), checksum)
	if err != nil {
		return fmt.Errorf("record migration %d: %w", req.Version, err)
	}
	return nil
}

func (s *migrationsStore) Delete(ctx context.Context, db database.DBTxConn, version int64) error {
	_, err := db.ExecContext(ctx, `DELETE FROM `+migrationsTableName+` WHERE version = ?`, version)
	if err != nil {
		return fmt.Errorf("delete migration %d: %w", version, err)
	}
	return nil
}

func (s *migrationsStore) GetMigration(ctx context.Context, db database.DBTxConn, version int64) (*database.GetMigrationResult, error) {
	var appliedAt int64
	err := db.QueryRowContext(ctx, `SELECT applied_at FROM `+migrationsTableName+` WHERE version = ?`, version).Scan(&appliedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %d", database.ErrVersionNotFound, version)
	}
	if err != nil {
		return nil, fmt.Errorf("get migration %d: %w", version, err)
	}
	return &database.GetMigrationResult{
		Timestamp: time.Unix(0, appliedAt).UTC(),
		IsApplied: true,
	}, nil
}

func (s *migrationsStore) GetLatestVersion(ctx context.Context, db database.DBTxConn) (int64, error) {
	var version sql.NullInt64
	err := db.QueryRowContext(ctx, `SELECT MAX(version) FROM `+migrationsTableName).Scan(&version)
	if err != nil {
		return -1, fmt.Errorf("get latest migration version: %w", err)
	}
	if !version.Valid {
		return -1, fmt.Errorf("latest %w", database.ErrVersionNotFound)
	}
	return version.Int64, nil
}

func (s *migrationsStore) ListMigrations(ctx context.Context, db database.DBTxConn) ([]*database.ListMigrationsResult, error) {
	rows, err := db.QueryContext(ctx, `SELECT version FROM `+migrationsTableName+` ORDER BY version DESC`)
	if err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	defer rows.Close()
	var out []*database.ListMigrationsResult
	for rows.Next() {
		var r database.ListMigrationsResult
		if err := rows.Scan(&r.Version); err != nil {
			return nil, fmt.Errorf("scan migration row: %w", err)
		}
		r.IsApplied = true
		out = append(out, &r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list migrations: %w", err)
	}
	return out, nil
}
