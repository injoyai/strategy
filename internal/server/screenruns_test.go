package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
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
	jobs     *jobs.Store
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
	jstore := jobs.NewStore(db, fixedClock(), nil)
	api := NewAPI(Options{
		Log:         silentLogger(),
		Auth:        LocalAuth{},
		Clock:       fixedClock(),
		Idempotency: store.NewIdempotencyStore(db),
		Jobs:        jstore,
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
		jobs:     jstore,
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
			// The dataset catalog resolves bar/close from the seeded rows, so the
			// field binding is verifiable and contributes a literal kind check.
			if !c.Available || c.Reason != nil {
				t.Fatalf("field binding coverage = %+v, want available", c)
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

// TestScreenRunPreflightChecksLiteralAgainstCatalogType is the payoff of the
// dataset catalog: the condition's literal kind is compared against the field's
// observed type, so a boolean threshold on a decimal field is a finding instead
// of a runtime surprise.
func TestScreenRunPreflightChecksLiteralAgainstCatalogType(t *testing.T) {
	stack := newScreenStack(t)
	typed := saveScreener(t, stack.handler, "screenrun-seed-typed", `{
      "name": "typed-pool",
      "input_bindings": [
        {"binding_id":"px","kind":"field","dataset":"bar","field":"close"}
      ],
      "condition_tree": {"node_id":"gt","kind":"compare","input":{"binding_id":"px"},"operator":"gt","value":{"kind":"boolean","value":"true"}},
      "ranking": {"mode":"sort","fields":[{"input":{"binding_id":"px"},"direction":"desc"}]},
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
    }`, typed.ID, typed.Version, stack.snapshot.ID, stack.universe.ID, stack.universe.DefinitionHash))
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
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode preflight: %v", err)
	}
	if out.Valid {
		t.Fatalf("preflight = %s, want invalid: a boolean threshold cannot compare a decimal field", rec.Body.String())
	}
	for _, issue := range out.Issues {
		if issue.Code == "screening.literal_kind_mismatch" && issue.Severity == "error" {
			return
		}
	}
	t.Fatalf("issues = %+v, want an error-severity screening.literal_kind_mismatch", out.Issues)
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

// submit posts a run submission with the given idempotency key.
func (s *screenStack) submit(t *testing.T, key string, body []byte) *httptest.ResponseRecorder {
	t.Helper()
	headers := map[string]string{"Content-Type": "application/json"}
	if key != "" {
		headers["Idempotency-Key"] = key
	}
	return doRequest(t, s.handler, http.MethodPost, "/api/v1/screen-runs", headers, body)
}

// TestScreenRunSubmissionQueuesAJob checks the command boundary: a submission
// re-validates the frozen inputs, requires the idempotency key, and answers 202
// with the job that will execute the run.
func TestScreenRunSubmissionQueuesAJob(t *testing.T) {
	stack := newScreenStack(t)
	body := stack.preflightBody(stack.universe.DefinitionHash)

	if rec := stack.submit(t, "", body); rec.Code != http.StatusBadRequest {
		t.Fatalf("status without an idempotency key = %d, want 400: %s", rec.Code, rec.Body.String())
	}

	rec := stack.submit(t, "screenrun-submit-key-1", body)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("status = %d, want 202: %s", rec.Code, rec.Body.String())
	}
	var job struct {
		ID    string `json:"id"`
		Kind  string `json:"kind"`
		State string `json:"state"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &job); err != nil {
		t.Fatalf("decode job: %v", err)
	}
	if job.Kind != screenrun.KindScreenRun || job.State != string(jobs.StateQueued) {
		t.Fatalf("job = %+v, want a queued screening run", job)
	}
	if got := rec.Header().Get("Location"); got != "/api/v1/jobs/"+job.ID {
		t.Fatalf("Location = %q, want the job resource URL", got)
	}
	// The frozen configuration travels with the job, so the worker can replay
	// the exact submission.
	config, err := stack.jobs.Config(context.Background(), job.ID)
	if err != nil {
		t.Fatalf("job config: %v", err)
	}
	if !strings.Contains(string(config), stack.snapshot.ID.String()) {
		t.Fatalf("job config = %s, want the pinned snapshot", config)
	}
}

// TestScreenRunSubmissionRefusesUnusableInputs proves a submission whose inputs
// are already known to be unusable is rejected with its findings instead of
// queueing a run that would publish nothing.
func TestScreenRunSubmissionRefusesUnusableInputs(t *testing.T) {
	stack := newScreenStack(t)
	unknown := saveScreener(t, stack.handler, "screenrun-seed-unknown", `{
      "name": "unknown-dataset",
      "input_bindings": [
        {"binding_id":"px","kind":"field","dataset":"missing","field":"close"}
      ],
      "condition_tree": {"node_id":"gt","kind":"compare","input":{"binding_id":"px"},"operator":"gt","value":{"kind":"decimal","value":"10"}},
      "ranking": {"mode":"sort","fields":[{"input":{"binding_id":"px"},"direction":"desc"}]},
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
    }`, unknown.ID, unknown.Version, stack.snapshot.ID, stack.universe.ID, stack.universe.DefinitionHash))

	rec := stack.submit(t, "screenrun-submit-key-2", body)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Code   string `json:"code"`
		Issues []struct {
			Code     string `json:"code"`
			Severity string `json:"severity"`
		} `json:"issues"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	if out.Code != screenrun.CodePreflightFailed {
		t.Fatalf("code = %q, want %q", out.Code, screenrun.CodePreflightFailed)
	}
	if len(out.Issues) == 0 {
		t.Fatal("a refused submission must carry the findings that refused it")
	}
}

// seedRun records one run through the store, optionally publishing a two-member
// result. The read surface is exercised without a worker this way.
func (s *screenStack) seedRun(t *testing.T, publish bool) screening.RunRecord {
	t.Helper()
	ctx := context.Background()
	record, err := s.data.CreateScreenRun(ctx, screening.RunRequest{
		JobID: "job_synthetic",
		Config: screening.WireRunConfig{
			ScreenerRef:         domain.VersionRef{ID: domain.ID(s.screener.ID), Version: s.screener.Version},
			SnapshotID:          s.snapshot.ID,
			UniverseRef:         domain.VersionRef{ID: s.universe.ID, Version: s.universe.DefinitionHash},
			AsOf:                time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			DecisionTimezone:    "Asia/Shanghai",
			RequiredValuePolicy: "exclude_instrument",
		},
		EngineVersion:        screening.EngineVersion,
		ScoringPolicyVersion: screenrun.ScoringPolicyVersion,
		SnapshotHash:         s.snapshot.ManifestHash,
	})
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if !publish {
		return record
	}
	rows := []screening.Row{
		{
			InstrumentID: "INST_A",
			Stage:        screening.StageSelected,
			Rank:         1,
			Selected:     true,
			Values:       screening.Inputs{"px": {Kind: domain.ValueDecimal, Encoded: "10.40"}},
			Nodes: []screening.NodeEvaluation{{
				NodeID: "gt", Kind: "compare", Truth: screening.TruthTrue,
				Input: &screening.Input{BindingID: "px"},
			}},
		},
		{
			InstrumentID: "INST_B",
			Stage:        screening.StageConditionFalse,
			Values:       screening.Inputs{"px": {Kind: domain.ValueDecimal, Encoded: "4.00"}},
			Nodes: []screening.NodeEvaluation{{
				NodeID: "gt", Kind: "compare", Truth: screening.TruthFalse,
				Input: &screening.Input{BindingID: "px"},
			}},
		},
	}
	err = s.data.PublishScreenRun(ctx, record.ID, screening.RunPublishRequest{
		Summary: screening.Summary{
			Population: 2, ConditionFalse: 1, ConditionTrue: 1, Rankable: 1, Selected: 1,
		},
		Rows:        rows,
		Columns:     screening.ResultColumns([]domain.ID{"px"}, rows),
		ResultHash:  "result-hash",
		ArtifactIDs: []domain.ID{"art_1"},
	})
	if err != nil {
		t.Fatalf("publish run: %v", err)
	}
	published, err := s.data.GetScreenRun(ctx, record.ID)
	if err != nil {
		t.Fatalf("reload run: %v", err)
	}
	return published
}

func (s *screenStack) get(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	return doRequest(t, s.handler, http.MethodGet, target, nil, nil)
}

// TestScreenRunReadSurfaceServesThePublishedResult walks the read endpoints: the
// run reports its frozen config and summary, the rows page in official order
// with their columns, the state filter narrows them and the explanation carries
// the frozen per-node evidence.
func TestScreenRunReadSurfaceServesThePublishedResult(t *testing.T) {
	stack := newScreenStack(t)
	run := stack.seedRun(t, true)

	rec := stack.get(t, "/api/v1/screen-runs/"+run.ID.String())
	if rec.Code != http.StatusOK {
		t.Fatalf("get run status = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	var detail struct {
		ID     string `json:"id"`
		JobID  string `json:"job_id"`
		Config struct {
			SnapshotID string `json:"snapshot_id"`
		} `json:"config"`
		Summary *struct {
			Selected int `json:"selected"`
		} `json:"summary"`
		Artifacts []string `json:"artifact_ids"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &detail); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if detail.ID != run.ID.String() || detail.Config.SnapshotID != stack.snapshot.ID.String() {
		t.Fatalf("run = %+v, want the frozen configuration", detail)
	}
	if detail.Summary == nil || detail.Summary.Selected != 1 {
		t.Fatalf("summary = %+v, want the published rollup", detail.Summary)
	}
	if len(detail.Artifacts) != 1 {
		t.Fatalf("artifact ids = %+v, want the sealed artifact", detail.Artifacts)
	}

	list := stack.get(t, "/api/v1/screen-runs?limit=10")
	if list.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200: %s", list.Code, list.Body.String())
	}
	var page struct {
		Items []struct {
			ID string `json:"id"`
		} `json:"items"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &page); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(page.Items) != 1 || page.Items[0].ID != run.ID.String() {
		t.Fatalf("list = %+v, want the seeded run", page.Items)
	}

	rows := stack.get(t, "/api/v1/screen-runs/"+run.ID.String()+"/rows?limit=1")
	if rows.Code != http.StatusOK {
		t.Fatalf("rows status = %d, want 200: %s", rows.Code, rows.Body.String())
	}
	var rowPage struct {
		Items []struct {
			InstrumentID string                  `json:"instrument_id"`
			Selected     bool                    `json:"selected"`
			Rank         *int64                  `json:"rank"`
			Values       map[string]domain.Value `json:"values"`
		} `json:"items"`
		NextCursor *string        `json:"next_cursor"`
		Columns    []domain.Field `json:"columns"`
	}
	if err := json.Unmarshal(rows.Body.Bytes(), &rowPage); err != nil {
		t.Fatalf("decode rows: %v", err)
	}
	if len(rowPage.Items) != 1 || rowPage.Items[0].InstrumentID != "INST_A" || rowPage.Items[0].Rank == nil {
		t.Fatalf("rows = %+v, want the selected instrument with its rank", rowPage.Items)
	}
	if rowPage.Items[0].Values["px"].Encoded != "10.40" {
		t.Fatalf("row values = %+v, want the frozen display column", rowPage.Items[0].Values)
	}
	if len(rowPage.Columns) != 1 || rowPage.Columns[0].Name != "px" || rowPage.Columns[0].Type != domain.FieldDecimal {
		t.Fatalf("columns = %+v, want the derived display column", rowPage.Columns)
	}
	if rowPage.NextCursor == nil {
		t.Fatal("a bounded page of two rows must return a cursor")
	}
	// The cursor is bound to the run, its frozen result and the state filter:
	// reusing it against a differently filtered listing is rejected rather than
	// silently returning a wrong page.
	cursor := *rowPage.NextCursor
	reused := stack.get(t, "/api/v1/screen-runs/"+run.ID.String()+"/rows?limit=1&state=selected&cursor="+cursor)
	if reused.Code != http.StatusBadRequest {
		t.Fatalf("reusing a scoped cursor = %d, want 400: %s", reused.Code, reused.Body.String())
	}

	selected := stack.get(t, "/api/v1/screen-runs/"+run.ID.String()+"/rows?limit=1&state=selected")
	if selected.Code != http.StatusOK {
		t.Fatalf("selected rows status = %d, want 200: %s", selected.Code, selected.Body.String())
	}
	var selectedPage struct {
		Items []struct {
			InstrumentID string `json:"instrument_id"`
		} `json:"items"`
		NextCursor *string `json:"next_cursor"`
	}
	if err := json.Unmarshal(selected.Body.Bytes(), &selectedPage); err != nil {
		t.Fatalf("decode selected rows: %v", err)
	}
	if len(selectedPage.Items) != 1 || selectedPage.Items[0].InstrumentID != "INST_A" {
		t.Fatalf("selected rows = %+v, want only the selected instrument", selectedPage.Items)
	}
	if selectedPage.NextCursor != nil {
		t.Fatal("the only selected row ends the listing; no cursor expected")
	}

	explanation := stack.get(t, "/api/v1/screen-runs/"+run.ID.String()+"/explanations/INST_B")
	if explanation.Code != http.StatusOK {
		t.Fatalf("explanation status = %d, want 200: %s", explanation.Code, explanation.Body.String())
	}
	var evidence struct {
		Stage string `json:"stage"`
		Nodes []struct {
			NodeID string `json:"node_id"`
			Truth  string `json:"truth"`
			Input  *struct {
				BindingID string `json:"binding_id"`
			} `json:"input"`
		} `json:"nodes"`
		Score []json.RawMessage `json:"score"`
	}
	if err := json.Unmarshal(explanation.Body.Bytes(), &evidence); err != nil {
		t.Fatalf("decode explanation: %v", err)
	}
	if evidence.Stage != string(screening.StageConditionFalse) {
		t.Fatalf("stage = %q, want the exclusion stage", evidence.Stage)
	}
	if len(evidence.Nodes) != 1 || evidence.Nodes[0].Truth != string(screening.TruthFalse) || evidence.Nodes[0].Input == nil {
		t.Fatalf("nodes = %+v, want the frozen condition evidence", evidence.Nodes)
	}
	if evidence.Score == nil || len(evidence.Score) != 0 {
		t.Fatalf("score = %+v, want an empty array in sort mode", evidence.Score)
	}
}

// TestScreenRunResultIsNotReadyBeforePublish pins the publish boundary over
// HTTP: the run is addressable immediately, but its result is refused with a
// conflict until the run publishes it.
func TestScreenRunResultIsNotReadyBeforePublish(t *testing.T) {
	stack := newScreenStack(t)
	run := stack.seedRun(t, false)

	detail := stack.get(t, "/api/v1/screen-runs/"+run.ID.String())
	if detail.Code != http.StatusOK {
		t.Fatalf("unpublished run status = %d, want 200: %s", detail.Code, detail.Body.String())
	}
	var out struct {
		Summary *json.RawMessage `json:"summary"`
	}
	if err := json.Unmarshal(detail.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode run: %v", err)
	}
	if out.Summary != nil {
		t.Fatalf("summary = %s, want null before publish", *out.Summary)
	}

	for _, target := range []string{
		"/api/v1/screen-runs/" + run.ID.String() + "/rows",
		"/api/v1/screen-runs/" + run.ID.String() + "/explanations/INST_A",
	} {
		rec := stack.get(t, target)
		if rec.Code != http.StatusConflict {
			t.Fatalf("%s status = %d, want 409: %s", target, rec.Code, rec.Body.String())
		}
		if env := decodeWireError(t, rec); env.Code != screening.CodeResultNotReady {
			t.Fatalf("%s code = %q, want %q", target, env.Code, screening.CodeResultNotReady)
		}
	}
}
