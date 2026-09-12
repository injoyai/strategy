package artifacts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"

	"github.com/injoyai/strategy/internal/store"
)

func newTestStore(t *testing.T) (*Store, *sql.DB) {
	t.Helper()
	root := t.TempDir()
	db, err := store.Open(filepath.Join(root, "metadata.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(context.Background(), db, slog.Default()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return New(root, db), db
}

func sha256hex(b []byte) string {
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:])
}

func TestIngestPublishesContentAddressedFile(t *testing.T) {
	s, _ := newTestStore(t)
	content := []byte("hello artifacts")

	p, err := s.Ingest(strings.NewReader(string(content)))
	if err != nil {
		t.Fatalf("ingest: %v", err)
	}
	if p.Checksum != sha256hex(content) {
		t.Fatalf("checksum = %s, want %s", p.Checksum, sha256hex(content))
	}
	if p.Size != int64(len(content)) {
		t.Fatalf("size = %d, want %d", p.Size, len(content))
	}
	wantKey := "sha256/" + p.Checksum[:2] + "/" + p.Checksum[2:]
	if p.StorageKey != wantKey {
		t.Fatalf("storage key = %s, want %s", p.StorageKey, wantKey)
	}
	path, err := s.Path(p.StorageKey)
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("published file unreadable: %v", err)
	}
	if string(got) != string(content) {
		t.Fatalf("content mismatch: %q", got)
	}
}

func TestIngestDeduplicatesIdenticalContent(t *testing.T) {
	s, _ := newTestStore(t)
	content := []byte("dedupe me")

	first, err := s.Ingest(strings.NewReader(string(content)))
	if err != nil {
		t.Fatal(err)
	}
	second, err := s.Ingest(strings.NewReader(string(content)))
	if err != nil {
		t.Fatal(err)
	}
	if first.StorageKey != second.StorageKey {
		t.Fatalf("same content produced different keys: %s vs %s", first.StorageKey, second.StorageKey)
	}
	// No staging debris may remain after either path.
	staging := filepath.Join(s.root, "artifacts", "staging")
	entries, err := os.ReadDir(staging)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging not empty: %d entries", len(entries))
	}
}

func TestIngestFailureLeavesNoDebris(t *testing.T) {
	s, _ := newTestStore(t)
	if _, err := s.Ingest(failingReader{}); err == nil {
		t.Fatal("expected read failure")
	}
	staging := filepath.Join(s.root, "artifacts", "staging")
	entries, _ := os.ReadDir(staging)
	if len(entries) != 0 {
		t.Fatalf("failed ingest left %d staged file(s)", len(entries))
	}
}

type failingReader struct{}

func (failingReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestPathRejectsMalformedKeys(t *testing.T) {
	s, _ := newTestStore(t)
	for _, key := range []string{
		"", "sha256/", "sha256/../../etc/passwd",
		"sha256/zz/zz", strings.Repeat("a", 64), "sha256/" + strings.Repeat("a", 64),
		"sha256/" + strings.Repeat("a", 63), "sha256/ab/" + strings.Repeat("0", 61),
	} {
		if _, err := s.Path(key); err == nil {
			t.Errorf("key %q accepted", key)
		}
	}
	valid := "sha256/ab/" + strings.Repeat("0", 62)
	if _, err := s.Path(valid); err != nil {
		t.Errorf("valid key rejected: %v", err)
	}
}

func TestVerifyReferencedFiles(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()

	// Empty metadata passes trivially.
	if err := s.VerifyReferencedFiles(ctx, db); err != nil {
		t.Fatalf("empty references: %v", err)
	}

	p, err := s.Ingest(strings.NewReader("referenced"))
	if err != nil {
		t.Fatal(err)
	}
	insertArtifact(t, db, p.StorageKey, p.Size)

	if err := s.VerifyReferencedFiles(ctx, db); err != nil {
		t.Fatalf("referenced file present: %v", err)
	}

	// Wrong size recorded in metadata must fail.
	if _, err := db.Exec(`UPDATE artifacts SET size = 999`); err != nil {
		t.Fatal(err)
	}
	if err := s.VerifyReferencedFiles(ctx, db); err == nil || !strings.Contains(err.Error(), "size") {
		t.Fatalf("expected size mismatch, got %v", err)
	}

	// Missing file must fail with the key named.
	if _, err := db.Exec(`UPDATE artifacts SET size = ?`, p.Size); err != nil {
		t.Fatal(err)
	}
	path, _ := s.Path(p.StorageKey)
	os.Remove(path)
	if err := s.VerifyReferencedFiles(ctx, db); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing file error, got %v", err)
	}
}

