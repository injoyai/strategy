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

// M0 dataset catalog: what a dataset declares, what its rows actually contain
// and which quality findings were recorded against it. Everything here is
// derived from persisted state — the declared schema from the batch
// declaration captured at ingestion, the field types from the stored values
// (a Value carries its kind), the coverage from the observation timestamps and
// the findings from the batches' issue lists. Nothing is inferred from a
// naming convention or filled with a default.

const (
	defaultDatasetLimit = 50
	maxDatasetLimit     = 200
)

// datasetNaturalKey is the identity the normalized store keys rows on, as
// implemented by the observations table. It is reported as the dataset's
// natural key because that is exactly what uniqueness is enforced on; a
// dataset that needs a different identity would need a different store.
var datasetNaturalKey = []string{"instrument_id", "entity_id", "event_time"}

func (s *Store) GetDataset(ctx context.Context, id domain.ID) (domain.Dataset, error) {
	if id == "" {
		return domain.Dataset{}, domain.NewError(domain.CodeValidationInvalid, "data: dataset id is required")
	}
	dataset, err := s.buildDataset(ctx, id.String())
	if err != nil {
		return domain.Dataset{}, err
	}
	if dataset.ID == "" {
		return domain.Dataset{}, domain.NewError(domain.CodeResourceNotFound, "data: dataset %s not found", id)
	}
	return dataset, nil
}

// ListDatasets lists the dataset catalog keyed by dataset name — the only
// stable dataset identity the store has, because that is what both batches and
// observations carry.
func (s *Store) ListDatasets(ctx context.Context, f ports.DatasetFilter) (domain.PageResult[domain.Dataset], error) {
	asc := f.Sort == sortID
	limit := clampLimit(f.Limit, defaultDatasetLimit, maxDatasetLimit)

	query := new(strings.Builder)
	query.WriteString(`
		SELECT dataset_id FROM batches
		WHERE workspace = ? AND dataset_id <> ''`)
	args := []any{workspaceDefault}
	if f.Q != "" {
		query.WriteString(` AND dataset_id LIKE ? ESCAPE '\'`)
		args = append(args, "%"+escapeLike(f.Q)+"%")
	}
	if f.AfterID != "" {
		if asc {
			query.WriteString(` AND dataset_id > ?`)
		} else {
			query.WriteString(` AND dataset_id < ?`)
		}
		args = append(args, f.AfterID)
	}
	if asc {
		query.WriteString(` GROUP BY dataset_id ORDER BY dataset_id ASC`)
	} else {
		query.WriteString(` GROUP BY dataset_id ORDER BY dataset_id DESC`)
	}
	query.WriteString(` LIMIT ?`)
	args = append(args, limit+1)

	rs, err := s.db.QueryContext(ctx, query.String(), args...)
	if err != nil {
		return domain.PageResult[domain.Dataset]{}, fmt.Errorf("data: list datasets: %w", err)
	}
	names := make([]string, 0, limit+1)
	for rs.Next() {
		var name string
		if err := rs.Scan(&name); err != nil {
			rs.Close()
			return domain.PageResult[domain.Dataset]{}, fmt.Errorf("data: scan dataset: %w", err)
		}
		names = append(names, name)
	}
	rs.Close()
	if err := rs.Err(); err != nil {
		return domain.PageResult[domain.Dataset]{}, fmt.Errorf("data: iterate datasets: %w", err)
	}

	next := ""
	if len(names) > limit {
		names = names[:limit]
		next = names[len(names)-1]
	}
	items := make([]domain.Dataset, 0, len(names))
	for _, name := range names {
		dataset, err := s.buildDataset(ctx, name)
		if err != nil {
			return domain.PageResult[domain.Dataset]{}, err
		}
		items = append(items, dataset)
	}
	return domain.PageResult[domain.Dataset]{Items: items, NextCursor: next}, nil
}

