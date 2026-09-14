package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screenrun"
	"github.com/injoyai/strategy/internal/store"
)

// The screening-run preflight closure: references are resolved, the mother pool
// is read through a snapshot-pinned view, the rule set is validated, and each
// declared binding reports whether it could be resolved.

// screenMomentumSpec is a PIT input, so the seeded rows carry published_at —
// the engine excludes rows without it and would otherwise report every member
// as missing.
func screenMomentumSpec() *factor.Spec {
	return &factor.Spec{
		ID:      "momentum",
		Version: "1.0.0",
		Title:   "Momentum",
		Kind:    factor.KindBuiltin,
		Params: []factor.Param{
			{Name: "n", Type: factor.ParamInteger, Required: true},
		},
		Inputs: []factor.Input{{
			Name: "close", Dataset: "bar", Field: "close", Frequency: "daily",
			Lookback: 1, Unit: "price", PIT: true,
		}},
		OutputUnit:   "ratio",
		AssetClasses: []string{"equity"},
		Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) {
			return domain.Decimal("1"), "", nil
		},
	}
}

// screenSlowSpec needs more history than the fixture snapshot carries, so the
// preflight has to report insufficient history instead of shortening the window.
func screenSlowSpec() *factor.Spec {
	return &factor.Spec{
		ID:      "slow-momentum",
		Version: "1.0.0",
		Title:   "Slow momentum",
		Kind:    factor.KindBuiltin,
		Params: []factor.Param{
			{Name: "n", Type: factor.ParamInteger, Required: true},
		},
		Inputs: []factor.Input{{
			Name: "close", Dataset: "bar", Field: "close", Frequency: "daily",
			Lookback: 5, Unit: "price", PIT: true,
		}},
		OutputUnit:   "ratio",
		AssetClasses: []string{"equity"},
		Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) {
			return domain.Decimal("1"), "", nil
		},
	}
}

func screenBarRow(inst string) domain.Observation {
	at := time.Date(2026, 1, 5, 15, 30, 0, 0, time.UTC)
	published := at
	instrument := domain.ID(inst)
	return domain.Observation{
		InstrumentID: &instrument,
		Dataset:      "bar",
		EventTime:    time.Date(2026, 1, 5, 15, 0, 0, 0, time.UTC),
		Values:       map[string]domain.Value{"close": {Kind: domain.ValueDecimal, Encoded: "10.40"}},
		Provenance: domain.Provenance{
			SourceID:       "synthetic",
			SourceRecordID: "bar-" + inst,
			RevisionID:     "rev-001",
			AvailableAt:    at,
			IngestedAt:     at,
			PublishedAt:    &published,
		},
	}
}

type screenStack struct {
	handler  http.Handler
	data     *data.Store
	snapshot domain.Snapshot
	universe domain.UniverseVersion
	screener screenerWire
}

// newScreenStack seeds a snapshot, a static universe and a screener version so
// the preflight has real frozen inputs to resolve.
func newScreenStack(t *testing.T) *screenStack {
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
	dataStore := data.New(db, fixedClock())
	registry, err := factor.NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	if err := registry.Register(screenMomentumSpec()); err != nil {
		t.Fatalf("register momentum: %v", err)
	}
	if err := registry.Register(screenSlowSpec()); err != nil {
		t.Fatalf("register slow momentum: %v", err)
	}
	runs, err := screenrun.New(dataStore, registry, factor.NewCache())
	if err != nil {
		t.Fatalf("build screenrun service: %v", err)
	}
	api := NewAPI(Options{
		Log:         silentLogger(),
		Auth:        LocalAuth{},
		Clock:       fixedClock(),
		Idempotency: store.NewIdempotencyStore(db),
		Data:        dataStore,
		ScreenRuns:  runs,
	})
	h := api.Handler()

	receipt, err := dataStore.Append(ctx, ports.BatchInput{
		JobID:        "job-1",
		Dataset:      "bar",
		Frequency:    "daily",
		Observations: []domain.Observation{screenBarRow("INST_A"), screenBarRow("INST_B")},
	})
	if err != nil {
		t.Fatalf("append batch: %v", err)
	}
	snapshot, err := dataStore.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap-one", BatchIDs: []domain.ID{receipt.BatchID}})
	if err != nil {
		t.Fatalf("publish snapshot: %v", err)
	}
	universe, err := dataStore.CreateUniverseVersion(ctx, domain.UniverseVersionRequest{
		Name:       "pool",
		SnapshotID: snapshot.ID,
		Definition: domain.UniverseDefinition{Kind: domain.UniverseStatic, Members: []domain.ID{"INST_A", "INST_B"}},
	})
	if err != nil {
		t.Fatalf("create universe: %v", err)
	}
	return &screenStack{
		handler:  h,
		data:     dataStore,
		snapshot: snapshot,
		universe: universe,
		screener: saveScreener(t, h, "screenrun-seed-1", screenerWithBindings),
	}
}

