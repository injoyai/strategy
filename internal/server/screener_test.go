package server

import (
	"context"
	"encoding/json"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/store"
)

// The M1S screener surface closure: save an immutable version, read it back by
// (id, version), page the catalog, and reject what the rule contract forbids.

const screenerBody = `{
  "name": "quality-momentum",
  "description": "close above 10",
  "input_bindings": [{"binding_id":"px","kind":"field","dataset":"bar","field":"close"}],
  "condition_tree": {"node_id":"gt","kind":"compare","input":{"binding_id":"px"},"operator":"gt","value":{"kind":"decimal","value":"10"}},
  "ranking": {"mode":"sort","fields":[{"input":{"binding_id":"px"},"direction":"desc"}]},
  "selection": {"mode":"top_n","n":5},
  "display_columns": ["px"]
}`

type screenerWire struct {
	ID                string            `json:"id"`
	Version           string            `json:"version"`
	Name              string            `json:"name"`
	Description       string            `json:"description"`
	ParentID          string            `json:"parent_id"`
	RuleSchemaVersion string            `json:"rule_schema_version"`
	CreatedAt         string            `json:"created_at"`
	InputBindings     []json.RawMessage `json:"input_bindings"`
	Selection         struct {
		Mode string `json:"mode"`
		N    int64  `json:"n"`
	} `json:"selection"`
}

type screenerPageWire struct {
	Items      []screenerWire `json:"items"`
	NextCursor *string        `json:"next_cursor"`
}

