package data

import (
	"context"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// datasetObservation carries a schema version and a deliberately odd shape: one
// declared field with a value, one declared field with no value at all, one
// undeclared field, and one field stored as missing.
func datasetObservation(dataset, inst string) domain.Observation {
	at := mustTime("2026-01-05T15:30:00Z")
	published := at
	instrument := domain.ID(inst)
	return domain.Observation{
		InstrumentID: &instrument,
		Dataset:      dataset,
		EventTime:    mustTime("2026-01-05T15:00:00Z"),
		Values: map[string]domain.Value{
			"close": {Kind: domain.ValueDecimal, Encoded: "10.40"},
			"unit":  {Kind: domain.ValueString, Encoded: "CNY"},
			"note":  {MissingReason: "missing_input"},
		},
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "bar-" + inst,
			RevisionID:     "rev-001",
			AvailableAt:    at,
			IngestedAt:     at,
			PublishedAt:    &published,
			SchemaVersion:  "bar/v1",
		},
	}
}

func declaredBar() *domain.DatasetDeclaration {
	return &domain.DatasetDeclaration{
		Frequency:             "daily",
		AvailabilityPolicyRef: domain.VersionRef{ID: "availability", Version: "v1"},
		Fields: []domain.DatasetField{
			{Name: "close", Unit: "cny"},
			{Name: "pe", Unit: "ratio"},
		},
	}
}

func appendDeclaredBatch(t *testing.T, s *Store, declaration *domain.DatasetDeclaration) {
	t.Helper()
	if _, err := s.Append(context.Background(), ports.BatchInput{
		JobID:        "job-1",
		Dataset:      "bar",
		Frequency:    "daily",
		Observations: []domain.Observation{datasetObservation("bar", "INST_A")},
		Issues: []domain.Issue{{
			Code: "quality.unit_undeclared", Path: "values.unit",
			Message: "unit was not declared", Severity: domain.SeverityWarning,
		}},
		Declaration: declaration,
	}); err != nil {
		t.Fatalf("append batch: %v", err)
	}
}

// TestDatasetCatalogReportsDeclaredSchemaAndObservedShapes is the contract of
// the catalog: declared fields carry declared units and observed types,
// observed-but-undeclared fields carry an empty unit, and a declared field with
// no values reports an unknown type instead of a guess.
func TestDatasetCatalogReportsDeclaredSchemaAndObservedShapes(t *testing.T) {
	store, _ := newTestStore(t)
	appendDeclaredBatch(t, store, declaredBar())

	dataset, err := store.GetDataset(context.Background(), "bar")
	if err != nil {
		t.Fatalf("get dataset: %v", err)
	}
	if dataset.ID != "bar" || dataset.Name != "bar" {
		t.Fatalf("dataset identity = %q/%q, want bar/bar", dataset.ID, dataset.Name)
	}
	if dataset.SchemaVersion != "bar/v1" {
		t.Fatalf("schema version = %q, want bar/v1 (from provenance)", dataset.SchemaVersion)
	}
	if dataset.AvailabilityPolicyRef != (domain.VersionRef{ID: "availability", Version: "v1"}) {
		t.Fatalf("availability policy ref = %+v, want the persisted declaration's", dataset.AvailabilityPolicyRef)
	}
	if len(dataset.NaturalKey) != 3 || dataset.NaturalKey[0] != "instrument_id" {
		t.Fatalf("natural key = %v, want the stored identity columns", dataset.NaturalKey)
	}
	if len(dataset.Frequencies) != 1 || dataset.Frequencies[0] != "daily" {
		t.Fatalf("frequencies = %v, want the frequency the batch was ingested at", dataset.Frequencies)
	}
	if dataset.Coverage == nil {
		t.Fatal("coverage must be reported for a dataset with rows")
	}
	if got := dataset.Coverage.From.Format("2006-01-02T15:04:05Z07:00"); got != "2026-01-05T15:00:00Z" {
		t.Fatalf("coverage from = %s, want the observation's event time", got)
	}

	want := []struct {
		name     string
		typ      domain.FieldType
		unit     string
		nullable bool
	}{
		{"close", domain.FieldDecimal, "cny", false},
		{"pe", domain.FieldUnknown, "ratio", false},
		{"note", domain.FieldUnknown, "", true},
		{"unit", domain.FieldString, "", false},
	}
	if len(dataset.Fields) != len(want) {
		t.Fatalf("fields = %+v, want %d entries", dataset.Fields, len(want))
	}
	for i, expected := range want {
		field := dataset.Fields[i]
		if field.Name != expected.name || field.Type != expected.typ || field.Unit != expected.unit || field.Nullable != expected.nullable {
			t.Fatalf("fields[%d] = %+v, want %+v", i, field, expected)
		}
	}
	if len(dataset.QualityIssues) != 1 || dataset.QualityIssues[0].Code != "quality.unit_undeclared" {
		t.Fatalf("quality issues = %+v, want the batch's finding", dataset.QualityIssues)
	}
}

