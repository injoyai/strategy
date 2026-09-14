package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

// M1S S2 screening-run persistence. A run is created by the worker that
// executes its screen.run job and is published exactly once; the summary, the
// frozen rows and the result hash are written in one transaction, so an
// interrupted run leaves an unpublished record — readable as "not ready" —
// instead of a partial result that would look authoritative. Published results
// are immutable: a second publish is a conflict, never an overwrite.

const (
	defaultScreenRunLimit = 50
	maxScreenRunLimit     = 200
	// maxScreenRowLimit bounds one page of frozen result rows. Rows are read
	// per run and pages are walked with a cursor, so the bound matches the
	// contract's page size rather than the dataset query limit.
	defaultScreenRowLimit = 50
	maxScreenRowLimit     = 200
)

const screenRunColumns = `id, job_id, screener_id, source_run_id, config_json, config_hash,
	engine_version, scoring_policy_version, snapshot_hash, summary_json, result_hash,
	columns_json, artifact_ids, published_at, created_at`

func (s *Store) scanScreenRun(row scanner) (screening.RunRecord, error) {
	var (
		record      screening.RunRecord
		configJSON  []byte
		summaryJSON []byte
		columnsJSON []byte
		artifacts   []byte
		publishedAt sql.NullInt64
		createdAt   int64
	)
	if err := row.Scan(&record.ID, &record.JobID, &record.ScreenerID, &record.SourceRunID,
		&configJSON, &record.ConfigHash, &record.EngineVersion, &record.ScoringPolicyVersion,
		&record.SnapshotHash, &summaryJSON, &record.ResultHash, &columnsJSON, &artifacts,
		&publishedAt, &createdAt); err != nil {
		return screening.RunRecord{}, err
	}
	if err := unmarshalRecord(configJSON, &record.Config); err != nil {
		return screening.RunRecord{}, err
	}
	if len(summaryJSON) > 0 {
		var summary screening.Summary
		if err := unmarshalRecord(summaryJSON, &summary); err != nil {
			return screening.RunRecord{}, err
		}
		record.Summary = &summary
	}
	if err := unmarshalRecord(columnsJSON, &record.Columns); err != nil {
		return screening.RunRecord{}, err
	}
	if err := unmarshalRecord(artifacts, &record.ArtifactIDs); err != nil {
		return screening.RunRecord{}, err
	}
	record.CreatedAt = time.Unix(0, createdAt).UTC()
	if publishedAt.Valid {
		published := time.Unix(0, publishedAt.Int64).UTC()
		record.PublishedAt = &published
	}
	return record, nil
}

// unmarshalRecord decodes a stored JSON payload produced by this package.
// Undecodable JSON means our own write path or a migration is broken, so it
// fails as an internal error instead of being swallowed.
func unmarshalRecord(raw []byte, out any) error {
	if len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return domain.Wrap(err, domain.CodeInternalError, "data: decode screening run payload")
	}
	return nil
}

// CreateScreenRun records one run, unpublished. Creation happens when the
// worker starts executing the run's job, so a queued job never exposes a run
// that has not begun, and an interrupted run stays visibly unpublished. The
// configuration is stored verbatim and hashed: the hash identifies the frozen
// inputs, so a published result can always be traced to them.
func (s *Store) CreateScreenRun(ctx context.Context, req screening.RunRequest) (screening.RunRecord, error) {
	if err := req.Validate(); err != nil {
		return screening.RunRecord{}, err
	}
	encoded, err := marshalJSON(req.Config)
	if err != nil {
		return screening.RunRecord{}, err
	}
	configHash := s.sum.Checksum(encoded)
	createdAt := s.clock.Now().UTC()
	id := s.newID("srun")
	if _, err := s.db.ExecContext(ctx, `
		INSERT INTO screen_runs (
			id, workspace, job_id, screener_id, source_run_id, config_json, config_hash,
			engine_version, scoring_policy_version, snapshot_hash, columns_json, artifact_ids, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '[]', '[]', ?)`,
		id, workspaceDefault, req.JobID, req.Config.ScreenerRef.ID, req.Config.SourceRunID,
		encoded, configHash, req.EngineVersion, req.ScoringPolicyVersion, req.SnapshotHash,
		createdAt.UnixNano()); err != nil {
		return screening.RunRecord{}, fmt.Errorf("data: insert screening run: %w", err)
	}
	return screening.RunRecord{
		ID:                   id,
		JobID:                req.JobID,
		ScreenerID:           req.Config.ScreenerRef.ID,
		SourceRunID:          req.Config.SourceRunID,
		Config:               req.Config,
		ConfigHash:           configHash,
		EngineVersion:        req.EngineVersion,
		ScoringPolicyVersion: req.ScoringPolicyVersion,
		SnapshotHash:         req.SnapshotHash,
		Columns:              []domain.Field{},
		ArtifactIDs:          []domain.ID{},
		CreatedAt:            createdAt,
	}, nil
}

