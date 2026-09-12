package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/store"
)

var testBase = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestStore(t *testing.T) (*Store, *ports.FixedClock) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(context.Background(), db, silentLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	clk := ports.NewFixedClock(testBase)
	return New(db, clk), clk
}

func mustTime(value string) time.Time {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(fmt.Sprintf("parse time %q: %v", value, err))
	}
	return parsed
}

func timePtr(value time.Time) *time.Time { return &value }

func idPtr(value string) *domain.ID {
	id := domain.ID(value)
	return &id
}

func barRowAt(inst, date, close, revision string, availableAt time.Time, published *time.Time) domain.Observation {
	return domain.Observation{
		InstrumentID: idPtr(inst),
		Dataset:      "bar",
		EventTime:    mustTime(date + "T15:00:00Z"),
		Values: map[string]domain.Value{
			"open":   {Kind: domain.ValueDecimal, Encoded: "10.00"},
			"high":   {Kind: domain.ValueDecimal, Encoded: "10.50"},
			"low":    {Kind: domain.ValueDecimal, Encoded: "9.90"},
			"close":  {Kind: domain.ValueDecimal, Encoded: close},
			"volume": {Kind: domain.ValueDecimal, Encoded: "1000"},
			"unit":   {Kind: domain.ValueString, Encoded: "CNY"},
		},
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "bar-" + inst + "-" + date,
			RevisionID:     revision,
			AvailableAt:    availableAt,
			IngestedAt:     availableAt,
			PublishedAt:    published,
		},
	}
}

func barRow(inst, date, close string, published *time.Time) domain.Observation {
	return barRowAt(inst, date, close, "rev-001", mustTime(date+"T15:30:00Z"), published)
}

func appendBatch(t *testing.T, s *Store, job domain.ID, issues []domain.Issue, obs ...domain.Observation) domain.IngestReceipt {
	t.Helper()
	receipt, err := s.Append(context.Background(), ports.BatchInput{
		JobID:        job,
		Dataset:      "bar",
		Frequency:    "daily",
		Observations: obs,
		Issues:       issues,
	})
	if err != nil {
		t.Fatalf("append batch: %v", err)
	}
	return receipt
}

func assertErr(t *testing.T, err error, code, contains string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != code {
		t.Fatalf("code = %q, want %q (err: %v)", domErr.Code, code, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err.Error(), contains)
	}
}

func TestAppendValidation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	t.Run("job required", func(t *testing.T) {
		_, err := s.Append(ctx, ports.BatchInput{Dataset: "bar", Frequency: "daily"})
		assertErr(t, err, domain.CodeValidationInvalid, "data: batch job_id is required")
	})
	t.Run("dataset required", func(t *testing.T) {
		_, err := s.Append(ctx, ports.BatchInput{JobID: "job-1", Frequency: "daily"})
		assertErr(t, err, domain.CodeValidationInvalid, "data: batch dataset is required")
	})
	t.Run("frequency required", func(t *testing.T) {
		_, err := s.Append(ctx, ports.BatchInput{JobID: "job-1", Dataset: "bar"})
		assertErr(t, err, domain.CodeValidationInvalid, "data: batch frequency is required")
	})
	t.Run("zero event time", func(t *testing.T) {
		obs := barRow("INST_A", "2026-01-05", "10.40", nil)
		obs.EventTime = time.Time{}
		_, err := s.Append(ctx, ports.BatchInput{JobID: "job-1", Dataset: "bar", Frequency: "daily", Observations: []domain.Observation{obs}})
		assertErr(t, err, domain.CodeValidationInvalid, "data: observations[0] has zero event_time")
	})
	t.Run("zero available at", func(t *testing.T) {
		obs := barRow("INST_A", "2026-01-05", "10.40", nil)
		obs.Provenance.AvailableAt = time.Time{}
		_, err := s.Append(ctx, ports.BatchInput{JobID: "job-1", Dataset: "bar", Frequency: "daily", Observations: []domain.Observation{obs}})
		assertErr(t, err, domain.CodeValidationInvalid, "data: observations[0] has zero available_at")
	})
	t.Run("dataset mismatch", func(t *testing.T) {
		obs := barRow("INST_A", "2026-01-05", "10.40", nil)
		obs.Dataset = "instrument"
		_, err := s.Append(ctx, ports.BatchInput{JobID: "job-1", Dataset: "bar", Frequency: "daily", Observations: []domain.Observation{obs}})
		assertErr(t, err, domain.CodeValidationInvalid, `data: observations[0] dataset "instrument" does not match batch dataset "bar"`)
	})
	t.Run("effective invalid", func(t *testing.T) {
		obs := barRow("INST_A", "2026-01-05", "10.40", nil)
		obs.Effective = &domain.Interval{From: mustTime("2026-01-05T15:00:00Z"), To: mustTime("2026-01-05T14:00:00Z")}
		_, err := s.Append(ctx, ports.BatchInput{JobID: "job-1", Dataset: "bar", Frequency: "daily", Observations: []domain.Observation{obs}})
		assertErr(t, err, domain.CodeValidationInterval, "data: observations[0] effective invalid")
	})
}

