package store

import (
	"context"
	"database/sql"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// openTestStore returns a migrated database in a temporary directory.
func openTestStore(t *testing.T) *sql.DB {
	t.Helper()
	db, err := Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := Migrate(context.Background(), db, slog.Default()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func TestOpenSetsWALPragmas(t *testing.T) {
	db := openTestStore(t)
	var mode string
	if err := db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatal(err)
	}
	if mode != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var timeout int
	if err := db.QueryRow(`PRAGMA busy_timeout`).Scan(&timeout); err != nil {
		t.Fatal(err)
	}
	if timeout != 5000 {
		t.Fatalf("busy_timeout = %d, want 5000", timeout)
	}
}

func TestOpenRejectsUNCPath(t *testing.T) {
	if _, err := Open(`\\server\share\metadata.db`); err == nil {
		t.Fatal("expected UNC path to be rejected")
	}
}

func TestMigrateIsIdempotent(t *testing.T) {
	db := openTestStore(t) // openTestStore ran Migrate once; a second run must be a no-op.
	if err := Migrate(context.Background(), db, slog.Default()); err != nil {
		t.Fatalf("second migrate: %v", err)
	}
	for _, table := range []string{
		"schema_migrations", "idempotency_records", "jobs", "job_events", "runs",
		"provider_connection_versions", "batches", "snapshots", "snapshot_batches", "artifacts",
	} {
		var n int
		if err := db.QueryRow(
			`SELECT COUNT(1) FROM sqlite_master WHERE type='table' AND name=?`, table).Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n != 1 {
			t.Errorf("table %s missing", table)
		}
	}
}

func TestMigrateRecordsChecksums(t *testing.T) {
	db := openTestStore(t)
	var version int64
	var checksum string
	if err := db.QueryRow(
		`SELECT version, checksum FROM schema_migrations WHERE version > 0`).Scan(&version, &checksum); err != nil {
		t.Fatal(err)
	}
	if version != 1 {
		t.Fatalf("version = %d, want 1", version)
	}
	if len(checksum) != 64 {
		t.Fatalf("checksum = %q, want a 64-char sha256", checksum)
	}
}

func TestMigrateRejectsTamperedChecksum(t *testing.T) {
	db := openTestStore(t)
	if _, err := db.Exec(`UPDATE schema_migrations SET checksum = 'deadbeef' WHERE version = 1`); err != nil {
		t.Fatal(err)
	}
	err := Migrate(context.Background(), db, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "changed on disk") {
		t.Fatalf("expected tamper refusal, got %v", err)
	}
}

func TestMigrateRejectsUnverifiableVersion(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := Migrate(context.Background(), db, slog.Default()); err != nil {
		t.Fatal(err)
	}
	// Simulate a history whose file is gone from the embedded filesystem.
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at, checksum) VALUES (99, 0, 'x')`); err != nil {
		t.Fatal(err)
	}
	err = Migrate(context.Background(), db, slog.Default())
	if err == nil || !strings.Contains(err.Error(), "cannot be verified") {
		t.Fatalf("expected missing-file refusal, got %v", err)
	}
}

func TestIdempotencyStoreClaimAndReplay(t *testing.T) {
	db := openTestStore(t)
	s := NewIdempotencyStore(db)
	now := time.Now().UTC()

	existing, claimed, err := s.Begin("caller|default|POST|/api/v1/things", "key-1234", "hash-a", now)
	if err != nil || !claimed || existing != nil {
		t.Fatalf("first claim: claimed=%v existing=%v err=%v", claimed, existing, err)
	}

	// Same (scope, key) before expiry replays the in-flight record.
	existing, claimed, err = s.Begin("caller|default|POST|/api/v1/things", "key-1234", "hash-a", now.Add(time.Minute))
	if err != nil || claimed || existing == nil || !existing.InFlight() {
		t.Fatalf("replay claim: claimed=%v existing=%v err=%v", claimed, existing, err)
	}

	done := ports.IdempotencyRecord{
		Scope:       "caller|default|POST|/api/v1/things",
		Key:         "key-1234",
		RequestHash: "hash-a",
		Status:      201,
		Body:        []byte(`{"id":"t1"}`),
		Location:    "/api/v1/things/t1",
	}
	if err := s.Commit(done); err != nil {
		t.Fatalf("commit: %v", err)
	}

	existing, claimed, err = s.Begin("caller|default|POST|/api/v1/things", "key-1234", "hash-a", now.Add(2*time.Minute))
	if err != nil || claimed {
		t.Fatalf("post-commit claim: claimed=%v err=%v", claimed, err)
	}
	if existing.Status != 201 || string(existing.Body) != `{"id":"t1"}` || existing.Location != "/api/v1/things/t1" {
		t.Fatalf("replayed record mismatch: %+v", existing)
	}
	if existing.CreatedAt != now.UTC() || !existing.ExpiresAt.Equal(now.Add(ports.IdempotencyReplayWindow).UTC()) {
		t.Fatalf("timestamps not roundtripped: created=%v expires=%v", existing.CreatedAt, existing.ExpiresAt)
	}
}

func TestIdempotencyStoreScopeIsolation(t *testing.T) {
	db := openTestStore(t)
	s := NewIdempotencyStore(db)
	now := time.Now().UTC()

	if _, claimed, _ := s.Begin("caller-a|default|POST|/x", "shared-key", "h", now); !claimed {
		t.Fatal("caller-a claim failed")
	}
	existing, claimed, err := s.Begin("caller-b|default|POST|/x", "shared-key", "h", now)
	if err != nil || !claimed || existing != nil {
		t.Fatalf("caller-b must claim independently: claimed=%v existing=%v err=%v", claimed, existing, err)
	}
}

func TestIdempotencyStoreExpiredClaimNotReclaimed(t *testing.T) {
	db := openTestStore(t)
	s := NewIdempotencyStore(db)
	now := time.Now().UTC()

	if _, claimed, _ := s.Begin("s", "k", "h", now); !claimed {
		t.Fatal("initial claim failed")
	}
	// Before expiry: replay. After expiry: the stored record is returned,
	// never reclaimed for a silent re-execution.
	if rec, claimed, _ := s.Begin("s", "k", "h", now.Add(ports.IdempotencyReplayWindow-time.Minute)); claimed || rec == nil {
		t.Fatal("expected replay before expiry")
	}
	existing, claimed, err := s.Begin("s", "k", "h", now.Add(ports.IdempotencyReplayWindow+time.Minute))
	if err != nil || claimed || existing == nil {
		t.Fatalf("expired claim: claimed=%v existing=%v err=%v, want the stored record", claimed, existing, err)
	}
	if existing.RequestHash != "h" {
		t.Errorf("request hash = %q, want the original claim hash", existing.RequestHash)
	}
}

func TestIdempotencyStoreCommitHashGuard(t *testing.T) {
	db := openTestStore(t)
	s := NewIdempotencyStore(db)
	now := time.Now().UTC()

	if _, claimed, _ := s.Begin("s", "k", "hash-a", now); !claimed {
		t.Fatal("claim failed")
	}
	err := s.Commit(ports.IdempotencyRecord{Scope: "s", Key: "k", RequestHash: "hash-other", Status: 200})
	if domain.ErrorCode(err) != domain.CodeIdempotencyConflict {
		t.Fatalf("expected idempotency conflict, got %v", err)
	}
	// A matching hash commits; a second commit for the same claim also
	// succeeds (idempotent upsert of the same response).
	if err := s.Commit(ports.IdempotencyRecord{Scope: "s", Key: "k", RequestHash: "hash-a", Status: 200}); err != nil {
		t.Fatalf("matching commit failed: %v", err)
	}
}

func TestIdempotencyStoreConcurrentClaimExactlyOneWinner(t *testing.T) {
	db := openTestStore(t)
	s := NewIdempotencyStore(db)
	now := time.Now().UTC()

	const contenders = 8
	var wg sync.WaitGroup
	wins := make(chan struct{}, contenders)
	for range contenders {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, claimed, err := s.Begin("s", "contended", "h", now)
			if err != nil {
				t.Errorf("claim: %v", err)
				return
			}
			if claimed {
				wins <- struct{}{}
			}
		}()
	}
	wg.Wait()
	close(wins)
	if len(wins) != 1 {
		t.Fatalf("got %d winners, want exactly 1", len(wins))
	}
}
