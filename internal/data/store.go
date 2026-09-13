// Package data implements the M0-06 data plane on the SQLite store: batch
// persistence with content addressing, fail-closed snapshot publishing and
// point-in-time reads over observations. The store persists evidence and
// derives only aggregate quality labels; it never re-derives, repairs or
// drops individual findings, and publishing never silently downgrades a
// failed gate into a published snapshot.
package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

const (
	workspaceDefault = "default"

	sortID = "id"

	defaultBatchLimit    = 50
	maxBatchLimit        = 200
	defaultSnapshotLimit = 50
	maxSnapshotLimit     = 200
	defaultQueryLimit    = 200
	maxQueryLimit        = 1000
)

// Store is the SQLite-backed DataStore. clock and sum are injectable so
// tests can freeze time and checksums deterministically.
type Store struct {
	db    *sql.DB
	clock ports.Clock
	sum   ports.Checksummer
}

var _ ports.DataStore = (*Store)(nil)

// New builds a data store. clock defaults to the system clock.
func New(db *sql.DB, clock ports.Clock) *Store {
	if clock == nil {
		clock = ports.SystemClock{}
	}
	return &Store{db: db, clock: clock, sum: ports.SHA256Checksummer{}}
}

func (s *Store) newID(prefix string) domain.ID {
	id, err := ports.RandomIDGenerator{Prefix: prefix}.NewID()
	if err != nil {
		// RandomIDGenerator only fails when the OS entropy source fails;
		// that is unrecoverable for a single-process service.
		panic(fmt.Sprintf("data: generate id: %v", err))
	}
	return id
}

func (s *Store) withTx(ctx context.Context, fn func(tx *sql.Tx) error) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("data: begin: %w", err)
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("data: commit: %w", err)
	}
	return nil
}

