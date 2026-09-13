package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strconv"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// view is an immutable point-in-time read bound to one snapshot and one
// as_of decision time. It is handed out by Store.OpenView and never exposes
// mutation; queries apply the PIT filters in SQL (available_at <= as_of and
// the effective window covering as_of) before the latest-revision rule picks
// the surviving row per natural key, and a query-level replay_time further
// narrows visibility to rows ingested at or before that moment.
type view struct {
	store      *Store
	snapshotID domain.ID
	asOf       time.Time
}

var _ ports.DataView = (*view)(nil)

// SnapshotID returns the snapshot the view is pinned to.
func (v *view) SnapshotID() domain.ID { return v.snapshotID }

// AsOf returns the decision time the view is pinned to.
func (v *view) AsOf() time.Time { return v.asOf }

// Query returns one page of point-in-time observations. The SQL scan applies
// the PIT filters — available_at <= as_of, the effective window covering
// as_of (half-open: effective_from <= as_of < effective_to; windowless rows
// always pass), and ingested_at <= replay_time when the query carries one —
// then orders rows so the LAST row per (instrument, entity, event_time) is
// the latest revision. Rows stream into a winner map, then the ordered
// result is windowed by the decimal offset cursor — safe here because a
// pinned snapshot and as_of are immutable, so the winner set never changes
// under a cursor (unlike list APIs, whose cursors live in the server layer).
func (v *view) Query(ctx context.Context, q domain.DataQuery) (domain.PageResult[domain.Observation], error) {
	if err := q.Validate(); err != nil {
		return domain.PageResult[domain.Observation]{}, err
	}
	if q.SnapshotID != v.snapshotID {
		return domain.PageResult[domain.Observation]{}, domain.NewError(domain.CodeValidationInvalid, "data query: snapshot_id %q does not match this view (%q)", q.SnapshotID, v.snapshotID)
	}
	offset := 0
	if q.Cursor != "" {
		parsed, err := strconv.Atoi(q.Cursor)
		if err != nil || parsed < 0 {
			return domain.PageResult[domain.Observation]{}, domain.NewError(domain.CodeValidationInvalid, "data query: invalid cursor")
		}
		offset = parsed
	}
	limit := clampLimit(q.Limit, defaultQueryLimit, maxQueryLimit)

	query := `
		SELECT instrument_id, entity_id, event_time, period_end,
		       effective_from, effective_to, values_json, provenance_json
		FROM observations
		WHERE batch_id IN (SELECT batch_id FROM snapshot_batches WHERE snapshot_id = ? AND workspace = ?)
		  AND workspace = ? AND dataset = ? AND frequency = ?
		  AND instrument_id IN (` + placeholders(len(q.InstrumentIDs)) + `)
		  AND event_time >= ? AND event_time < ?
		  AND available_at <= ?
		  AND (effective_from IS NULL OR (effective_from <= ? AND (effective_to IS NULL OR ? < effective_to)))`
	if q.ReplayTime != nil {
		query += `
		  AND ingested_at <= ?`
	}
	query += `
		ORDER BY instrument_id ASC, event_time ASC, available_at ASC, revision_id ASC, id ASC`
	args := make([]any, 0, len(q.InstrumentIDs)+11)
	args = append(args, q.SnapshotID, workspaceDefault, workspaceDefault, q.Dataset, q.Frequency)
	for _, id := range q.InstrumentIDs {
		args = append(args, id.String())
	}
	args = append(args, q.Range.From.UTC().UnixNano(), q.Range.To.UTC().UnixNano(), v.asOf.UnixNano())
	args = append(args, v.asOf.UnixNano(), v.asOf.UnixNano())
	if q.ReplayTime != nil {
		args = append(args, q.ReplayTime.UTC().UnixNano())
	}

	rs, err := v.store.db.QueryContext(ctx, query, args...)
	if err != nil {
		return domain.PageResult[domain.Observation]{}, fmt.Errorf("data: query observations: %w", err)
	}
	defer rs.Close()

	// order preserves first-seen key order; winners holds the last row per
	// key per the SQL order — that row is the latest revision. Rows of one
	// key cluster together in the scan, so unconditional overwrite lands on
	// the winner without buffering every superseded row in order.
	order := make([]string, 0)
	winners := make(map[string]domain.Observation)
	for rs.Next() {
		rec, key, err := scanObservation(rs, q.Dataset, q.Fields)
		if err != nil {
			return domain.PageResult[domain.Observation]{}, err
		}
		if _, seen := winners[key]; !seen {
			order = append(order, key)
		}
		winners[key] = rec
	}
	if err := rs.Err(); err != nil {
		return domain.PageResult[domain.Observation]{}, fmt.Errorf("data: iterate observations: %w", err)
	}

	start := offset
	if start > len(order) {
		start = len(order)
	}
	end := offset + limit
	if end > len(order) {
		end = len(order)
	}
	items := make([]domain.Observation, 0, end-start)
	for _, key := range order[start:end] {
		items = append(items, winners[key])
	}
	next := ""
	if end < len(order) {
		next = strconv.Itoa(end)
	}
	return domain.PageResult[domain.Observation]{Items: items, NextCursor: next}, nil
}

// scanObservation decodes one observations row into the domain record and
// returns its natural key (instrument + entity + event time) used to
// resolve the latest revision.
func scanObservation(row scanner, dataset string, fields []string) (domain.Observation, string, error) {
	var (
		rec            domain.Observation
		instrumentID   string
		entityID       string
		eventTime      int64
		periodEnd      string
		effectiveFrom  sql.NullInt64
		effectiveTo    sql.NullInt64
		valuesJSON     []byte
		provenanceJSON []byte
	)
	if err := row.Scan(&instrumentID, &entityID, &eventTime, &periodEnd, &effectiveFrom, &effectiveTo, &valuesJSON, &provenanceJSON); err != nil {
		return domain.Observation{}, "", fmt.Errorf("data: scan observation: %w", err)
	}
	rec.Dataset = dataset
	rec.EventTime = time.Unix(0, eventTime).UTC()
	if instrumentID != "" {
		instrument := domain.ID(instrumentID)
		rec.InstrumentID = &instrument
	}
	if entityID != "" {
		entity := domain.ID(entityID)
		rec.EntityID = &entity
	}
	if periodEnd != "" {
		period := periodEnd
		rec.PeriodEnd = &period
	}
	if effectiveFrom.Valid && effectiveTo.Valid {
		effective := domain.Interval{
			From: time.Unix(0, effectiveFrom.Int64).UTC(),
			To:   time.Unix(0, effectiveTo.Int64).UTC(),
		}
		rec.Effective = &effective
	}
	var values map[string]domain.Value
	if err := json.Unmarshal(valuesJSON, &values); err != nil {
		return domain.Observation{}, "", domain.Wrap(err, domain.CodeInternalError, "data: decode values")
	}
	rec.Values = projectValues(values, fields)
	if err := json.Unmarshal(provenanceJSON, &rec.Provenance); err != nil {
		return domain.Observation{}, "", domain.Wrap(err, domain.CodeInternalError, "data: decode provenance")
	}
	key := instrumentID + "\x00" + entityID + "\x00" + strconv.FormatInt(eventTime, 10)
	return rec, key, nil
}

// projectValues narrows the full stored value map to the requested fields;
// absent fields are simply omitted. The returned map is never nil so the
// wire never renders null.
func projectValues(values map[string]domain.Value, fields []string) map[string]domain.Value {
	out := make(map[string]domain.Value, len(fields))
	for _, field := range fields {
		if value, ok := values[field]; ok {
			out[field] = value
		}
	}
	return out
}
