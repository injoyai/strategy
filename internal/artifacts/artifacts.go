// Package artifacts implements the content-addressed file protocol for
// datasets, manifests, and other large payloads (M0-04):
//
//  1. Ingest writes to a randomly named staging file inside the service-managed
//     data root; client-supplied paths are never joined.
//  2. Content streams to disk while SHA-256 is computed on the fly.
//  3. The staged file is atomically renamed (same volume) to its
//     content-addressed location under sha256/<xx>/<rest>. At this point no
//     metadata references it, so it is invisible through the API.
//  4. The caller makes the file visible by recording a reference (an
//     artifacts row) in the same metadata transaction as the business state.
//     If that transaction fails, the file becomes a recoverable orphan —
//     never a committed record pointing at a missing file.
//  5. At startup VerifyReferencedFiles checks that every referenced file
//     exists with the recorded size. Orphaned files are quarantined by Sweep
//     after a grace period, never deleted at startup.
package artifacts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// layout directories, relative to the data root.
const (
	dirArtifacts  = "artifacts"
	dirStaging    = dirArtifacts + "/staging"
	dirContent    = dirArtifacts + "/sha256"
	dirQuarantine = dirArtifacts + "/quarantine"
)

// Store manages artifact files under a service-managed data root.
type Store struct {
	root string
	db   *sql.DB
}

// New returns a Store rooted at the configured data directory. The db handle
// backs the metadata half of the protocol (Record/Get/Open); the file half
// stays under root.
func New(root string, db *sql.DB) *Store { return &Store{root: root, db: db} }

// Artifact is the metadata record of one published file.
type Artifact struct {
	ID         string
	Name       string
	MediaType  string
	Checksum   string
	Size       int64
	StorageKey string
	CreatedAt  time.Time
}

// RecordInput carries the caller-supplied metadata for Record.
type RecordInput struct {
	Name      string
	MediaType string
	Workspace string
	Placement Placement
}

// Record publishes a placement: it inserts the artifacts row in one
// transaction and resolves to the canonical record. Content-addressed
// storage means the same bytes always resolve to the same artifact id, so
// re-recording an identical upload is idempotent.
func (s *Store) Record(ctx context.Context, in RecordInput) (Artifact, error) {
	if in.Placement.Checksum == "" || in.Placement.StorageKey == "" {
		return Artifact{}, fmt.Errorf("artifacts: record requires an ingested placement")
	}
	workspace := in.Workspace
	if workspace == "" {
		workspace = "default"
	}
	id, err := ports.RandomIDGenerator{Prefix: "art"}.NewID()
	if err != nil {
		return Artifact{}, err
	}
	now := time.Now().UTC().UnixNano()
	_, err = s.db.ExecContext(ctx, `
INSERT INTO artifacts (id, workspace, name, media_type, size, checksum, storage_key, published_at, created_at)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT (storage_key) DO NOTHING`,
		id, workspace, in.Name, in.MediaType, in.Placement.Size, in.Placement.Checksum,
		in.Placement.StorageKey, now, now)
	if err != nil {
		return Artifact{}, fmt.Errorf("artifacts: record %s: %w", in.Placement.StorageKey, err)
	}
	return s.GetByStorageKey(ctx, in.Placement.StorageKey)
}