// GetScreenRun returns one run by id, published or not.
func (s *Store) GetScreenRun(ctx context.Context, id domain.ID) (screening.RunRecord, error) {
	record, err := s.scanScreenRun(s.db.QueryRowContext(ctx, `
		SELECT `+screenRunColumns+`
		FROM screen_runs
		WHERE workspace = ? AND id = ?`, workspaceDefault, id))
	if errors.Is(err, sql.ErrNoRows) {
		return screening.RunRecord{}, domain.NewError(domain.CodeResourceNotFound, "data: screening run %s not found", id)
	}
	if err != nil {
		return screening.RunRecord{}, fmt.Errorf("data: get screening run: %w", err)
	}
	return record, nil
}

// ListScreenRuns lists runs with the same id-only keyset semantics as the
// other listings. The job state filter joins the run's job, so a caller can
// separate running from succeeded and failed runs; the response itself carries
// no state, the job does.
func (s *Store) ListScreenRuns(ctx context.Context, f ports.ScreenRunFilter) (domain.PageResult[screening.RunRecord], error) {
	asc := f.Sort == sortID
	limit := clampLimit(f.Limit, defaultScreenRunLimit, maxScreenRunLimit)

	query := new(strings.Builder)
	query.WriteString(`
		SELECT ` + prefixed(screenRunColumns, "r") + `
		FROM screen_runs r
		LEFT JOIN jobs j ON j.id = r.job_id
		WHERE r.workspace = ?`)
	args := []any{workspaceDefault}
	if f.ScreenerID != "" {
		query.WriteString(` AND r.screener_id = ?`)
		args = append(args, f.ScreenerID)
	}
	if f.JobState != "" {
		query.WriteString(` AND j.state = ?`)
		args = append(args, f.JobState)
	}
	if f.AfterID != "" {
		if asc {
			query.WriteString(` AND r.id > ?`)
		} else {
			query.WriteString(` AND r.id < ?`)
		}
		args = append(args, f.AfterID)
	}
	if asc {
		query.WriteString(` ORDER BY r.id ASC`)
	} else {
		query.WriteString(` ORDER BY r.id DESC`)
	}
	query.WriteString(` LIMIT ?`)
	args = append(args, limit+1)

	rs, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return domain.PageResult[screening.RunRecord]{}, fmt.Errorf("data: list screening runs: %w", err)
	}
	defer rs.Close()

	items := make([]screening.RunRecord, 0)
	for rs.Next() {
		record, err := s.scanScreenRun(rs)
		if err != nil {
			return domain.PageResult[screening.RunRecord]{}, fmt.Errorf("data: scan screening run: %w", err)
		}
		items = append(items, record)
	}
	if err := rs.Err(); err != nil {
		return domain.PageResult[screening.RunRecord]{}, fmt.Errorf("data: iterate screening runs: %w", err)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		next = items[len(items)-1].ID.String()
	}
	return domain.PageResult[screening.RunRecord]{Items: items, NextCursor: next}, nil
}

