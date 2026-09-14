package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/store"
)

// The dataset catalog is a read-only surface derived from persisted state: the
// declared schema captured at ingestion, the observed field shapes and the
// coverage and findings of the dataset's batches.

func datasetCatalogStack(t *testing.T) (http.Handler, *data.Store) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(context.Background(), db, silentLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dataStore := data.New(db, fixedClock())
	api := NewAPI(Options{
		Log:         silentLogger(),
		Auth:        LocalAuth{},
		Clock:       fixedClock(),
		Idempotency: store.NewIdempotencyStore(db),
		Data:        dataStore,
	})
	return api.Handler(), dataStore
}

func seedDeclaredBar(t *testing.T, store *data.Store) {
	t.Helper()
	at := time.Date(2026, 1, 5, 15, 30, 0, 0, time.UTC)
	published := at
	instrument := domain.ID("INST_A")
	if _, err := store.Append(context.Background(), ports.BatchInput{
		JobID:     "job-1",
		Dataset:   "bar",
		Frequency: "daily",
		Observations: []domain.Observation{{
			InstrumentID: &instrument,
			Dataset:      "bar",
			EventTime:    time.Date(2026, 1, 5, 15, 0, 0, 0, time.UTC),
			Values:       map[string]domain.Value{"close": {Kind: domain.ValueDecimal, Encoded: "10.40"}},
			Provenance: domain.Provenance{
				SourceID: "synthetic", SourceRecordID: "bar-INST_A", RevisionID: "rev-001",
				AvailableAt: at, IngestedAt: at, PublishedAt: &published, SchemaVersion: "bar/v1",
			},
		}},
		Declaration: &domain.DatasetDeclaration{
			Frequency:             "daily",
			AvailabilityPolicyRef: domain.VersionRef{ID: "availability", Version: "v1"},
			Fields:                []domain.DatasetField{{Name: "close", Unit: "cny"}},
		},
	}); err != nil {
		t.Fatalf("append batch: %v", err)
	}
}

func TestDatasetCatalogListAndRead(t *testing.T) {
	h, dataStore := datasetCatalogStack(t)
	seedDeclaredBar(t, dataStore)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/datasets", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list datasets = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var page struct {
		Items []struct {
			ID            string `json:"id"`
			Name          string `json:"name"`
			SchemaVersion string `json:"schema_version"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode dataset page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != "bar" || page.Items[0].SchemaVersion != "bar/v1" {
		t.Fatalf("items = %+v, want the declared bar dataset", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatalf("next_cursor = %v, want null on the last page", *page.NextCursor)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/datasets/bar", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get dataset = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var dataset struct {
		ID                    string            `json:"id"`
		Fields                []json.RawMessage `json:"fields"`
		NaturalKey            []string          `json:"natural_key"`
		AvailabilityPolicyRef struct {
			ID      string `json:"id"`
			Version string `json:"version"`
		} `json:"availability_policy_ref"`
		Coverage *struct {
			From string `json:"from"`
			To   string `json:"to"`
		} `json:"coverage"`
		QualityIssues []json.RawMessage `json:"quality_issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &dataset); err != nil {
		t.Fatalf("decode dataset: %v", err)
	}
	if dataset.AvailabilityPolicyRef.ID != "availability" || dataset.AvailabilityPolicyRef.Version != "v1" {
		t.Fatalf("availability_policy_ref = %+v, want the persisted declaration's", dataset.AvailabilityPolicyRef)
	}
	if dataset.Coverage == nil || dataset.Coverage.From == "" {
		t.Fatalf("coverage = %+v, want the observed span", dataset.Coverage)
	}
	if len(dataset.NaturalKey) == 0 || len(dataset.Fields) == 0 {
		t.Fatalf("natural_key/fields = %v/%d, want both populated", dataset.NaturalKey, len(dataset.Fields))
	}
	// quality_issues is required by the contract and must render as an array.
	if dataset.QualityIssues == nil {
		t.Fatal("quality_issues must not be null")
	}
}

func TestDatasetCatalogUnknownIsNotFound(t *testing.T) {
	h, _ := datasetCatalogStack(t)
	rec := doRequest(t, h, http.MethodGet, "/api/v1/datasets/missing", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown dataset = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if env := decodeWireError(t, rec); env.Code != "resource.not_found" {
		t.Fatalf("code = %q, want resource.not_found", env.Code)
	}
}