func TestAppendDedupesIdenticalRows(t *testing.T) {
	s, _ := newTestStore(t)
	row := barRow("INST_A", "2026-01-05", "10.40", nil)
	receipt := appendBatch(t, s, "job-1", nil, row, row)
	if receipt.Rows != 1 {
		t.Fatalf("rows = %d, want 1", receipt.Rows)
	}
	if receipt.Issues == nil || len(receipt.Issues) != 0 {
		t.Fatalf("issues = %#v, want empty non-nil slice", receipt.Issues)
	}
	if !strings.HasPrefix(receipt.BatchID.String(), "batch_") {
		t.Fatalf("batch id %q missing batch_ prefix", receipt.BatchID)
	}
	batch, err := s.GetBatch(context.Background(), receipt.BatchID)
	if err != nil {
		t.Fatalf("get batch: %v", err)
	}
	if batch.RowCount != 1 {
		t.Fatalf("stored row count = %d, want 1", batch.RowCount)
	}
	if batch.DatasetID != "bar" {
		t.Fatalf("dataset = %q, want bar", batch.DatasetID)
	}
	if !batch.Ready {
		t.Fatal("fresh batch must be ready")
	}
}

func TestAppendChecksumOrderIndependent(t *testing.T) {
	s, _ := newTestStore(t)
	rowA := barRow("INST_A", "2026-01-05", "10.40", nil)
	rowB := barRow("INST_B", "2026-01-05", "20.40", nil)
	first := appendBatch(t, s, "job-1", nil, rowA, rowB)
	second := appendBatch(t, s, "job-1", nil, rowB, rowA)
	ctx := context.Background()
	b1, err := s.GetBatch(ctx, first.BatchID)
	if err != nil {
		t.Fatalf("get batch 1: %v", err)
	}
	b2, err := s.GetBatch(ctx, second.BatchID)
	if err != nil {
		t.Fatalf("get batch 2: %v", err)
	}
	if b1.Checksum != b2.Checksum {
		t.Fatalf("checksums differ: %s vs %s", b1.Checksum, b2.Checksum)
	}
	if b1.RowCount != 2 || b2.RowCount != 2 {
		t.Fatalf("row counts = %d / %d, want 2 / 2", b1.RowCount, b2.RowCount)
	}
}