// prefixed qualifies a column list with a table alias; the run listing joins
// jobs for the state filter, so unqualified names would be ambiguous.
func prefixed(columns, alias string) string {
	parts := strings.Split(columns, ",")
	for i, part := range parts {
		parts[i] = alias + "." + strings.TrimSpace(part)
	}
	return strings.Join(parts, ", ")
}

// PublishScreenRun writes a run's frozen result exactly once. The stage counts
// are re-checked against the rows they describe and the whole result lands in
// one transaction: either the summary, every row and the result hash are all
// visible, or the run stays unpublished. A second publish is a conflict — a
// published result is evidence and is never overwritten.
func (s *Store) PublishScreenRun(ctx context.Context, runID domain.ID, req screening.RunPublishRequest) error {
	if runID == "" {
		return domain.NewError(domain.CodeValidationInvalid, "data: screening run id is required")
	}
	if req.ResultHash == "" {
		return domain.NewError(domain.CodeValidationInvalid, "data: screening run result hash is required")
	}
	if err := req.Summary.Check(len(req.Rows)); err != nil {
		return err
	}
	seen := make(map[domain.ID]struct{}, len(req.Rows))
	for _, row := range req.Rows {
		if row.InstrumentID == "" {
			return domain.NewError(domain.CodeInternalError, "data: screening run row has no instrument id")
		}
		if _, dup := seen[row.InstrumentID]; dup {
			return domain.NewError(domain.CodeInternalError,
				"data: screening run result lists instrument %s twice", row.InstrumentID)
		}
		seen[row.InstrumentID] = struct{}{}
	}
	summaryJSON, err := marshalJSON(req.Summary)
	if err != nil {
		return err
	}
	columnsJSON, err := marshalJSON(nonNilFields(req.Columns))
	if err != nil {
		return err
	}
	artifactsJSON, err := marshalJSON(nonNilIDs(req.ArtifactIDs))
	if err != nil {
		return err
	}
	publishedAt := s.clock.Now().UTC()

	return s.withTx(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `
			UPDATE screen_runs
			SET summary_json = ?, columns_json = ?, result_hash = ?, artifact_ids = ?, published_at = ?
			WHERE workspace = ? AND id = ? AND published_at IS NULL`,
			summaryJSON, columnsJSON, req.ResultHash, artifactsJSON,
			publishedAt.UnixNano(), workspaceDefault, runID)
		if err != nil {
			return fmt.Errorf("data: publish screening run: %w", err)
		}
		affected, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("data: publish screening run rows: %w", err)
		}
		if affected == 0 {
			// Either the run does not exist or it already published; both are
			// decided outside the write, so the transaction stays a single
			// atomic marker flip.
			return s.publishRefused(ctx, tx, runID)
		}
		stmt, err := tx.PrepareContext(ctx, `
			INSERT INTO screen_result_rows (
				run_id, workspace, ordinal, instrument_id, stage, selected, row_json
			) VALUES (?, ?, ?, ?, ?, ?, ?)`)
		if err != nil {
			return fmt.Errorf("data: prepare result row insert: %w", err)
		}
		defer stmt.Close()
		for ordinal, row := range req.Rows {
			encoded, err := marshalJSON(row)
			if err != nil {
				return err
			}
			if _, err := stmt.ExecContext(ctx, runID, workspaceDefault, ordinal,
				row.InstrumentID, string(row.Stage), boolInt(row.Selected), encoded); err != nil {
				return fmt.Errorf("data: insert result row: %w", err)
			}
		}
		return nil
	})
}

