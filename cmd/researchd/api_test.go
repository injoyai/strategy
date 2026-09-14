package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/server"
	"github.com/injoyai/strategy/internal/store"
	"github.com/injoyai/strategy/internal/synthetic"
)

// These tests exercise the exact assembly newAPI performs for the deployed
// binary. A surface that is only mounted when one of its dependencies is nil
// disappears silently, so the wiring itself has to be asserted: before this
// test existed, cmd/researchd never supplied the research service and the whole
// M1-09 surface answered 404 in the real process.

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func newTestHandler(t *testing.T) http.Handler {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, silentLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}

	jstore := jobs.NewStore(db, nil, jobs.NewHub())
	if err := jstore.RecoverOrphans(ctx); err != nil {
		t.Fatalf("recover orphaned jobs: %v", err)
	}
	art := artifacts.New(t.TempDir(), db)
	dataStore := data.New(db, nil)
	provider, err := synthetic.Factory{}.Open(ctx, domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: synthetic.ProviderID, Version: synthetic.ProviderVersion},
		Settings: json.RawMessage("{}"),
	})
	if err != nil {
		t.Fatalf("open synthetic provider: %v", err)
	}

	api, err := newAPI(silentLogger(), server.LocalAuth{}, db, jstore, dataStore, art,
		[]server.ProviderRegistration{{Provider: provider, Factory: synthetic.Factory{}}})
	if err != nil {
		t.Fatalf("build api: %v", err)
	}
	return api.Handler()
}

func do(t *testing.T, h http.Handler, method, target string, headers map[string]string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req := httptest.NewRequest(method, target, rdr)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// TestNewAPIMountsEverySurface fails if any contract surface that the binary
// is expected to serve is missing from the assembled API.
func TestNewAPIMountsEverySurface(t *testing.T) {
	h := newTestHandler(t)
	for _, target := range []string{
		"/api/v1/providers",
		"/api/v1/snapshots",
		"/api/v1/jobs",
		"/api/v1/datasets",  // M0 catalog, mounted with the data store
		"/api/v1/universes", // M1-06/M1-09, mounted only with the research service
		"/api/v1/factors",   // M1-07/M1-09, mounted only with the research service
		"/api/v1/screeners", // M1S S2, mounted with the data store
	} {
		rec := do(t, h, http.MethodGet, target, nil, nil)
		if rec.Code != http.StatusOK {
			t.Errorf("GET %s = %d, want 200 (body: %s)", target, rec.Code, rec.Body.String())
		}
	}
}

// TestNewAPIFactorCatalogIsNotEmpty guards the dependency half of the same
// property: mounting /factors with an unregistered registry would answer 200
// with an empty catalog, which is a different failure than a routing miss.
func TestNewAPIFactorCatalogIsNotEmpty(t *testing.T) {
	h := newTestHandler(t)
	rec := do(t, h, http.MethodGet, "/api/v1/factors", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/v1/factors = %d, want 200", rec.Code)
	}
	var page struct {
		Items []json.RawMessage `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode factor page: %v", err)
	}
	if len(page.Items) == 0 {
		t.Fatal("the factor catalog is empty: the default factors were not registered in the wiring")
	}
}

// TestNewAPIScreenRunPreflightIsMounted proves the POST-only screening surface
// is reachable: an unmounted service answers the contract's not-found envelope,
// while a mounted one rejects an empty body as a malformed request.
func TestNewAPIScreenRunPreflightIsMounted(t *testing.T) {
	h := newTestHandler(t)
	rec := do(t, h, http.MethodPost, "/api/v1/screen-runs/preflight", map[string]string{
		"Content-Type": "application/json",
	}, []byte(`{}`))
	if rec.Code == http.StatusNotFound {
		t.Fatalf("POST /api/v1/screen-runs/preflight = 404: the screening run service is not mounted")
	}
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("empty preflight body = %d, want 400 (body: %s)", rec.Code, rec.Body.String())
	}
}

// TestNewAPIAcceptsScreenerCreate proves the store-backed write path is
// reachable end to end from this assembly, not just listed.
func TestNewAPIAcceptsScreenerCreate(t *testing.T) {
	h := newTestHandler(t)
	body := []byte(`{
      "name": "quality-momentum",
      "input_bindings": [{"binding_id":"px","kind":"field","dataset":"bar","field":"close"}],
      "condition_tree": {"node_id":"gt","kind":"compare","input":{"binding_id":"px"},"operator":"gt","value":{"kind":"decimal","value":"10"}},
      "ranking": {"mode":"sort","fields":[{"input":{"binding_id":"px"},"direction":"desc"}]},
      "selection": {"mode":"all"}
    }`)
	rec := do(t, h, http.MethodPost, "/api/v1/screeners", map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "researchd-wiring-1",
	}, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /api/v1/screeners = %d, want 201 (body: %s)", rec.Code, rec.Body.String())
	}
	var created struct {
		ID      string `json:"id"`
		Version string `json:"version"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode screener: %v", err)
	}
	if created.ID == "" || created.Version != "v1" {
		t.Fatalf("created screener = %+v, want an id and version v1", created)
	}
}
