// Package deps_smoke verifies the approved production dependencies with
// minimal samples on the target platform (GATE-M0-START evidence). The tests
// run with `go test ./...` and require no external services.
package deps_smoke_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/pressly/goose/v3"
	"github.com/shopspring/decimal"

	_ "modernc.org/sqlite"
)

func openSQLite(t *testing.T) *sql.DB {
	t.Helper()
	path := filepath.Join(t.TempDir(), "smoke.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if _, err := db.Exec(`PRAGMA journal_mode=WAL`); err != nil {
		t.Fatalf("enable WAL: %v", err)
	}
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil || mode != "wal" {
		t.Fatalf("journal mode = %q, err = %v, want wal", mode, err)
	}
	if _, err := db.Exec(`PRAGMA busy_timeout=5000`); err != nil {
		t.Fatalf("set busy_timeout: %v", err)
	}
	return db
}

func TestSQLiteWALRoundtrip(t *testing.T) {
	db := openSQLite(t)
	if _, err := db.Exec(`CREATE TABLE smoke (id INTEGER PRIMARY KEY, note TEXT NOT NULL)`); err != nil {
		t.Fatalf("create table: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO smoke (note) VALUES (?)`, "hello"); err != nil {
		t.Fatalf("insert: %v", err)
	}
	var got string
	if err := db.QueryRow(`SELECT note FROM smoke WHERE id = 1`).Scan(&got); err != nil {
		t.Fatalf("select: %v", err)
	}
	if got != "hello" {
		t.Fatalf("roundtrip mismatch: %q", got)
	}
}

func TestGooseMinimalMigration(t *testing.T) {
	db := openSQLite(t)
	dir := t.TempDir()
	mig := filepath.Join(dir, "00001_init.sql")
	sql := `-- +goose Up
CREATE TABLE goose_smoke (id INTEGER PRIMARY KEY);
-- +goose Down
DROP TABLE goose_smoke;
`
	if err := os.WriteFile(mig, []byte(sql), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := goose.SetDialect("sqlite3"); err != nil {
		t.Fatalf("set dialect: %v", err)
	}
	if err := goose.Up(db, dir); err != nil {
		t.Fatalf("goose up: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO goose_smoke (id) VALUES (1)`); err != nil {
		t.Fatalf("insert into migrated table: %v", err)
	}
}

func TestDecimalExactnessAndJSONString(t *testing.T) {
	a := decimal.RequireFromString("0.1")
	b := decimal.RequireFromString("0.2")
	sum := a.Add(b)
	if sum.String() != "0.3" {
		t.Fatalf("0.1+0.2 = %s, want exact 0.3", sum.String())
	}

	// HTTP boundary rule: decimal values travel as JSON strings.
	raw, err := json.Marshal(map[string]string{"amount": sum.String()})
	if err != nil {
		t.Fatal(err)
	}
	var back map[string]string
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	if back["amount"] != "0.3" {
		t.Fatalf("json roundtrip mismatch: %v", back)
	}
}

func TestDecimalRejectsNonNumericBoundaries(t *testing.T) {
	// Note: shopspring/decimal parses exponent notation like "1e999" into a
	// big integer, so rejecting exponent forms is the HTTP boundary parser's
	// job (M0-02), not the library's.
	for _, bad := range []string{"NaN", "Inf", "1,5", ""} {
		if _, err := decimal.NewFromString(bad); err == nil {
			t.Fatalf("NewFromString(%q) must fail", bad)
		}
	}
}