func TestAppendStoresRawManifestAndQualityStatus(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()

	withManifest, err := s.Append(ctx, ports.BatchInput{
		JobID: "job-1", Dataset: "bar", Frequency: "daily",
		Observations: []domain.Observation{barRow("INST_A", "2026-01-05", "10.40", nil)},
		Issues: []domain.Issue{
			{Code: "quality.missing_trading_day", Path: "instrument=INST_A/day=2026-01-08", Message: "gap", Severity: domain.SeverityWarning},
		},
		RawManifest: json.RawMessage(`{"page":1}`),
	})
	if err != nil {
		t.Fatalf("append with manifest: %v", err)
	}
	clean := appendBatch(t, s, "job-1", nil, barRow("INST_B", "2026-01-05", "20.40", nil))
	errored, err := s.Append(ctx, ports.BatchInput{
		JobID: "job-1", Dataset: "bar", Frequency: "daily",
		Observations: []domain.Observation{barRow("INST_C", "2026-01-05", "30.40", nil)},
		Issues: []domain.Issue{
			{Code: "quality.ohlc_violation", Path: "row[0]", Message: "bad", Severity: domain.SeverityError},
		},
	})
	if err != nil {
		t.Fatalf("append with error issue: %v", err)
	}

	var manifest sql.NullString
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT raw_manifest, quality_status FROM batches WHERE id = ?`, withManifest.BatchID).Scan(&manifest, &status); err != nil {
		t.Fatalf("scan batch 1: %v", err)
	}
	if !manifest.Valid || manifest.String != `{"page":1}` {
		t.Fatalf("raw manifest = %#v, want verbatim {\"page\":1}", manifest)
	}
	if status != domain.SeverityWarning {
		t.Fatalf("quality status = %q, want %q", status, domain.SeverityWarning)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT raw_manifest, quality_status FROM batches WHERE id = ?`, clean.BatchID).Scan(&manifest, &status); err != nil {
		t.Fatalf("scan batch 2: %v", err)
	}
	if manifest.Valid {
		t.Fatalf("raw manifest = %q, want NULL", manifest.String)
	}
	if status != "clean" {
		t.Fatalf("quality status = %q, want clean", status)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT quality_status FROM batches WHERE id = ?`, errored.BatchID).Scan(&status); err != nil {
		t.Fatalf("scan batch 3: %v", err)
	}
	if status != domain.SeverityError {
		t.Fatalf("quality status = %q, want %q", status, domain.SeverityError)
	}
}

func TestGetBatchNotFound(t *testing.T) {
	s, _ := newTestStore(t)
	_, err := s.GetBatch(context.Background(), "batch_missing")
	assertErr(t, err, domain.CodeResourceNotFound, "data: batch batch_missing not found")
}

func TestListBatchesKeysetAndFilters(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	b1 := appendBatch(t, s, "job-1", nil, barRow("INST_A", "2026-01-05", "10.40", nil))
	b2 := appendBatch(t, s, "job-2", nil, barRow("INST_A", "2026-01-06", "10.50", nil))
	b3 := appendBatch(t, s, "job-2", nil, barRow("INST_A", "2026-01-07", "10.60", nil))

	page1, err := s.ListBatches(ctx, ports.BatchFilter{Sort: "id", Limit: 2})
	if err != nil {
		t.Fatalf("list page 1: %v", err)
	}
	if len(page1.Items) != 2 {
		t.Fatalf("page 1 items = %d, want 2", len(page1.Items))
	}
	if page1.Items[0].ID.String() >= page1.Items[1].ID.String() {
		t.Fatalf("page 1 not ascending: %s >= %s", page1.Items[0].ID, page1.Items[1].ID)
	}
	if page1.NextCursor != page1.Items[1].ID.String() {
		t.Fatalf("next cursor = %q, want %q", page1.NextCursor, page1.Items[1].ID)
	}

	page2, err := s.ListBatches(ctx, ports.BatchFilter{Sort: "id", Limit: 2, AfterID: page1.NextCursor})
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(page2.Items) != 1 {
		t.Fatalf("page 2 items = %d, want 1", len(page2.Items))
	}
	if page2.NextCursor != "" {
		t.Fatalf("page 2 next cursor = %q, want empty", page2.NextCursor)
	}
	seen := map[domain.ID]bool{b1.BatchID: true, b2.BatchID: true, b3.BatchID: true}
	for _, item := range page1.Items {
		if !seen[item.ID] {
			t.Fatalf("unexpected batch %s in page 1", item.ID)
		}
		delete(seen, item.ID)
	}
	for _, item := range page2.Items {
		if !seen[item.ID] {
			t.Fatalf("unexpected batch %s in page 2", item.ID)
		}
		delete(seen, item.ID)
	}
	if len(seen) != 0 {
		t.Fatalf("batches missing from pages: %v", seen)
	}

	byJob, err := s.ListBatches(ctx, ports.BatchFilter{JobID: "job-1"})
	if err != nil {
		t.Fatalf("list by job: %v", err)
	}
	if len(byJob.Items) != 1 || byJob.Items[0].ID != b1.BatchID {
		t.Fatalf("job filter = %#v, want only %s", byJob.Items, b1.BatchID)
	}
	byDataset, err := s.ListBatches(ctx, ports.BatchFilter{Dataset: "instrument"})
	if err != nil {
		t.Fatalf("list by dataset: %v", err)
	}
	if len(byDataset.Items) != 0 {
		t.Fatalf("dataset filter items = %d, want 0", len(byDataset.Items))
	}
}

func TestPublishSnapshotHappy(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	batch := appendBatch(t, s, "job-1", nil, barRow("INST_A", "2026-01-05", "10.40", nil))
	snap, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-clean", BatchIDs: []domain.ID{batch.BatchID}})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if !strings.HasPrefix(snap.ID.String(), "snap_") {
		t.Fatalf("snapshot id %q missing snap_ prefix", snap.ID)
	}
	if snap.Name != "snap-clean" {
		t.Fatalf("name = %q, want snap-clean", snap.Name)
	}
	if !snap.CreatedAt.Equal(testBase) {
		t.Fatalf("created at = %v, want %v", snap.CreatedAt, testBase)
	}
	if len(snap.BatchIDs) != 1 || snap.BatchIDs[0] != batch.BatchID {
		t.Fatalf("batch ids = %v, want [%s]", snap.BatchIDs, batch.BatchID)
	}
	if snap.StrictPIT {
		t.Fatal("strict pit should default to false")
	}
	if len(snap.ManifestHash) != 64 {
		t.Fatalf("manifest hash = %q, want 64 hex chars", snap.ManifestHash)
	}
	if len(snap.QualityIssues) != 0 {
		t.Fatalf("quality issues = %v, want empty", snap.QualityIssues)
	}
	got, err := s.GetSnapshot(ctx, snap.ID)
	if err != nil {
		t.Fatalf("get snapshot: %v", err)
	}
	if got.Name != snap.Name || got.ManifestHash != snap.ManifestHash || !got.CreatedAt.Equal(snap.CreatedAt) {
		t.Fatalf("get snapshot mismatch: %#v vs %#v", got, snap)
	}
	listed, err := s.ListSnapshots(ctx, ports.SnapshotFilter{})
	if err != nil {
		t.Fatalf("list snapshots: %v", err)
	}
	if len(listed.Items) != 1 || listed.Items[0].ID != snap.ID {
		t.Fatalf("listed snapshots = %#v, want [%s]", listed.Items, snap.ID)
	}
}

func TestPublishSnapshotFailClosed(t *testing.T) {
	s, _ := newTestStore(t)
	batch := appendBatch(t, s, "job-1", []domain.Issue{
		{Code: "quality.ohlc_violation", Path: "row[0]", Message: "bad", Severity: domain.SeverityError},
	}, barRow("INST_A", "2026-01-05", "10.40", nil))
	_, err := s.PublishSnapshot(context.Background(), domain.SnapshotRequest{Name: "snap-bad", BatchIDs: []domain.ID{batch.BatchID}})
	assertErr(t, err, domain.CodeResourceConflict, `snapshot "snap-bad" blocked by 1 error-severity quality issue(s); publishing is fail-closed`)
	listed, listErr := s.ListSnapshots(context.Background(), ports.SnapshotFilter{})
	if listErr != nil {
		t.Fatalf("list snapshots: %v", listErr)
	}
	if len(listed.Items) != 0 {
		t.Fatalf("blocked publish must not leave snapshots, got %d", len(listed.Items))
	}
}

func TestPublishSnapshotStrictPIT(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	unverified := appendBatch(t, s, "job-1", nil,
		barRow("INST_A", "2026-01-05", "10.40", nil),
		barRow("INST_A", "2026-01-06", "10.50", nil),
	)
	_, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-strict", BatchIDs: []domain.ID{unverified.BatchID}, StrictPIT: true})
	assertErr(t, err, domain.CodeResourceConflict, "blocked by 2 observation(s) without a published time; strict PIT requires verified availability")

	verified := appendBatch(t, s, "job-2", nil,
		barRow("INST_A", "2026-01-05", "10.40", timePtr(mustTime("2026-01-05T15:35:00Z"))),
		barRow("INST_A", "2026-01-06", "10.50", timePtr(mustTime("2026-01-06T15:35:00Z"))),
	)
	snap, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-strict", BatchIDs: []domain.ID{verified.BatchID}, StrictPIT: true})
	if err != nil {
		t.Fatalf("publish verified: %v", err)
	}
	if !snap.StrictPIT {
		t.Fatal("snapshot must keep strict_pit")
	}
}

func TestPublishSnapshotValidation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	_, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{BatchIDs: []domain.ID{"batch_x"}})
	assertErr(t, err, domain.CodeValidationInvalid, "snapshot: name is required")
	_, err = s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-x"})
	assertErr(t, err, domain.CodeValidationInvalid, "snapshot: at least one batch_id is required")
	batch := appendBatch(t, s, "job-1", nil, barRow("INST_A", "2026-01-05", "10.40", nil))
	_, err = s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-x", BatchIDs: []domain.ID{batch.BatchID, batch.BatchID}})
	assertErr(t, err, domain.CodeValidationInvalid, "contains duplicate")
	_, err = s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-x", BatchIDs: []domain.ID{"batch_missing"}})
	assertErr(t, err, domain.CodeResourceNotFound, "snapshot: batch batch_missing not found")
}

func TestOpenViewPITWinner(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	batch := appendBatch(t, s, "job-1", nil,
		barRowAt("INST_A", "2026-01-05", "10.40", "rev-001", mustTime("2026-01-05T15:30:00Z"), timePtr(mustTime("2026-01-05T15:35:00Z"))),
		barRowAt("INST_A", "2026-01-05", "10.50", "rev-002", mustTime("2026-01-06T09:00:00Z"), timePtr(mustTime("2026-01-06T09:05:00Z"))),
		barRowAt("INST_A", "2026-01-06", "10.70", "rev-001", mustTime("2026-01-06T15:30:00Z"), timePtr(mustTime("2026-01-06T15:35:00Z"))),
	)
	snap, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-pit", BatchIDs: []domain.ID{batch.BatchID}})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	base := domain.DataQuery{
		Dataset:       "bar",
		Frequency:     "daily",
		InstrumentIDs: []domain.ID{"INST_A"},
		Fields:        []string{"close"},
		Range:         domain.Interval{From: mustTime("2026-01-01T00:00:00Z"), To: mustTime("2026-02-01T00:00:00Z")},
	}

	early, err := s.OpenView(ctx, snap.ID, mustTime("2026-01-06T00:00:00Z"))
	if err != nil {
		t.Fatalf("open early view: %v", err)
	}
	if early.SnapshotID() != snap.ID {
		t.Fatalf("view snapshot = %s, want %s", early.SnapshotID(), snap.ID)
	}
	q := base
	q.SnapshotID = snap.ID
	q.AsOf = mustTime("2026-01-06T00:00:00Z")
	page, err := early.Query(ctx, q)
	if err != nil {
		t.Fatalf("query early: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("early items = %d, want 1", len(page.Items))
	}
	if got := page.Items[0].Values["close"].Encoded; got != "10.40" {
		t.Fatalf("early winner close = %q, want 10.40", got)
	}

	late, err := s.OpenView(ctx, snap.ID, mustTime("2026-01-10T00:00:00Z"))
	if err != nil {
		t.Fatalf("open late view: %v", err)
	}
	q.AsOf = mustTime("2026-01-10T00:00:00Z")
	page, err = late.Query(ctx, q)
	if err != nil {
		t.Fatalf("query late: %v", err)
	}
	if len(page.Items) != 2 {
		t.Fatalf("late items = %d, want 2", len(page.Items))
	}
	if got := page.Items[0].Values["close"].Encoded; got != "10.50" {
		t.Fatalf("late winner for 01-05 = %q, want 10.50", got)
	}
	if got := page.Items[1].Values["close"].Encoded; got != "10.70" {
		t.Fatalf("late winner for 01-06 = %q, want 10.70", got)
	}
}

func TestOpenViewQueryGuards(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	batch := appendBatch(t, s, "job-1", nil, barRow("INST_A", "2026-01-05", "10.40", nil), barRow("INST_A", "2026-01-06", "10.50", nil))
	snap, err := s.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-guards", BatchIDs: []domain.ID{batch.BatchID}})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	_, err = s.OpenView(ctx, "snap_missing", testBase)
	assertErr(t, err, domain.CodeResourceNotFound, "data: snapshot snap_missing not found")
	_, err = s.OpenView(ctx, snap.ID, time.Time{})
	assertErr(t, err, domain.CodeValidationInvalid, "data: as_of is required")

	view, err := s.OpenView(ctx, snap.ID, mustTime("2026-01-07T00:00:00Z"))
	if err != nil {
		t.Fatalf("open view: %v", err)
	}
	base := domain.DataQuery{
		SnapshotID:    snap.ID,
		AsOf:          mustTime("2026-01-07T00:00:00Z"),
		Dataset:       "bar",
		Frequency:     "daily",
		InstrumentIDs: []domain.ID{"INST_A"},
		Fields:        []string{"close"},
		Range:         domain.Interval{From: mustTime("2026-01-01T00:00:00Z"), To: mustTime("2026-02-01T00:00:00Z")},
	}
	empty := base
	empty.InstrumentIDs = nil
	_, err = view.Query(ctx, empty)
	assertErr(t, err, domain.CodeValidationInvalid, "data query: instrument_ids is required")

	mismatch := base
	mismatch.SnapshotID = "snap_other"
	_, err = view.Query(ctx, mismatch)
	assertErr(t, err, domain.CodeValidationInvalid, "does not match this view")

	page1, err := view.Query(ctx, func() domain.DataQuery {
		q := base
		q.Limit = 1
		return q
	}())
	if err != nil {
		t.Fatalf("query page 1: %v", err)
	}
	if len(page1.Items) != 1 || page1.NextCursor != "1" {
		t.Fatalf("page 1 = %#v (cursor %q), want 1 item with cursor 1", page1.Items, page1.NextCursor)
	}
	page2, err := view.Query(ctx, func() domain.DataQuery {
		q := base
		q.Cursor = page1.NextCursor
		return q
	}())
	if err != nil {
		t.Fatalf("query page 2: %v", err)
	}
	if len(page2.Items) != 1 || page2.NextCursor != "" {
		t.Fatalf("page 2 = %#v (cursor %q), want 1 item with empty cursor", page2.Items, page2.NextCursor)
	}
	bad := base
	bad.Cursor = "x"
	_, err = view.Query(ctx, bad)
	assertErr(t, err, domain.CodeValidationInvalid, "data query: invalid cursor")
}

func TestConnectionsLifecycle(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	cfg := domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: "synthetic", Version: "v1"},
		Name:     "synthetic-demo",
		Settings: json.RawMessage(`{"include_bad_data":false}`),
	}
	root, err := s.CreateConnection(ctx, cfg)
	if err != nil {
		t.Fatalf("create root: %v", err)
	}
	if !strings.HasPrefix(root.ID.String(), "conn_") {
		t.Fatalf("connection id %q missing conn_ prefix", root.ID)
	}
	if root.Version != "1" {
		t.Fatalf("root version = %q, want 1", root.Version)
	}
	if root.ParentID != "" {
		t.Fatalf("root parent = %q, want empty", root.ParentID)
	}
	if !root.CreatedAt.Equal(testBase) {
		t.Fatalf("created at = %v, want %v", root.CreatedAt, testBase)
	}
	if string(root.Settings) != `{"include_bad_data":false}` {
		t.Fatalf("settings = %s, want verbatim", root.Settings)
	}

	childCfg := cfg
	childCfg.ParentID = root.ID.String()
	childCfg.Name = "synthetic-demo-v2"
	child, err := s.CreateConnection(ctx, childCfg)
	if err != nil {
		t.Fatalf("create child: %v", err)
	}
	if child.Version != "2" {
		t.Fatalf("child version = %q, want 2", child.Version)
	}
	if child.ParentID != root.ID {
		t.Fatalf("child parent = %q, want %s", child.ParentID, root.ID)
	}

	gotRoot, err := s.GetConnection(ctx, root.ID)
	if err != nil {
		t.Fatalf("get root: %v", err)
	}
	if gotRoot.Version != "2" || gotRoot.Name != "synthetic-demo-v2" {
		t.Fatalf("root lookup must resolve to head, got %#v", gotRoot)
	}
	gotChild, err := s.GetConnection(ctx, child.ID)
	if err != nil {
		t.Fatalf("get child: %v", err)
	}
	if gotChild.Version != "2" || gotChild.Name != "synthetic-demo-v2" {
		t.Fatalf("child round trip = %#v", gotChild)
	}

	head, err := s.ListConnections(ctx, "", "", "", 10)
	if err != nil {
		t.Fatalf("list connections: %v", err)
	}
	if len(head.Items) != 1 || head.Items[0].ID != child.ID || head.Items[0].Version != "2" {
		t.Fatalf("head list = %#v, want only child v2", head.Items)
	}
	byName, err := s.ListConnections(ctx, "synthetic-demo", "", "", 10)
	if err != nil {
		t.Fatalf("list by name: %v", err)
	}
	if len(byName.Items) != 1 {
		t.Fatalf("name filter items = %d, want 1", len(byName.Items))
	}
	byNone, err := s.ListConnections(ctx, "nope", "", "", 10)
	if err != nil {
		t.Fatalf("list by none: %v", err)
	}
	if len(byNone.Items) != 0 {
		t.Fatalf("no-match filter items = %d, want 0", len(byNone.Items))
	}
	_, err = s.GetConnection(ctx, "conn_missing")
	assertErr(t, err, domain.CodeResourceNotFound, "data: connection conn_missing not found")
}

func TestConnectionValidation(t *testing.T) {
	s, _ := newTestStore(t)
	ctx := context.Background()
	base := domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: "synthetic", Version: "v1"},
		Name:     "synthetic-demo",
		Settings: json.RawMessage(`{}`),
	}

	noName := base
	noName.Name = ""
	_, err := s.CreateConnection(ctx, noName)
	assertErr(t, err, domain.CodeValidationInvalid, "connection: name is required")

	latest := base
	latest.Provider = domain.VersionRef{ID: "synthetic", Version: "latest"}
	_, err = s.CreateConnection(ctx, latest)
	assertErr(t, err, domain.CodeValidationInvalid, "connection: provider_ref invalid")

	noSettings := base
	noSettings.Settings = nil
	_, err = s.CreateConnection(ctx, noSettings)
	assertErr(t, err, domain.CodeValidationInvalid, "connection: settings is required")

	arraySettings := base
	arraySettings.Settings = json.RawMessage(`[1,2]`)
	_, err = s.CreateConnection(ctx, arraySettings)
	assertErr(t, err, domain.CodeValidationInvalid, "connection: settings must be a JSON object")
}