func TestSweepQuarantinesOrphansOnly(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()

	// A referenced file, an old orphan, and a fresh orphan.
	kept, err := s.Ingest(strings.NewReader("kept"))
	if err != nil {
		t.Fatal(err)
	}
	insertArtifact(t, db, kept.StorageKey, kept.Size)

	oldOrphan, err := s.Ingest(strings.NewReader("old orphan"))
	if err != nil {
		t.Fatal(err)
	}
	freshOrphan, err := s.Ingest(strings.NewReader("fresh orphan"))
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Sweep(ctx, db, time.Hour, slog.Default()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	// Fresh orphan must still be at its published location (grace period).
	freshPath, _ := s.Path(freshOrphan.StorageKey)
	if _, err := os.Stat(freshPath); err != nil {
		t.Fatalf("fresh orphan swept before grace: %v", err)
	}

	// Age both beyond the grace period.
	oldTime := time.Now().Add(-2 * time.Hour)
	for _, p := range []string{oldOrphan.StorageKey, freshOrphan.StorageKey} {
		path, _ := s.Path(p)
		if err := os.Chtimes(path, oldTime, oldTime); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.Sweep(ctx, db, time.Hour, slog.Default()); err != nil {
		t.Fatalf("sweep: %v", err)
	}

	keptPath, _ := s.Path(kept.StorageKey)
	if _, err := os.Stat(keptPath); err != nil {
		t.Fatalf("referenced file was quarantined: %v", err)
	}
	orphanPath, _ := s.Path(oldOrphan.StorageKey)
	if _, err := os.Stat(orphanPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old orphan still published: %v", err)
	}
	// The old orphan now lives under quarantine, path preserved relative to
	// the data root.
	rest := strings.TrimPrefix(oldOrphan.StorageKey, "sha256/")
	qPath := filepath.Join(s.root, "artifacts", "quarantine", "artifacts", "sha256", rest[:2], rest[3:])
	if _, err := os.Stat(qPath); err != nil {
		t.Fatalf("orphan not quarantined: %v", err)
	}
}

func TestSweepQuarantinesStagingDebris(t *testing.T) {
	s, db := newTestStore(t)
	ctx := context.Background()

	staging := filepath.Join(s.root, "artifacts", "staging")
	if err := os.MkdirAll(staging, 0o755); err != nil {
		t.Fatal(err)
	}
	debris := filepath.Join(staging, "ingest-abandoned")
	os.WriteFile(debris, []byte("half written"), 0o644)
	oldTime := time.Now().Add(-2 * time.Hour)
	os.Chtimes(debris, oldTime, oldTime)

	if err := s.Sweep(ctx, db, time.Hour, slog.Default()); err != nil {
		t.Fatalf("sweep: %v", err)
	}
	qPath := filepath.Join(s.root, "artifacts", "quarantine", "artifacts", "staging", "ingest-abandoned")
	if _, err := os.Stat(qPath); err != nil {
		t.Fatalf("staging debris not quarantined: %v", err)
	}
	if _, err := os.Stat(debris); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("staging debris still present: %v", err)
	}
}

func insertArtifact(t *testing.T, db *sql.DB, key string, size int64) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO artifacts (id, workspace, media_type, size, checksum, storage_key, created_at)
		VALUES ('art-test', 'default', 'application/octet-stream', ?, ?, ?, ?)`,
		size, strings.TrimPrefix(key, "sha256/"), key, time.Now().UTC().UnixNano())
	if err != nil {
		t.Fatal(err)
	}
}