func newScreenerAPI(t *testing.T) http.Handler {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(context.Background(), db, silentLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	api := NewAPI(Options{
		Log:         silentLogger(),
		Auth:        LocalAuth{},
		Clock:       fixedClock(),
		Idempotency: store.NewIdempotencyStore(db),
		Data:        data.New(db, fixedClock()),
	})
	return api.Handler()
}

func saveScreener(t *testing.T, h http.Handler, key, body string) screenerWire {
	t.Helper()
	rec := doRequest(t, h, http.MethodPost, "/api/v1/screeners", map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": key,
	}, []byte(body))
	if rec.Code != http.StatusCreated {
		t.Fatalf("create screener status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	var wire screenerWire
	if err := json.Unmarshal(rec.Body.Bytes(), &wire); err != nil {
		t.Fatalf("decode screener: %v", err)
	}
	return wire
}

func TestScreenerCreateReadList(t *testing.T) {
	h := newScreenerAPI(t)
	created := saveScreener(t, h, "screener-key-1", screenerBody)
	if created.Version != "v1" {
		t.Fatalf("version = %q, want v1", created.Version)
	}
	if created.RuleSchemaVersion != "screener-rule/1" {
		t.Fatalf("rule_schema_version = %q, want screener-rule/1", created.RuleSchemaVersion)
	}
	if created.CreatedAt == "" || created.Description != "close above 10" || len(created.InputBindings) != 1 {
		t.Fatalf("created screener lost data: %+v", created)
	}

	rec := doRequest(t, h, http.MethodGet, "/api/v1/screeners/"+created.ID+"?version="+created.Version, nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get screener status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var fetched screenerWire
	if err := json.Unmarshal(rec.Body.Bytes(), &fetched); err != nil {
		t.Fatalf("decode fetched screener: %v", err)
	}
	if fetched.ID != created.ID || fetched.Version != created.Version || fetched.Name != created.Name {
		t.Fatalf("fetched screener = %+v, want the saved one %+v", fetched, created)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/screeners", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("list screeners status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var page screenerPageWire
	if err := json.Unmarshal(rec.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode page: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != created.ID {
		t.Fatalf("page items = %+v, want the created screener", page.Items)
	}
	if page.NextCursor != nil {
		t.Fatalf("next_cursor = %v, want null on the last page", *page.NextCursor)
	}
}

// TestScreenerGetRequiresVersion pins the contract: an id alone does not name
// a frozen rule set, so the version query parameter is mandatory and an
// unknown revision is a miss rather than a fallback to the latest.
func TestScreenerGetRequiresVersion(t *testing.T) {
	h := newScreenerAPI(t)
	created := saveScreener(t, h, "screener-key-1", screenerBody)

	rec := doRequest(t, h, http.MethodGet, "/api/v1/screeners/"+created.ID, nil, nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing version status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if env := decodeWireError(t, rec); env.Code != "validation.invalid" {
		t.Fatalf("missing version code = %q, want validation.invalid", env.Code)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/v1/screeners/"+created.ID+"?version=v9", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown version status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if env := decodeWireError(t, rec); env.Code != "resource.not_found" {
		t.Fatalf("unknown version code = %q, want resource.not_found", env.Code)
	}
}

func TestScreenerCreateRejectsInvalidDefinition(t *testing.T) {
	h := newScreenerAPI(t)
	// The condition references a binding that is not declared: the payload
	// parses, the rule contract does not hold.
	body := `{
      "name": "broken",
      "input_bindings": [{"binding_id":"px","kind":"field","dataset":"bar","field":"close"}],
      "condition_tree": {"node_id":"gt","kind":"compare","input":{"binding_id":"missing"},"operator":"gt","value":{"kind":"decimal","value":"10"}},
      "ranking": {"mode":"sort","fields":[{"input":{"binding_id":"px"},"direction":"desc"}]},
      "selection": {"mode":"all"}
    }`
	rec := doRequest(t, h, http.MethodPost, "/api/v1/screeners", map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "screener-key-1",
	}, []byte(body))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("invalid definition status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if env := decodeWireError(t, rec); env.Code != "screening.definition_invalid" {
		t.Fatalf("invalid definition code = %q, want screening.definition_invalid", env.Code)
	}

	// A malformed payload (no condition tree at all) is rejected before the
	// definition view is even built.
	rec = doRequest(t, h, http.MethodPost, "/api/v1/screeners", map[string]string{
		"Content-Type":    "application/json",
		"Idempotency-Key": "screener-key-2",
	}, []byte(`{"name":"empty"}`))
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("missing condition tree status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
}

// TestScreenerCreateIdempotentReplay pins the two layers: the same
// Idempotency-Key replays the stored response, while a fresh key mints a new
// immutable revision (save is deliberately not idempotent by content).
func TestScreenerCreateIdempotentReplay(t *testing.T) {
	h := newScreenerAPI(t)
	first := saveScreener(t, h, "screener-key-1", screenerBody)
	replay := saveScreener(t, h, "screener-key-1", screenerBody)
	if replay.ID != first.ID || replay.Version != first.Version {
		t.Fatalf("replay minted %s@%s, want the original %s@%s", replay.ID, replay.Version, first.ID, first.Version)
	}

	second := saveScreener(t, h, "screener-key-2", screenerBody)
	if second.ID == first.ID {
		t.Fatalf("a fresh key reused screener id %s", second.ID)
	}
	if second.Version != "v1" {
		t.Fatalf("new screener version = %q, want v1", second.Version)
	}
}

func TestScreenerCreateAppendsRevision(t *testing.T) {
	h := newScreenerAPI(t)
	first := saveScreener(t, h, "screener-key-1", screenerBody)

	body := `{
      "name": "quality-momentum",
      "parent_id": "` + first.ID + `",
      "input_bindings": [{"binding_id":"px","kind":"field","dataset":"bar","field":"close"}],
      "condition_tree": {"node_id":"gt","kind":"compare","input":{"binding_id":"px"},"operator":"gt","value":{"kind":"decimal","value":"10"}},
      "ranking": {"mode":"sort","fields":[{"input":{"binding_id":"px"},"direction":"desc"}]},
      "selection": {"mode":"all"},
      "display_columns": ["px"]
    }`
	second := saveScreener(t, h, "screener-key-2", body)
	if second.ID != first.ID {
		t.Fatalf("revision id = %q, want the parent id %q", second.ID, first.ID)
	}
	if second.Version != "v2" {
		t.Fatalf("revision version = %q, want v2", second.Version)
	}
	if second.ParentID != first.ID {
		t.Fatalf("parent_id = %q, want %q", second.ParentID, first.ID)
	}

	// The first revision stays readable and unchanged.
	rec := doRequest(t, h, http.MethodGet, "/api/v1/screeners/"+first.ID+"?version=v1", nil, nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("get first revision status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var frozen screenerWire
	if err := json.Unmarshal(rec.Body.Bytes(), &frozen); err != nil {
		t.Fatalf("decode frozen revision: %v", err)
	}
	if frozen.Selection.Mode != "top_n" || frozen.Selection.N != 5 {
		t.Fatalf("first revision changed: %+v", frozen.Selection)
	}
}

func TestScreenerCreateRequiresIdempotencyKey(t *testing.T) {
	h := newScreenerAPI(t)
	rec := doRequest(t, h, http.MethodPost, "/api/v1/screeners", map[string]string{
		"Content-Type": "application/json",
	}, []byte(screenerBody))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing key status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if env := decodeWireError(t, rec); env.Code != "validation.invalid" {
		t.Fatalf("missing key code = %q, want validation.invalid", env.Code)
	}
}