// buildDataset assembles one catalog row. An unknown dataset comes back with an
// empty ID rather than an error so the caller can tell "not found" from a real
// failure.
func (s *Store) buildDataset(ctx context.Context, name string) (domain.Dataset, error) {
	var batches int
	if err := s.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM batches WHERE workspace = ? AND dataset_id = ?`,
		workspaceDefault, name).Scan(&batches); err != nil {
		return domain.Dataset{}, fmt.Errorf("data: count dataset batches: %w", err)
	}
	if batches == 0 {
		return domain.Dataset{}, nil
	}

	output := domain.Dataset{
		ID:            domain.ID(name),
		Name:          name,
		NaturalKey:    append([]string(nil), datasetNaturalKey...),
		Fields:        []domain.Field{},
		Frequencies:   []string{},
		QualityIssues: []domain.Issue{},
	}

	frequencies, err := s.datasetFrequencies(ctx, name)
	if err != nil {
		return domain.Dataset{}, err
	}
	output.Frequencies = frequencies

	declaration, err := s.datasetDeclaration(ctx, name)
	if err != nil {
		return domain.Dataset{}, err
	}
	if declaration != nil {
		output.AvailabilityPolicyRef = declaration.AvailabilityPolicyRef
	}

	kinds, present, nullable, err := s.datasetFieldShapes(ctx, name)
	if err != nil {
		return domain.Dataset{}, err
	}
	schemaVersion, err := s.datasetSchemaVersion(ctx, name)
	if err != nil {
		return domain.Dataset{}, err
	}
	output.SchemaVersion = schemaVersion
	output.Fields = mergeDatasetFields(declaration, kinds, present, nullable)
	coverage, err := s.datasetCoverage(ctx, name)
	if err != nil {
		return domain.Dataset{}, err
	}
	output.Coverage = coverage
	issues, err := s.datasetIssues(ctx, name)
	if err != nil {
		return domain.Dataset{}, err
	}
	output.QualityIssues = issues
	return output, nil
}

// datasetFrequencies lists the frequencies the dataset's rows were ingested at.
// Append writes one frequency on the batch and the same one on every row it
// writes, so the batch-level list is the dataset's frequency list, and a reader
// asking for a frequency outside it will find no rows.
func (s *Store) datasetFrequencies(ctx context.Context, name string) ([]string, error) {
	rs, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT frequency FROM batches
		WHERE workspace = ? AND dataset_id = ? AND frequency <> ''
		ORDER BY frequency ASC`, workspaceDefault, name)
	if err != nil {
		return nil, fmt.Errorf("data: read dataset frequencies: %w", err)
	}
	defer rs.Close()
	frequencies := []string{}
	for rs.Next() {
		var frequency string
		if err := rs.Scan(&frequency); err != nil {
			return nil, fmt.Errorf("data: scan dataset frequency: %w", err)
		}
		frequencies = append(frequencies, frequency)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("data: iterate dataset frequencies: %w", err)
	}
	return frequencies, nil
}

// datasetDeclaration returns the newest declaration for a dataset. Later
// declarations win because a mapping change is a schema change, and the catalog
// reports the schema currently in force.
func (s *Store) datasetDeclaration(ctx context.Context, name string) (*domain.DatasetDeclaration, error) {
	var raw string
	err := s.db.QueryRowContext(ctx, `
		SELECT declaration_json FROM batches
		WHERE workspace = ? AND dataset_id = ? AND declaration_json <> ''
		ORDER BY created_at DESC, id DESC
		LIMIT 1`, workspaceDefault, name).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("data: read dataset declaration: %w", err)
	}
	var declaration domain.DatasetDeclaration
	if err := json.Unmarshal([]byte(raw), &declaration); err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "data: decode dataset declaration")
	}
	return &declaration, nil
}