// publishRefused explains why an atomic publish could not claim the run.
func (s *Store) publishRefused(ctx context.Context, tx *sql.Tx, runID domain.ID) error {
	var published sql.NullInt64
	err := tx.QueryRowContext(ctx, `
		SELECT published_at FROM screen_runs WHERE workspace = ? AND id = ?`,
		workspaceDefault, runID).Scan(&published)
	if errors.Is(err, sql.ErrNoRows) {
		return domain.NewError(domain.CodeResourceNotFound, "data: screening run %s not found", runID)
	}
	if err != nil {
		return fmt.Errorf("data: read screening run state: %w", err)
	}
	return domain.NewError(domain.CodeResourceConflict,
		"data: screening run %s already published its result; a published result is immutable", runID)
}

// ListScreenRunRows pages the frozen rows of one published run in the run's
// canonical order (selected and rankable rows by official rank, then excluded
// rows by instrument id). NextCursor is the last returned ordinal, so the next
// page resumes with no re-sorting and no dependence on the caller's page size.
func (s *Store) ListScreenRunRows(ctx context.Context, runID domain.ID, f ports.ScreenRunRowFilter) (domain.PageResult[screening.Row], error) {
	limit := clampLimit(f.Limit, defaultScreenRowLimit, maxScreenRowLimit)

	query := new(strings.Builder)
	query.WriteString(`
		SELECT ordinal, row_json FROM screen_result_rows
		WHERE workspace = ? AND run_id = ?`)
	args := []any{workspaceDefault, runID}
	if f.AfterOrdinal != nil {
		query.WriteString(` AND ordinal > ?`)
		args = append(args, *f.AfterOrdinal)
	}
	if f.Selected != nil {
		query.WriteString(` AND selected = ?`)
		args = append(args, boolInt(*f.Selected))
	}
	query.WriteString(` ORDER BY ordinal ASC LIMIT ?`)
	args = append(args, limit+1)

	rs, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return domain.PageResult[screening.Row]{}, fmt.Errorf("data: list screening rows: %w", err)
	}
	defer rs.Close()

	items := make([]screening.Row, 0)
	ordinals := make([]int64, 0)
	for rs.Next() {
		var (
			ordinal int64
			raw     []byte
		)
		if err := rs.Scan(&ordinal, &raw); err != nil {
			return domain.PageResult[screening.Row]{}, fmt.Errorf("data: scan screening row: %w", err)
		}
		var row screening.Row
		if err := unmarshalRecord(raw, &row); err != nil {
			return domain.PageResult[screening.Row]{}, err
		}
		items = append(items, row)
		ordinals = append(ordinals, ordinal)
	}
	if err := rs.Err(); err != nil {
		return domain.PageResult[screening.Row]{}, fmt.Errorf("data: iterate screening rows: %w", err)
	}

	next := ""
	if len(items) > limit {
		items = items[:limit]
		// The extra row proves another page exists; the resume point is the
		// ordinal of the last row this page kept.
		next = strconv.FormatInt(ordinals[limit-1], 10)
	}
	return domain.PageResult[screening.Row]{Items: items, NextCursor: next}, nil
}

// GetScreenRunRow returns one instrument's frozen row — the evidence behind its
// classification and its score components.
func (s *Store) GetScreenRunRow(ctx context.Context, runID, instrumentID domain.ID) (screening.Row, error) {
	var raw []byte
	err := s.db.QueryRowContext(ctx, `
		SELECT row_json FROM screen_result_rows
		WHERE workspace = ? AND run_id = ? AND instrument_id = ?`,
		workspaceDefault, runID, instrumentID).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return screening.Row{}, domain.NewError(domain.CodeResourceNotFound,
			"data: screening run %s has no result for instrument %s", runID, instrumentID)
	}
	if err != nil {
		return screening.Row{}, fmt.Errorf("data: get screening row: %w", err)
	}
	var row screening.Row
	if err := unmarshalRecord(raw, &row); err != nil {
		return screening.Row{}, err
	}
	return row, nil
}

func nonNilFields(fields []domain.Field) []domain.Field {
	if fields == nil {
		return []domain.Field{}
	}
	return fields
}

func nonNilIDs(ids []domain.ID) []domain.ID {
	if ids == nil {
		return []domain.ID{}
	}
	return ids
}