// placeholders renders "?, ?, ..." with n marks for IN clauses.
func placeholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// escapeLike escapes SQL LIKE metacharacters so a user-supplied q matches
// literally; every LIKE using it must pair with ESCAPE '\'.
func escapeLike(s string) string {
	return strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`).Replace(s)
}

func marshalJSON(v any) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "data: encode %T", v)
	}
	return b, nil
}

// marshalIssues writes an issue list; empty stays "[]" so the wire never
// renders null.
func marshalIssues(issues []domain.Issue) ([]byte, error) {
	if len(issues) == 0 {
		return []byte("[]"), nil
	}
	return marshalJSON(issues)
}

// unmarshalIssues reads an issue list stored by this package. Undecodable
// JSON means our own write path or a migration is broken, so it fails as an
// internal error instead of being swallowed.
func unmarshalIssues(raw []byte) ([]domain.Issue, error) {
	if len(raw) == 0 {
		return []domain.Issue{}, nil
	}
	var issues []domain.Issue
	if err := json.Unmarshal(raw, &issues); err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "data: decode issues")
	}
	return issues, nil
}

// worstSeverity reduces an issue list to the aggregate quality label stored
// on batches and snapshots: "clean", "info", "warning" or "error". Error
// wins immediately; the scan never needs to continue.
func worstSeverity(issues []domain.Issue) string {
	worst := "clean"
	for _, issue := range issues {
		switch issue.Severity {
		case domain.SeverityError:
			return domain.SeverityError
		case domain.SeverityWarning:
			worst = domain.SeverityWarning
		case domain.SeverityInfo:
			if worst == "clean" {
				worst = domain.SeverityInfo
			}
		}
	}
	return worst
}

// unionIssues merges the issue lists of the batches feeding a snapshot into
// one deduplicated list, preserving first-seen order. The result is never
// nil so the wire never renders null.
func unionIssues(groups ...[]domain.Issue) []domain.Issue {
	seen := make(map[string]struct{})
	merged := make([]domain.Issue, 0)
	for _, group := range groups {
		for _, issue := range group {
			key := issue.Code + "\x00" + issue.Path + "\x00" + issue.Message
			if _, ok := seen[key]; ok {
				continue
			}
			seen[key] = struct{}{}
			merged = append(merged, issue)
		}
	}
	return merged
}

// clampLimit bounds a caller-supplied page size: non-positive takes the
// default, anything above max is capped.
func clampLimit(n, def, max int) int {
	if n <= 0 {
		return def
	}
	if n > max {
		return max
	}
	return n
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// rowRecord is the canonical byte form of one observation inside a batch.
// It exists only for content addressing: times are normalized to UTC and
// nil maps and pointers collapse to their empty forms, so two rows with the
// same meaning hash identically regardless of how the provider spelled them.
type rowRecord struct {
	InstrumentID string                  `json:"instrument_id,omitempty"`
	EntityID     string                  `json:"entity_id,omitempty"`
	Dataset      string                  `json:"dataset"`
	EventTime    time.Time               `json:"event_time"`
	PeriodEnd    *string                 `json:"period_end,omitempty"`
	Effective    *domain.Interval        `json:"effective,omitempty"`
	Values       map[string]domain.Value `json:"values"`
	Provenance   domain.Provenance       `json:"provenance"`
}

// newRowRecord canonicalizes one observation for hashing and persistence.
func newRowRecord(o domain.Observation) rowRecord {
	rec := rowRecord{
		Dataset:    o.Dataset,
		EventTime:  o.EventTime.UTC(),
		Values:     o.Values,
		Provenance: o.Provenance,
	}
	if rec.Values == nil {
		rec.Values = map[string]domain.Value{}
	}
	if o.InstrumentID != nil {
		rec.InstrumentID = o.InstrumentID.String()
	}
	if o.EntityID != nil {
		rec.EntityID = o.EntityID.String()
	}
	if o.PeriodEnd != nil {
		rec.PeriodEnd = o.PeriodEnd
	}
	if o.Effective != nil {
		effective := domain.Interval{
			From: o.Effective.From.UTC(),
			To:   o.Effective.To.UTC(),
		}
		rec.Effective = &effective
	}
	rec.Provenance.AvailableAt = o.Provenance.AvailableAt.UTC()
	rec.Provenance.IngestedAt = o.Provenance.IngestedAt.UTC()
	if o.Provenance.PublishedAt != nil {
		publishedAt := o.Provenance.PublishedAt.UTC()
		rec.Provenance.PublishedAt = &publishedAt
	}
	return rec
}

// rowLess orders rows deterministically so the batch checksum does not
// depend on the order the provider emitted pages in.
func rowLess(a, b rowRecord) bool {
	if a.InstrumentID != b.InstrumentID {
		return a.InstrumentID < b.InstrumentID
	}
	if a.EntityID != b.EntityID {
		return a.EntityID < b.EntityID
	}
	if !a.EventTime.Equal(b.EventTime) {
		return a.EventTime.Before(b.EventTime)
	}
	if a.Provenance.RevisionID != b.Provenance.RevisionID {
		return a.Provenance.RevisionID < b.Provenance.RevisionID
	}
	return a.Dataset < b.Dataset
}

// observationArgs marshals one canonical row into the column values of the
// observations insert: times are unix nanoseconds (UTC), the effective
// interval is bound only when both bounds exist, and published_at NULL
// means the upstream never announced one.
func observationArgs(rec rowRecord, batchID, dataset, frequency string) ([]any, error) {
	valuesJSON, err := marshalJSON(rec.Values)
	if err != nil {
		return nil, err
	}
	provenanceJSON, err := marshalJSON(rec.Provenance)
	if err != nil {
		return nil, err
	}
	periodEnd := ""
	if rec.PeriodEnd != nil {
		periodEnd = *rec.PeriodEnd
	}
	var effectiveFrom, effectiveTo, publishedAt any
	if rec.Effective != nil {
		effectiveFrom = rec.Effective.From.UnixNano()
		effectiveTo = rec.Effective.To.UnixNano()
	}
	if rec.Provenance.PublishedAt != nil {
		publishedAt = rec.Provenance.PublishedAt.UnixNano()
	}
	return []any{
		workspaceDefault,
		batchID,
		dataset,
		frequency,
		rec.InstrumentID,
		rec.EntityID,
		rec.EventTime.UnixNano(),
		periodEnd,
		effectiveFrom,
		effectiveTo,
		valuesJSON,
		provenanceJSON,
		rec.Provenance.AvailableAt.UnixNano(),
		rec.Provenance.IngestedAt.UnixNano(),
		rec.Provenance.RevisionID,
		rec.Provenance.SupersedesRevisionID,
		publishedAt,
	}, nil
}

// Append persists one batch: the batch row plus every observation, in one
// transaction. Rows are content-addressed before insert so byte-identical
// rows inside one input collapse (overlapping provider pages), and the
// batch checksum is the SHA-256 of the sorted canonical rows. Issues come
// from the pipeline verbatim; the store derives only the aggregate quality
// label and never gates the append itself — severity gating happens at
// publish time.
func (s *Store) Append(ctx context.Context, in ports.BatchInput) (domain.IngestReceipt, error) {
	if in.JobID == "" {
		return domain.IngestReceipt{}, domain.NewError(domain.CodeValidationInvalid, "data: batch job_id is required")
	}
	if in.Dataset == "" {
		return domain.IngestReceipt{}, domain.NewError(domain.CodeValidationInvalid, "data: batch dataset is required")
	}
	if in.Frequency == "" {
		return domain.IngestReceipt{}, domain.NewError(domain.CodeValidationInvalid, "data: batch frequency is required")
	}

	seen := make(map[string]struct{}, len(in.Observations))
	rows := make([]rowRecord, 0, len(in.Observations))
	for i, o := range in.Observations {
		if o.EventTime.IsZero() {
			return domain.IngestReceipt{}, domain.NewError(domain.CodeValidationInvalid, "data: observations[%d] has zero event_time", i)
		}
		if o.Provenance.AvailableAt.IsZero() {
			return domain.IngestReceipt{}, domain.NewError(domain.CodeValidationInvalid, "data: observations[%d] has zero available_at", i)
		}
		if o.Provenance.IngestedAt.IsZero() {
			return domain.IngestReceipt{}, domain.NewError(domain.CodeValidationInvalid, "data: observations[%d] has zero ingested_at", i)
		}
		if o.Dataset != in.Dataset {
			return domain.IngestReceipt{}, domain.NewError(domain.CodeValidationInvalid, "data: observations[%d] dataset %q does not match batch dataset %q", i, o.Dataset, in.Dataset)
		}
		if o.Effective != nil {
			if _, err := domain.NewInterval(o.Effective.From, o.Effective.To); err != nil {
				return domain.IngestReceipt{}, domain.Wrap(err, domain.CodeValidationInterval, "data: observations[%d] effective invalid", i)
			}
		}
		rec := newRowRecord(o)
		encoded, err := marshalJSON(rec)
		if err != nil {
			return domain.IngestReceipt{}, err
		}
		key := s.sum.Checksum(encoded)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		rows = append(rows, rec)
	}
	sort.Slice(rows, func(i, j int) bool { return rowLess(rows[i], rows[j]) })

	encodedRows, err := marshalJSON(rows)
	if err != nil {
		return domain.IngestReceipt{}, err
	}
	checksum := s.sum.Checksum(encodedRows)

	createdAt := s.clock.Now().UTC()
	batchID := s.newID("batch")
	issuesJSON, err := marshalIssues(in.Issues)
	if err != nil {
		return domain.IngestReceipt{}, err
	}
	issues := in.Issues
	if issues == nil {
		issues = []domain.Issue{}
	}

	var rawManifest any
	if len(in.RawManifest) > 0 {
		rawManifest = []byte(in.RawManifest)
	}

	err = s.withTx(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO batches (
				id, workspace, job_id, request_evidence, raw_manifest,
				normalized_manifest, quality_status, checksums, created_at,
				dataset_id, frequency, row_count, checksum, issues, ready
			) VALUES (?, ?, ?, NULL, ?, NULL, ?, NULL, ?, ?, ?, ?, ?, ?, 1)`,
			batchID, workspaceDefault, in.JobID, rawManifest, worstSeverity(in.Issues),
			createdAt.UnixNano(), in.Dataset, in.Frequency, len(rows), checksum, issuesJSON)
		if err != nil {
			return fmt.Errorf("data: insert batch: %w", err)
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO observations (
				workspace, batch_id, dataset, frequency, instrument_id,
				entity_id, event_time, period_end, effective_from, effective_to,
				values_json, provenance_json, available_at, ingested_at,
				revision_id, supersedes, published_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("data: prepare observation insert: %w", err)
		}
		defer stmt.Close()
		for _, rec := range rows {
			args, err := observationArgs(rec, batchID.String(), in.Dataset, in.Frequency)
			if err != nil {
				return err
			}
			if _, err := stmt.ExecContext(ctx, args...); err != nil {
				return fmt.Errorf("data: insert observation: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return domain.IngestReceipt{}, err
	}
	return domain.IngestReceipt{BatchID: batchID, Rows: int64(len(rows)), Issues: issues}, nil
}

// scanner is satisfied by both *sql.Row and *sql.Rows so shared scans work
// for get-one and list paths.
type scanner interface {
	Scan(dest ...any) error
}

// rowsQuerier is satisfied by both *sql.DB and *sql.Tx.
type rowsQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

const batchColumns = `id, job_id, dataset_id, row_count, checksum, created_at, issues, ready`

func scanBatch(row scanner) (domain.Batch, error) {
	var (
		b         domain.Batch
		issuesRaw []byte
		ready     int
		createdAt int64
	)
	if err := row.Scan(&b.ID, &b.JobID, &b.DatasetID, &b.RowCount, &b.Checksum, &createdAt, &issuesRaw, &ready); err != nil {
		return domain.Batch{}, err
	}
	b.CreatedAt = time.Unix(0, createdAt).UTC()
	issues, err := unmarshalIssues(issuesRaw)
	if err != nil {
		return domain.Batch{}, err
	}
	b.Issues = issues
	b.Ready = ready != 0
	return b, nil
}

// GetBatch returns one batch by id. Unknown ids are wire not-found errors;
// SQL text never leaks into HTTP.
func (s *Store) GetBatch(ctx context.Context, id domain.ID) (domain.Batch, error) {
	b, err := scanBatch(s.db.QueryRowContext(ctx, `
		SELECT `+batchColumns+`
		FROM batches
		WHERE id = ? AND workspace = ?`, id, workspaceDefault))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Batch{}, domain.NewError(domain.CodeResourceNotFound, "data: batch %s not found", id)
	}
	if err != nil {
		return domain.Batch{}, fmt.Errorf("data: get batch: %w", err)
	}
	return b, nil
}

// ListBatches lists batches with an id-only keyset cursor: the server's
// pagination layer decodes the opaque cursor and hands AfterID/Sort/Limit
// here, and NextCursor is the last item's id — "" when the page ended the
// collection. Sort accepts "id" ascending or anything else descending (the
// server contract validates the enum). Q is a substring filter on the
// batch id.
func (s *Store) ListBatches(ctx context.Context, f ports.BatchFilter) (domain.PageResult[domain.Batch], error) {
	asc := f.Sort == sortID
	limit := clampLimit(f.Limit, defaultBatchLimit, maxBatchLimit)

	query := new(strings.Builder)
	query.WriteString(`
		SELECT ` + batchColumns + `
		FROM batches
		WHERE workspace = ?`)
	args := []any{workspaceDefault}
	if f.JobID != "" {
		query.WriteString(` AND job_id = ?`)
		args = append(args, f.JobID)
	}
	if f.Dataset != "" {
		query.WriteString(` AND dataset_id = ?`)
		args = append(args, f.Dataset)
	}
	if f.Q != "" {
		query.WriteString(` AND id LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Q)+"%")
	}
	if f.AfterID != "" {
		if asc {
			query.WriteString(` AND id > ?`)
		} else {
			query.WriteString(` AND id < ?`)
		}
		args = append(args, f.AfterID)
	}
	if asc {
		query.WriteString(` ORDER BY id ASC`)
	} else {
		query.WriteString(` ORDER BY id DESC`)
	}
	query.WriteString(` LIMIT ?`)
	args = append(args, limit+1)

	rs, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return domain.PageResult[domain.Batch]{}, fmt.Errorf("data: list batches: %w", err)
	}
	defer rs.Close()

	items := make([]domain.Batch, 0)
	for rs.Next() {
		b, err := scanBatch(rs)
		if err != nil {
			return domain.PageResult[domain.Batch]{}, fmt.Errorf("data: scan batch: %w", err)
		}
		items = append(items, b)
	}
	if err := rs.Err(); err != nil {
		return domain.PageResult[domain.Batch]{}, fmt.Errorf("data: iterate batches: %w", err)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID.String()
	}
	return domain.PageResult[domain.Batch]{Items: items, NextCursor: next}, nil
}

// manifestEntry is one line of the snapshot manifest: a batch id and its
// content checksum at publish time.
type manifestEntry struct {
	BatchID  string `json:"batch_id"`
	Checksum string `json:"checksum"`
}

// canonicalManifest is the order-independent manifest a snapshot's hash is
// taken over: batch entries sorted by id plus the publish policy. The same
// batch set and policy always hash identically regardless of the order the
// request listed batches in, which is what makes idempotent republish
// possible.
type canonicalManifest struct {
	StrictPIT bool            `json:"strict_pit"`
	Batches   []manifestEntry `json:"batches"`
}

// PublishSnapshot freezes the requested batches into an immutable snapshot,
// fail-closed: any error-severity issue in any contributing batch blocks
// publication, and strict-PIT snapshots additionally reject observations
// whose provenance carries no published time. The manifest hash is taken
// over the canonical (order-independent) manifest, so republishing the same
// batch set under the same policy returns the existing snapshot instead of
// creating a duplicate. The snapshot row, its manifest hash and the batch
// membership are written in one transaction, so a crash never leaves a
// half-published snapshot.
func (s *Store) PublishSnapshot(ctx context.Context, req domain.SnapshotRequest) (domain.Snapshot, error) {
	if err := req.Validate(); err != nil {
		return domain.Snapshot{}, err
	}
	seen := make(map[domain.ID]struct{}, len(req.BatchIDs))
	for _, id := range req.BatchIDs {
		if _, dup := seen[id]; dup {
			return domain.Snapshot{}, domain.NewError(domain.CodeValidationInvalid, "snapshot: batch_ids contains duplicate %q", id)
		}
		seen[id] = struct{}{}
	}

	createdAt := s.clock.Now().UTC()
	id := s.newID("snap")
	var (
		entries       = make([]manifestEntry, 0, len(req.BatchIDs))
		groups        = make([][]domain.Issue, 0, len(req.BatchIDs))
		errCount      int
		unpublished   int
		manifestHash  string
		qualityIssues []domain.Issue
		existingID    domain.ID
	)
	err := s.withTx(ctx, func(tx *sql.Tx) error {
		stmt, err := tx.PrepareContext(ctx, `SELECT checksum, issues FROM batches WHERE id = ? AND workspace = ?`)
		if err != nil {
			return fmt.Errorf("data: prepare batch select: %w", err)
		}
		defer stmt.Close()
		for _, batchID := range req.BatchIDs {
			var checksum string
			var issuesRaw []byte
			if err := stmt.QueryRowContext(ctx, batchID, workspaceDefault).Scan(&checksum, &issuesRaw); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return domain.NewError(domain.CodeResourceNotFound, "snapshot: batch %s not found", batchID)
				}
				return fmt.Errorf("data: get batch for snapshot: %w", err)
			}
			issues, err := unmarshalIssues(issuesRaw)
			if err != nil {
				return err
			}
			for _, issue := range issues {
				if issue.Severity == domain.SeverityError {
					errCount++
				}
			}
			entries = append(entries, manifestEntry{BatchID: batchID.String(), Checksum: checksum})
			groups = append(groups, issues)
		}
		if errCount > 0 {
			return domain.NewError(domain.CodeResourceConflict, "snapshot %q blocked by %d error-severity quality issue(s); publishing is fail-closed", req.Name, errCount)
		}
		if req.StrictPIT {
			query := `SELECT COUNT(1) FROM observations WHERE workspace = ? AND published_at IS NULL AND batch_id IN (` + placeholders(len(req.BatchIDs)) + `)`
			args := make([]any, 0, len(req.BatchIDs)+1)
			args = append(args, workspaceDefault)
			for _, batchID := range req.BatchIDs {
				args = append(args, batchID)
			}
			if err := tx.QueryRowContext(ctx, query, args...).Scan(&unpublished); err != nil {
				return fmt.Errorf("data: count unpublished observations: %w", err)
			}
			if unpublished > 0 {
				return domain.NewError(domain.CodeResourceConflict, "snapshot %q blocked by %d observation(s) without a published time; strict PIT requires verified availability", req.Name, unpublished)
			}
		}
		sorted := make([]manifestEntry, len(entries))
		copy(sorted, entries)
		sort.Slice(sorted, func(i, j int) bool { return sorted[i].BatchID < sorted[j].BatchID })
		encodedManifest, err := marshalJSON(canonicalManifest{StrictPIT: req.StrictPIT, Batches: sorted})
		if err != nil {
			return err
		}
		manifestHash = s.sum.Checksum(encodedManifest)
		var found string
		scanErr := tx.QueryRowContext(ctx, `SELECT id FROM snapshots WHERE workspace = ? AND manifest_hash = ?`, workspaceDefault, manifestHash).Scan(&found)
		if scanErr == nil {
			existingID = domain.ID(found)
			return nil
		}
		if !errors.Is(scanErr, sql.ErrNoRows) {
			return fmt.Errorf("data: find snapshot by manifest: %w", scanErr)
		}
		qualityIssues = unionIssues(groups...)
		qualityJSON, err := marshalIssues(qualityIssues)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO snapshots (
				id, workspace, manifest_hash, quality_status, policy_version,
				published_at, created_at, name, strict_pit, quality_issues
			) VALUES (?, ?, ?, ?, '', ?, ?, ?, ?, ?)`,
			id, workspaceDefault, manifestHash, worstSeverity(qualityIssues),
			createdAt.UnixNano(), createdAt.UnixNano(), req.Name, boolInt(req.StrictPIT), qualityJSON); err != nil {
			return fmt.Errorf("data: insert snapshot: %w", err)
		}
		for ordinal, batchID := range req.BatchIDs {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO snapshot_batches (snapshot_id, ordinal, batch_id, workspace)
				VALUES (?, ?, ?, ?)`,
				id, ordinal, batchID, workspaceDefault); err != nil {
				return fmt.Errorf("data: insert snapshot batch: %w", err)
			}
		}
		return nil
	})
	if err != nil {
		return domain.Snapshot{}, err
	}
	if existingID != "" {
		return s.GetSnapshot(ctx, existingID)
	}
	batchIDs := make([]domain.ID, len(req.BatchIDs))
	copy(batchIDs, req.BatchIDs)
	return domain.Snapshot{
		ID:            id,
		Name:          req.Name,
		BatchIDs:      batchIDs,
		StrictPIT:     req.StrictPIT,
		ManifestHash:  manifestHash,
		CreatedAt:     createdAt,
		QualityIssues: qualityIssues,
	}, nil
}

const snapshotColumns = `id, name, strict_pit, manifest_hash, created_at, quality_issues`

// snapshotBatchIDs returns the member batch ids of one snapshot in manifest
// order; the result is always non-nil so the wire never renders null.
func (s *Store) snapshotBatchIDs(ctx context.Context, q rowsQuerier, id domain.ID) ([]domain.ID, error) {
	rs, err := q.QueryContext(ctx, `
		SELECT batch_id FROM snapshot_batches
		WHERE snapshot_id = ? AND workspace = ?
		ORDER BY ordinal ASC`, id, workspaceDefault)
	if err != nil {
		return nil, fmt.Errorf("data: list snapshot batches: %w", err)
	}
	defer rs.Close()
	ids := make([]domain.ID, 0)
	for rs.Next() {
		var raw string
		if err := rs.Scan(&raw); err != nil {
			return nil, fmt.Errorf("data: scan snapshot batch: %w", err)
		}
		ids = append(ids, domain.ID(raw))
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("data: iterate snapshot batches: %w", err)
	}
	return ids, nil
}

// snapshotBatchIDsMany loads the member batch ids of many snapshots in one
// query so listing never degrades into N+1 round trips.
func (s *Store) snapshotBatchIDsMany(ctx context.Context, q rowsQuerier, ids []domain.ID) (map[string][]domain.ID, error) {
	out := make(map[string][]domain.ID, len(ids))
	if len(ids) == 0 {
		return out, nil
	}
	query := `
		SELECT snapshot_id, batch_id FROM snapshot_batches
		WHERE workspace = ? AND snapshot_id IN (` + placeholders(len(ids)) + `)
		ORDER BY snapshot_id ASC, ordinal ASC`
	args := make([]any, 0, len(ids)+1)
	args = append(args, workspaceDefault)
	for _, id := range ids {
		args = append(args, id)
	}
	rs, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("data: list snapshot batches: %w", err)
	}
	defer rs.Close()
	for rs.Next() {
		var snapID, batchID string
		if err := rs.Scan(&snapID, &batchID); err != nil {
			return nil, fmt.Errorf("data: scan snapshot batch: %w", err)
		}
		out[snapID] = append(out[snapID], domain.ID(batchID))
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("data: iterate snapshot batches: %w", err)
	}
	return out, nil
}

func (s *Store) scanSnapshot(row scanner) (domain.Snapshot, error) {
	var (
		snap      domain.Snapshot
		issuesRaw []byte
		strictPIT int
		createdAt int64
	)
	if err := row.Scan(&snap.ID, &snap.Name, &strictPIT, &snap.ManifestHash, &createdAt, &issuesRaw); err != nil {
		return domain.Snapshot{}, err
	}
	snap.StrictPIT = strictPIT != 0
	snap.CreatedAt = time.Unix(0, createdAt).UTC()
	issues, err := unmarshalIssues(issuesRaw)
	if err != nil {
		return domain.Snapshot{}, err
	}
	snap.QualityIssues = issues
	return snap, nil
}

// GetSnapshot returns one snapshot with its member batch ids in manifest
// order. Unknown ids are wire not-found errors.
func (s *Store) GetSnapshot(ctx context.Context, id domain.ID) (domain.Snapshot, error) {
	snap, err := s.scanSnapshot(s.db.QueryRowContext(ctx, `
		SELECT `+snapshotColumns+`
		FROM snapshots
		WHERE id = ? AND workspace = ?`, id, workspaceDefault))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Snapshot{}, domain.NewError(domain.CodeResourceNotFound, "data: snapshot %s not found", id)
	}
	if err != nil {
		return domain.Snapshot{}, fmt.Errorf("data: get snapshot: %w", err)
	}
	batchIDs, err := s.snapshotBatchIDs(ctx, s.db, id)
	if err != nil {
		return domain.Snapshot{}, err
	}
	snap.BatchIDs = batchIDs
	return snap, nil
}

// ListSnapshots lists snapshots with the same id-only keyset semantics as
// ListBatches; member batch ids are filled in one batched query.
func (s *Store) ListSnapshots(ctx context.Context, f ports.SnapshotFilter) (domain.PageResult[domain.Snapshot], error) {
	asc := f.Sort == sortID
	limit := clampLimit(f.Limit, defaultSnapshotLimit, maxSnapshotLimit)

	query := new(strings.Builder)
	query.WriteString(`
		SELECT ` + snapshotColumns + `
		FROM snapshots
		WHERE workspace = ?`)
	args := []any{workspaceDefault}
	if f.Q != "" {
		query.WriteString(` AND id LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Q)+"%")
	}
	if f.AfterID != "" {
		if asc {
			query.WriteString(` AND id > ?`)
		} else {
			query.WriteString(` AND id < ?`)
		}
		args = append(args, f.AfterID)
	}
	if asc {
		query.WriteString(` ORDER BY id ASC`)
	} else {
		query.WriteString(` ORDER BY id DESC`)
	}
	query.WriteString(` LIMIT ?`)
	args = append(args, limit+1)

	rs, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return domain.PageResult[domain.Snapshot]{}, fmt.Errorf("data: list snapshots: %w", err)
	}
	defer rs.Close()

	items := make([]domain.Snapshot, 0)
	for rs.Next() {
		snap, err := s.scanSnapshot(rs)
		if err != nil {
			return domain.PageResult[domain.Snapshot]{}, fmt.Errorf("data: scan snapshot: %w", err)
		}
		items = append(items, snap)
	}
	if err := rs.Err(); err != nil {
		return domain.PageResult[domain.Snapshot]{}, fmt.Errorf("data: iterate snapshots: %w", err)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID.String()
	}
	ids := make([]domain.ID, len(items))
	for i := range items {
		ids[i] = items[i].ID
	}
	members, err := s.snapshotBatchIDsMany(ctx, s.db, ids)
	if err != nil {
		return domain.PageResult[domain.Snapshot]{}, err
	}
	for i := range items {
		if batchIDs, ok := members[items[i].ID.String()]; ok {
			items[i].BatchIDs = batchIDs
			continue
		}
		items[i].BatchIDs = []domain.ID{}
	}
	return domain.PageResult[domain.Snapshot]{Items: items, NextCursor: next}, nil
}

// OpenView binds an immutable read to one snapshot and one decision time.
// Snapshot existence is checked up front so later query failures never mix
// "unknown snapshot" with "no data".
func (s *Store) OpenView(ctx context.Context, id domain.ID, asOf time.Time) (ports.DataView, error) {
	if _, err := s.GetSnapshot(ctx, id); err != nil {
		return nil, err
	}
	if asOf.IsZero() {
		return nil, domain.NewError(domain.CodeValidationInvalid, "data: as_of is required")
	}
	return &view{store: s, snapshotID: id, asOf: asOf.UTC()}, nil
}