// datasetFieldShapes reads the distinct stored value maps of one dataset and
// returns each field's kind plus whether any row carried it as missing. Value
// blobs repeat heavily (one per schema shape), so the DISTINCT scan stays small
// while covering every shape ever written. Presence is tracked separately from
// kind: a field stored only as missing is still a field, but its type is not
// known.
func (s *Store) datasetFieldShapes(ctx context.Context, name string) (map[string]domain.ValueKind, map[string]bool, map[string]bool, error) {
	rs, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT values_json FROM observations
		WHERE workspace = ? AND dataset = ?
		ORDER BY values_json ASC`, workspaceDefault, name)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("data: read dataset values: %w", err)
	}
	defer rs.Close()
	kinds := map[string]domain.ValueKind{}
	present := map[string]bool{}
	nullable := map[string]bool{}
	for rs.Next() {
		var raw []byte
		if err := rs.Scan(&raw); err != nil {
			return nil, nil, nil, fmt.Errorf("data: scan dataset values: %w", err)
		}
		var values map[string]domain.Value
		if err := json.Unmarshal(raw, &values); err != nil {
			return nil, nil, nil, domain.Wrap(err, domain.CodeInternalError, "data: decode observation values")
		}
		for field, value := range values {
			present[field] = true
			if value.Kind != "" {
				kinds[field] = value.Kind
			}
			if value.MissingReason != "" {
				nullable[field] = true
			}
		}
	}
	if err := rs.Err(); err != nil {
		return nil, nil, nil, fmt.Errorf("data: iterate dataset values: %w", err)
	}
	return kinds, present, nullable, nil
}

// datasetSchemaVersion reports the schema version the rows carry. Provenance is
// the only place a dataset's schema version is recorded, and a dataset that
// mixes versions reports the newest one (deterministically, by its text order).
func (s *Store) datasetSchemaVersion(ctx context.Context, name string) (string, error) {
	rs, err := s.db.QueryContext(ctx, `
		SELECT DISTINCT provenance_json FROM observations
		WHERE workspace = ? AND dataset = ?
		ORDER BY provenance_json ASC`, workspaceDefault, name)
	if err != nil {
		return "", fmt.Errorf("data: read dataset provenance: %w", err)
	}
	defer rs.Close()
	version := ""
	for rs.Next() {
		var raw []byte
		if err := rs.Scan(&raw); err != nil {
			return "", fmt.Errorf("data: scan dataset provenance: %w", err)
		}
		var provenance domain.Provenance
		if err := json.Unmarshal(raw, &provenance); err != nil {
			return "", domain.Wrap(err, domain.CodeInternalError, "data: decode observation provenance")
		}
		if provenance.SchemaVersion > version {
			version = provenance.SchemaVersion
		}
	}
	if err := rs.Err(); err != nil {
		return "", fmt.Errorf("data: iterate dataset provenance: %w", err)
	}
	return version, nil
}

func (s *Store) datasetCoverage(ctx context.Context, name string) (*domain.Interval, error) {
	var (
		first sql.NullInt64
		last  sql.NullInt64
	)
	if err := s.db.QueryRowContext(ctx, `
		SELECT MIN(event_time), MAX(event_time) FROM observations
		WHERE workspace = ? AND dataset = ?`, workspaceDefault, name).Scan(&first, &last); err != nil {
		return nil, fmt.Errorf("data: read dataset coverage: %w", err)
	}
	if !first.Valid || !last.Valid {
		return nil, nil
	}
	from := time.Unix(0, first.Int64).UTC()
	to := time.Unix(0, last.Int64).UTC()
	// The catalog reports an inclusive span (first..last event), while Interval
	// is half-open, so the far end is the next nanosecond after the last event.
	coverage := domain.Interval{From: from, To: to.Add(time.Nanosecond)}
	return &coverage, nil
}

// datasetIssues concatenates the quality findings recorded with the dataset's
// batches, in batch order, so the catalog never hides a finding a batch kept.
func (s *Store) datasetIssues(ctx context.Context, name string) ([]domain.Issue, error) {
	rs, err := s.db.QueryContext(ctx, `
		SELECT issues FROM batches
		WHERE workspace = ? AND dataset_id = ?
		ORDER BY created_at ASC, id ASC`, workspaceDefault, name)
	if err != nil {
		return nil, fmt.Errorf("data: read dataset issues: %w", err)
	}
	defer rs.Close()
	issues := []domain.Issue{}
	for rs.Next() {
		var raw []byte
		if err := rs.Scan(&raw); err != nil {
			return nil, fmt.Errorf("data: scan dataset issues: %w", err)
		}
		batch, err := unmarshalIssues(raw)
		if err != nil {
			return nil, err
		}
		issues = append(issues, batch...)
	}
	if err := rs.Err(); err != nil {
		return nil, fmt.Errorf("data: iterate dataset issues: %w", err)
	}
	return issues, nil
}

// mergeDatasetFields lists declared fields first (in declaration order, so the
// operator's schema reads back as written) and then any field that was observed
// but never declared. A declared field keeps its unit; an undeclared one is
// reported with an empty unit, which is how "no unit is known" is spelled —
// never a substituted default.
func mergeDatasetFields(declaration *domain.DatasetDeclaration, kinds map[string]domain.ValueKind, present, nullable map[string]bool) []domain.Field {
	fields := make([]domain.Field, 0, len(present))
	declared := map[string]bool{}
	if declaration != nil {
		for _, field := range declaration.Fields {
			declared[field.Name] = true
			fields = append(fields, domain.Field{
				Name:     field.Name,
				Type:     fieldType(kinds[field.Name]),
				Unit:     field.Unit,
				Nullable: nullable[field.Name],
			})
		}
	}
	undeclared := make([]string, 0, len(present))
	for name := range present {
		if !declared[name] {
			undeclared = append(undeclared, name)
		}
	}
	sort.Strings(undeclared)
	for _, name := range undeclared {
		fields = append(fields, domain.Field{
			Name:     name,
			Type:     fieldType(kinds[name]),
			Nullable: nullable[name],
		})
	}
	return fields
}

// fieldType maps an observed value kind to the catalog's field type; a field
// with no observed values reports "unknown" rather than an assumed type.
func fieldType(kind domain.ValueKind) domain.FieldType {
	if kind == "" {
		return domain.FieldUnknown
	}
	return domain.FieldType(kind)
}