// TestDatasetCatalogListsEveryIngestedFrequency proves the frequency list is
// what a reader can actually find, not just the newest declaration: a dataset
// ingested at two frequencies reports both, so a factor reading a frequency
// outside the list is told instead of silently reading nothing.
func TestDatasetCatalogListsEveryIngestedFrequency(t *testing.T) {
	store, _ := newTestStore(t)
	appendDeclaredBatch(t, store, declaredBar())
	if _, err := store.Append(context.Background(), ports.BatchInput{
		JobID:        "job-2",
		Dataset:      "bar",
		Frequency:    "weekly",
		Observations: []domain.Observation{datasetObservation("bar", "INST_B")},
	}); err != nil {
		t.Fatalf("append weekly batch: %v", err)
	}

	dataset, err := store.GetDataset(context.Background(), "bar")
	if err != nil {
		t.Fatalf("get dataset: %v", err)
	}
	if len(dataset.Frequencies) != 2 || dataset.Frequencies[0] != "daily" || dataset.Frequencies[1] != "weekly" {
		t.Fatalf("frequencies = %v, want both ingested frequencies in order", dataset.Frequencies)
	}
}

// TestDatasetCatalogListsAndPages mirrors the other list APIs: name keyset,
// server-side paging and a not-found error for an unknown id.
func TestDatasetCatalogListsAndPages(t *testing.T) {
	store, _ := newTestStore(t)
	appendDeclaredBatch(t, store, declaredBar())
	// A second dataset with neither a declaration nor rows keeps its own row in
	// the catalog: batches are the dataset identity, not observations.
	if _, err := store.Append(context.Background(), ports.BatchInput{
		JobID:        "job-2",
		Dataset:      "valuation",
		Frequency:    "daily",
		Observations: []domain.Observation{datasetObservation("valuation", "INST_A")},
	}); err != nil {
		t.Fatalf("append valuation batch: %v", err)
	}

	page, err := store.ListDatasets(context.Background(), ports.DatasetFilter{Limit: 1, Sort: "id"})
	if err != nil {
		t.Fatalf("list datasets: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "bar" {
		t.Fatalf("first page = %+v, want bar", page.Items)
	}
	if page.NextCursor != "bar" {
		t.Fatalf("next cursor = %q, want bar", page.NextCursor)
	}
	next, err := store.ListDatasets(context.Background(), ports.DatasetFilter{Limit: 1, Sort: "id", AfterID: page.NextCursor})
	if err != nil {
		t.Fatalf("list second page: %v", err)
	}
	if len(next.Items) != 1 || next.Items[0].ID != "valuation" {
		t.Fatalf("second page = %+v, want valuation", next.Items)
	}
	if next.NextCursor != "" {
		t.Fatalf("next cursor = %q, want empty on the last page", next.NextCursor)
	}
	// The undeclared dataset still reports a row count through coverage, but no
	// declared schema.
	if len(next.Items[0].Fields) == 0 {
		t.Fatal("observed fields must be reported even without a declaration")
	}
	if next.Items[0].AvailabilityPolicyRef != (domain.VersionRef{}) {
		t.Fatalf("undeclared dataset policy = %+v, want the zero value", next.Items[0].AvailabilityPolicyRef)
	}

	if _, err := store.GetDataset(context.Background(), "missing"); err == nil {
		t.Fatal("unknown dataset must be not-found")
	} else if code := domain.ErrorCode(err); code != domain.CodeResourceNotFound {
		t.Fatalf("unknown dataset code = %q, want %q", code, domain.CodeResourceNotFound)
	}
}

// TestAppendRejectsContradictoryDeclaration pins the write boundary: one target
// field cannot carry two declared units, so a contradictory mapping fails
// loudly instead of weakening every later unit check.
func TestAppendRejectsContradictoryDeclaration(t *testing.T) {
	store, _ := newTestStore(t)
	before, err := store.ListBatches(context.Background(), ports.BatchFilter{})
	if err != nil {
		t.Fatalf("list batches: %v", err)
	}

	_, err = store.Append(context.Background(), ports.BatchInput{
		JobID:        "job-1",
		Dataset:      "bar",
		Frequency:    "daily",
		Observations: []domain.Observation{datasetObservation("bar", "INST_A")},
		Declaration: &domain.DatasetDeclaration{
			Frequency:             "daily",
			AvailabilityPolicyRef: domain.VersionRef{ID: "availability", Version: "v1"},
			Fields: []domain.DatasetField{
				{Name: "close", Unit: "cny"},
				{Name: "close", Unit: "usd"},
			},
		},
	})
	if err == nil {
		t.Fatal("a field declared with two units must be rejected")
	}
	if code := domain.ErrorCode(err); code != domain.CodeValidationInvalid {
		t.Fatalf("code = %q, want %q", code, domain.CodeValidationInvalid)
	}
	after, err := store.ListBatches(context.Background(), ports.BatchFilter{})
	if err != nil {
		t.Fatalf("list batches: %v", err)
	}
	if len(after.Items) != len(before.Items) {
		t.Fatal("a rejected append must not leave a batch behind")
	}
}