// screenerWithBindings declares one field binding and one factor binding, the
// two coverage classes the preflight has to tell apart.
const screenerWithBindings = `{
  "name": "momentum-pool",
  "input_bindings": [
    {"binding_id":"px","kind":"field","dataset":"bar","field":"close"},
    {"binding_id":"mom","kind":"factor","factor_ref":{"id":"momentum","version":"1.0.0"},"params":{"n":20}}
  ],
  "condition_tree": {"node_id":"gt","kind":"compare","input":{"binding_id":"px"},"operator":"gt","value":{"kind":"decimal","value":"10"}},
  "ranking": {"mode":"sort","fields":[{"input":{"binding_id":"px"},"direction":"desc"}]},
  "selection": {"mode":"all"}
}`

func (s *screenStack) preflightBody(universeVersion string) []byte {
	body := fmt.Sprintf(`{
      "screener_ref": {"id": %q, "version": %q},
      "snapshot_id": %q,
      "universe_ref": {"id": %q, "version": %q},
      "as_of": "2026-01-15T00:00:00Z",
      "decision_timezone": "Asia/Shanghai",
      "strict_pit": false,
      "required_value_policy": "exclude_instrument"
    }`, s.screener.ID, s.screener.Version, s.snapshot.ID, s.universe.ID, universeVersion)
	return []byte(body)
}

func (s *screenStack) preflight(t *testing.T, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	return doRequest(t, s.handler, http.MethodPost, "/api/v1/screen-runs/preflight", map[string]string{
		"Content-Type": "application/json",
	}, body)
}