// Get returns the artifact metadata for id.
func (s *Store) Get(ctx context.Context, id string) (Artifact, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, media_type, size, checksum, storage_key, created_at
FROM artifacts WHERE id = ?`, id)
	return scanArtifact(row)
}

// GetByStorageKey returns the artifact metadata for a content-addressed key.
func (s *Store) GetByStorageKey(ctx context.Context, key string) (Artifact, error) {
	row := s.db.QueryRowContext(ctx, `
SELECT id, name, media_type, size, checksum, storage_key, created_at
FROM artifacts WHERE storage_key = ?`, key)
	return scanArtifact(row)
}

// Open resolves id and opens its content file for streaming.
func (s *Store) Open(ctx context.Context, id string) (Artifact, io.ReadCloser, error) {
	art, err := s.Get(ctx, id)
	if err != nil {
		return Artifact{}, nil, err
	}
	path, err := s.Path(art.StorageKey)
	if err != nil {
		return Artifact{}, nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return Artifact{}, nil, fmt.Errorf("artifacts: open %s: %w", art.StorageKey, err)
	}
	return art, f, nil
}

type rowScanner interface{ Scan(dest ...any) error }

func scanArtifact(row rowScanner) (Artifact, error) {
	var art Artifact
	var createdNano int64
	err := row.Scan(&art.ID, &art.Name, &art.MediaType, &art.Size, &art.Checksum, &art.StorageKey, &createdNano)
	if errors.Is(err, sql.ErrNoRows) {
		return Artifact{}, domain.NewError(domain.CodeResourceNotFound, "artifact not found")
	}
	if err != nil {
		return Artifact{}, fmt.Errorf("artifacts: scan artifact: %w", err)
	}
	art.CreatedAt = time.Unix(0, createdNano).UTC()
	return art, nil
}

// Placement describes where Ingest parked a payload and how to reference it.
type Placement struct {
	// Checksum is the hex SHA-256 of the content.
	Checksum string
	// Size is the byte count as written.
	Size int64
	// StorageKey is the content-addressed key ("sha256/<xx>/<rest>") stored
	// in metadata to reference the file.
	StorageKey string
}

// Ingest streams r into the store and returns its placement. The staged
// file is removed on any error, so failed ingests leave no debris behind.
func (s *Store) Ingest(r io.Reader) (Placement, error) {
	staging := filepath.Join(s.root, filepath.FromSlash(dirStaging))
	if err := os.MkdirAll(staging, 0o755); err != nil {
		return Placement{}, fmt.Errorf("artifacts: create staging dir: %w", err)
	}
	tmp, err := os.CreateTemp(staging, "ingest-*")
	if err != nil {
		return Placement{}, fmt.Errorf("artifacts: create staging file: %w", err)
	}
	staged := tmp.Name()
	removeStaged := func() { _ = os.Remove(staged) }

	hash := sha256.New()
	size, err := io.Copy(io.MultiWriter(tmp, hash), r)
	if err != nil {
		tmp.Close()
		removeStaged()
		return Placement{}, fmt.Errorf("artifacts: stream content: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		removeStaged()
		return Placement{}, fmt.Errorf("artifacts: sync staged file: %w", err)
	}
	if err := tmp.Close(); err != nil {
		removeStaged()
		return Placement{}, fmt.Errorf("artifacts: close staged file: %w", err)
	}

	sum := hex.EncodeToString(hash.Sum(nil))
	key := "sha256/" + sum[:2] + "/" + sum[2:]
	final, err := s.Path(key)
	if err != nil {
		removeStaged()
		return Placement{}, err
	}

	if _, err := os.Stat(final); err == nil {
		// Identical content is already published; the staged copy is redundant.
		removeStaged()
	} else if errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(filepath.Dir(final), 0o755); err != nil {
			removeStaged()
			return Placement{}, fmt.Errorf("artifacts: create content dir: %w", err)
		}
		// Same-volume rename: atomic, so a concurrent reader either sees the
		// full file or nothing at all.
		if err := os.Rename(staged, final); err != nil {
			removeStaged()
			return Placement{}, fmt.Errorf("artifacts: publish %s: %w", key, err)
		}
	} else {
		removeStaged()
		return Placement{}, fmt.Errorf("artifacts: stat %s: %w", key, err)
	}

	return Placement{Checksum: sum, Size: size, StorageKey: key}, nil
}

// Path resolves a storage key to its absolute file location. Keys from
// metadata are validated strictly: only the "sha256/<xx>/<rest>" form
// produced by Ingest is accepted (2 hex + "/" + 62 hex), so client-controlled
// strings can never escape the content directory.
func (s *Store) Path(key string) (string, error) {
	rest, ok := strings.CutPrefix(key, "sha256/")
	if !ok || len(rest) != 65 || rest[2] != '/' ||
		!isHex(rest[:2]) || !isHex(rest[3:]) {
		return "", fmt.Errorf("artifacts: invalid storage key %q", key)
	}
	return filepath.Join(s.root, filepath.FromSlash(dirContent), rest[:2], rest[3:]), nil
}

func isHex(s string) bool {
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

// VerifyReferencedFiles is the startup check: every file referenced by the
// artifacts table must exist with the recorded size. Full checksum
// re-verification is deliberately not done here — that belongs to the backup
// restore procedure, where correctness matters more than startup latency.
func (s *Store) VerifyReferencedFiles(ctx context.Context, db *sql.DB) error {
	rows, err := db.QueryContext(ctx, `SELECT storage_key, size FROM artifacts`)
	if err != nil {
		return fmt.Errorf("artifacts: list references: %w", err)
	}
	defer rows.Close()
	var missing []string
	for rows.Next() {
		var key string
		var size int64
		if err := rows.Scan(&key, &size); err != nil {
			return fmt.Errorf("artifacts: scan references: %w", err)
		}
		path, err := s.Path(key)
		if err != nil {
			return fmt.Errorf("artifacts: referenced %w", err)
		}
		info, err := os.Stat(path)
		if err != nil {
			missing = append(missing, key)
			continue
		}
		if info.Size() != size {
			return fmt.Errorf("artifacts: referenced file %s has size %d, metadata says %d",
				key, info.Size(), size)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("artifacts: list references: %w", err)
	}
	if len(missing) > 0 {
		return fmt.Errorf("artifacts: %d referenced file(s) missing on disk: %s",
			len(missing), strings.Join(missing, ", "))
	}
	return nil
}

// Sweep quarantines unreferenced files older than grace: published artifacts
// that lost their metadata race and staging debris from crashed ingests.
// Files are moved under quarantine/<relative-path>, never deleted; operators
// reclaim the space after review. Metadata-referenced files are never moved.
func (s *Store) Sweep(ctx context.Context, db *sql.DB, grace time.Duration, log *slog.Logger) error {
	referenced, err := s.referencedKeys(ctx, db)
	if err != nil {
		return err
	}
	cutoff := time.Now().Add(-grace)

	contentRoot := filepath.Join(s.root, filepath.FromSlash(dirContent))
	err = filepath.WalkDir(contentRoot, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil // nothing published yet
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(contentRoot, path)
		if relErr != nil {
			return relErr
		}
		key := "sha256/" + filepath.ToSlash(rel)
		if referenced[key] {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(cutoff) {
			return nil // too recent to judge; may still be mid-ingest
		}
		return s.quarantine(path, filepath.FromSlash(dirContent)+string(filepath.Separator)+rel)
	})
	if err != nil {
		return fmt.Errorf("artifacts: sweep content: %w", err)
	}

	staging := filepath.Join(s.root, filepath.FromSlash(dirStaging))
	err = filepath.WalkDir(staging, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		if d.IsDir() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		if info.ModTime().After(cutoff) {
			return nil
		}
		return s.quarantine(path, filepath.FromSlash(dirStaging)+string(filepath.Separator)+filepath.Base(path))
	})
	if err != nil {
		return fmt.Errorf("artifacts: sweep staging: %w", err)
	}
	return nil
}

func (s *Store) referencedKeys(ctx context.Context, db *sql.DB) (map[string]bool, error) {
	rows, err := db.QueryContext(ctx, `SELECT storage_key FROM artifacts`)
	if err != nil {
		return nil, fmt.Errorf("artifacts: list references: %w", err)
	}
	defer rows.Close()
	out := make(map[string]bool)
	for rows.Next() {
		var key string
		if err := rows.Scan(&key); err != nil {
			return nil, fmt.Errorf("artifacts: scan references: %w", err)
		}
		out[key] = true
	}
	return out, rows.Err()
}

// quarantine moves src under quarantine/, preserving a path relative to the
// data root so repeated sweeps never collide.
func (s *Store) quarantine(src, rel string) error {
	dst := filepath.Join(s.root, filepath.FromSlash(dirQuarantine), rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return fmt.Errorf("create quarantine dir: %w", err)
	}
	if err := os.Rename(src, dst); err != nil {
		return fmt.Errorf("quarantine %s: %w", src, err)
	}
	return nil
}