func TestScreenRunPreflightResolvesFrozenInputs(t *testing.T) {
	stack := newScreenStack(t)
	rec := stack.preflight(t, stack.preflightBody(stack.universe.DefinitionHash))
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Valid  bool `json:"valid"`
		Issues []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
		} `json:"issues"`
		Coverage []struct {
			BindingID string  `json:"binding_id"`
			Available bool    `json:"available"`
			Reason    *string `json:"reason"`
		} `json:"coverage"`
		EstimatedScanRows *int `json:"estimated_scan_rows"`
		EstimatedRows     *int `json:"estimated_rows"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	if !out.Valid {
		t.Fatalf("preflight = %s, want valid (only warnings expected)", rec.Body.String())
	}
	if len(out.Coverage) != 2 {
		t.Fatalf("coverage = %+v, want one entry per binding", out.Coverage)
	}
	seen := map[string]bool{}
	for _, c := range out.Coverage {
		seen[c.BindingID] = true
		switch c.BindingID {
		case "mom":
			if !c.Available || c.Reason != nil {
				t.Fatalf("factor binding coverage = %+v, want available", c)
			}
		case "px":
			if c.Available || c.Reason == nil || *c.Reason != "screenrun.catalog_unavailable" {
				t.Fatalf("field binding coverage = %+v, want unavailable with the catalog reason", c)
			}
		}
	}
	if !seen["px"] || !seen["mom"] {
		t.Fatalf("coverage = %+v, want both bindings", out.Coverage)
	}
	for _, issue := range out.Issues {
		if issue.Severity == "error" {
			t.Fatalf("unexpected error issue: %+v", issue)
		}
	}
	// The estimates are derived from the resolved inputs: two members, one date
	// in the derived window.
	if out.EstimatedRows == nil || *out.EstimatedRows != 2 {
		t.Fatalf("estimated_rows = %v, want 2", out.EstimatedRows)
	}
	if out.EstimatedScanRows == nil || *out.EstimatedScanRows != 2 {
		t.Fatalf("estimated_scan_rows = %v, want 2 (2 members x 1 date)", out.EstimatedScanRows)
	}
}

// TestScreenRunPreflightReportsInsufficientHistory proves a snapshot that does
// not carry the factor's declared lookback is a failure with a reason, not a
// silently shortened window.
func TestScreenRunPreflightReportsInsufficientHistory(t *testing.T) {
	stack := newScreenStack(t)
	slow := saveScreener(t, stack.handler, "screenrun-seed-slow", `{
      "name": "slow-pool",
      "input_bindings": [
        {"binding_id":"mom","kind":"factor","factor_ref":{"id":"slow-momentum","version":"1.0.0"},"params":{"n":20}}
      ],
      "condition_tree": {"node_id":"gt","kind":"missing","input":{"binding_id":"mom"},"is_present":true},
      "ranking": {"mode":"sort","fields":[{"input":{"binding_id":"mom"},"direction":"desc"}]},
      "selection": {"mode":"all"}
    }`)

	body := []byte(fmt.Sprintf(`{
      "screener_ref": {"id": %q, "version": %q},
      "snapshot_id": %q,
      "universe_ref": {"id": %q, "version": %q},
      "as_of": "2026-01-15T00:00:00Z",
      "decision_timezone": "Asia/Shanghai",
      "strict_pit": false,
      "required_value_policy": "exclude_instrument"
    }`, slow.ID, slow.Version, stack.snapshot.ID, stack.universe.ID, stack.universe.DefinitionHash))
	rec := stack.preflight(t, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("preflight status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Valid  bool `json:"valid"`
		Issues []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
		} `json:"issues"`
		Coverage []struct {
			BindingID string  `json:"binding_id"`
			Available bool    `json:"available"`
			Reason    *string `json:"reason"`
		} `json:"coverage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	if out.Valid {
		t.Fatalf("preflight = %s, want invalid: the snapshot cannot satisfy a five-point lookback", rec.Body.String())
	}
	var insufficient bool
	for _, issue := range out.Issues {
		if issue.Code == "insufficient_history" && issue.Severity == "error" {
			insufficient = true
		}
	}
	if !insufficient {
		t.Fatalf("issues = %+v, want an error-severity insufficient_history", out.Issues)
	}
	for _, c := range out.Coverage {
		if c.BindingID == "mom" {
			if c.Available || c.Reason == nil || *c.Reason != "insufficient_history" {
				t.Fatalf("coverage = %+v, want unavailable with reason insufficient_history", c)
			}
		}
	}
}

func TestScreenRunPreflightRejectsUnpinnedUniverse(t *testing.T) {
	stack := newScreenStack(t)
	rec := stack.preflight(t, stack.preflightBody("not-the-definition-hash"))
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if env := decodeWireError(t, rec); env.Code != "resource.conflict" {
		t.Fatalf("code = %q, want resource.conflict", env.Code)
	}
}

func TestScreenRunPreflightRejectsUnknownReferences(t *testing.T) {
	stack := newScreenStack(t)
	body := []byte(fmt.Sprintf(`{
      "screener_ref": {"id": "scr_missing", "version": "v1"},
      "snapshot_id": %q,
      "universe_ref": {"id": %q, "version": %q},
      "as_of": "2026-01-15T00:00:00Z",
      "decision_timezone": "Asia/Shanghai",
      "strict_pit": false,
      "required_value_policy": "exclude_instrument"
    }`, stack.snapshot.ID, stack.universe.ID, stack.universe.DefinitionHash))
	rec := stack.preflight(t, body)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if env := decodeWireError(t, rec); env.Code != "resource.not_found" {
		t.Fatalf("code = %q, want resource.not_found", env.Code)
	}
}

// TestScreenRunPreflightRejectsMalformedRequest pins the boundary: a request
// that cannot be evaluated is an HTTP failure carrying no findings, because
// there is nothing resolved to report findings about.
func TestScreenRunPreflightRejectsMalformedRequest(t *testing.T) {
	stack := newScreenStack(t)
	rec := stack.preflight(t, []byte(`{
      "screener_ref": {"id":"scr_1","version":"v1"},
      "snapshot_id": "snap_1",
      "universe_ref": {"id":"univ_1","version":"hash_1"},
      "as_of": "2026-01-15T00:00:00Z",
      "decision_timezone": "Asia/Shanghai",
      "required_value_policy": "zero_fill"
    }`))
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
	}
	if env := decodeWireError(t, rec); env.Code != "validation.invalid" {
		t.Fatalf("code = %q, want validation.invalid", env.Code)
	}
}
