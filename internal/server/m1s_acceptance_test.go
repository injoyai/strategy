package server

import (
	"net/http"
	"strings"
	"testing"

	"github.com/injoyai/strategy/internal/screenrun"
	"github.com/injoyai/strategy/internal/synthetic"
)

// M1S vertical slice: ingest a snapshot through the real provider pipeline, save
// immutable screener revisions, preflight them, submit runs to the worker, read
// the published results, explain instruments and save a selection as a static
// pool — all over HTTP against the same assembly the binary mounts.
//
// Running the whole slice rather than the endpoints in isolation is the point:
// the catalog the preflight reads is the one ingestion declared, the factor a
// run binds is the registered one, and the result the API serves is the one the
// worker published.

type acceptanceScreener struct {
	ID      string `json:"id"`
	Version string `json:"version"`
	Name    string `json:"name"`
}

type acceptanceScreenRun struct {
	ID     string `json:"id"`
	JobID  string `json:"job_id"`
	Config struct {
		AsOf                string `json:"as_of"`
		DecisionTimezone    string `json:"decision_timezone"`
		RequiredValuePolicy string `json:"required_value_policy"`
	} `json:"config"`
	EngineVersion        string `json:"engine_version"`
	ScoringPolicyVersion string `json:"scoring_policy_version"`
	SnapshotHash         string `json:"snapshot_hash"`
	Summary              *struct {
		Population       int     `json:"population"`
		ConditionFalse   int     `json:"condition_false"`
		ConditionUnknown int     `json:"condition_unknown"`
		ConditionTrue    int     `json:"condition_true"`
		RankInsufficient int     `json:"rank_insufficient"`
		Rankable         int     `json:"rankable"`
		Selected         int     `json:"selected"`
		NotSelected      int     `json:"not_selected"`
		EmptyReason      *string `json:"empty_reason"`
	} `json:"summary"`
	ArtifactIDs []string `json:"artifact_ids"`
}

type acceptanceScreenRow struct {
	InstrumentID string                     `json:"instrument_id"`
	Selected     bool                       `json:"selected"`
	Rank         *int64                     `json:"rank"`
	Score        *string                    `json:"score"`
	Values       map[string]acceptanceValue `json:"values"`
	Reason       string                     `json:"reason"`
}

type acceptanceScreenRowPage struct {
	Items   []acceptanceScreenRow `json:"items"`
	Columns []struct {
		Name string `json:"name"`
		Type string `json:"type"`
		Unit string `json:"unit"`
	} `json:"columns"`
	NextCursor *string `json:"next_cursor"`
}

type acceptanceScreenExplanation struct {
	Stage string `json:"stage"`
	Nodes struct {
		NodeID    string           `json:"node_id"`
		Truth     string           `json:"truth"`
		Threshold *acceptanceValue `json:"threshold"`
	} `json:"nodes"`
	Score []struct {
		BindingID    string          `json:"binding_id"`
		RawValue     acceptanceValue `json:"raw_value"`
		Percentile   *string         `json:"percentile"`
		Weight       string          `json:"weight"`
		Contribution string          `json:"contribution"`
	} `json:"score"`
}

type acceptancePreflight struct {
	Valid    bool              `json:"valid"`
	Issues   []acceptanceIssue `json:"issues"`
	Coverage []struct {
		BindingID string  `json:"binding_id"`
		Available bool    `json:"available"`
		Reason    *string `json:"reason"`
	} `json:"coverage"`
	EstimatedRows *int `json:"estimated_rows"`
}

type acceptanceUniverse struct {
	ID             string `json:"id"`
	Name           string `json:"name"`
	SnapshotID     string `json:"snapshot_id"`
	DefinitionHash string `json:"definition_hash"`
	Definition     struct {
		Kind    string   `json:"kind"`
		Members []string `json:"members"`
	} `json:"definition"`
	Source *struct {
		ScreenRunID   string   `json:"screen_run_id"`
		AsOf          string   `json:"as_of"`
		SnapshotHash  string   `json:"snapshot_hash"`
		QualityLimits []string `json:"quality_limits"`
	} `json:"source"`
}

const stateSucceeded = "succeeded"

// TestM1SScreeningVerticalSliceAcceptance drives the screening loop end to end.
func TestM1SScreeningVerticalSliceAcceptance(t *testing.T) {
	s := bootAcceptance(t)
	snapshot := s.ingestSnapshot(t, "m1s-snapshot")
	mixedPool := s.savePool(t, "m1s-mixed-pool", snapshot.ID, "m1s-pool-mixed", "INST_A", "INST_B")
	singlePool := s.savePool(t, "m1s-single-pool", snapshot.ID, "m1s-pool-single", "INST_A")

	fieldScreener := s.saveScreener(t, "m1s-cheap-pool", "m1s-screener-field", map[string]any{
		"input_bindings": []map[string]any{
			{"binding_id": "px", "kind": "field", "dataset": synthetic.DatasetBar, "field": "close"},
		},
		"condition_tree":  compareCondition("cheap", "px", "lt", "15"),
		"ranking":         map[string]any{"mode": "sort", "fields": []map[string]any{{"input": map[string]any{"binding_id": "px"}, "direction": "desc"}}},
		"selection":       map[string]any{"mode": "all"},
		"display_columns": []string{"px"},
	})
	factorScreener := s.saveScreener(t, "m1s-cheap-momentum", "m1s-screener-factor", map[string]any{
		"input_bindings": []map[string]any{
			{"binding_id": "px", "kind": "field", "dataset": synthetic.DatasetBar, "field": "close"},
			{"binding_id": "mom", "kind": "factor", "factor_ref": map[string]any{"id": "momentum", "version": "1.0.0"}, "params": map[string]any{"n": 1}},
		},
		"condition_tree": compareCondition("cheap", "px", "lt", "15"),
		"ranking": map[string]any{
			"mode":       "score",
			"components": []map[string]any{{"input": map[string]any{"binding_id": "mom"}, "weight": "1", "direction": "larger_is_better"}},
		},
		"selection":       map[string]any{"mode": "all"},
		"display_columns": []string{"px", "mom"},
	})

	// 1. A pool whose members cannot all be computed at the decision time is
	// reported before anything runs: the factor engine's window is pool-wide, and
	// INST_B stops trading two days before it starts.
	thin := s.runRequest(factorScreener, snapshot, mixedPool)
	preflight := s.preflight(t, thin)
	if preflight.Valid {
		t.Fatalf("preflight = %+v, want invalid: INST_B has no points inside the factor window", preflight)
	}
	if !hasAcceptanceIssue(preflight.Issues, "insufficient_history", "error", "INST_B") {
		t.Fatalf("issues = %+v, want an error-severity insufficient_history naming INST_B", preflight.Issues)
	}
	for _, coverage := range preflight.Coverage {
		if coverage.BindingID == "mom" && (coverage.Available || coverage.Reason == nil || *coverage.Reason != "insufficient_history") {
			t.Fatalf("coverage = %+v, want the factor binding unavailable with its reason", coverage)
		}
	}

	// 2. The field-only revision resolves over the same pool: everything the run
	// needs is readable, so it is valid.
	fieldRun := s.runRequest(fieldScreener, snapshot, mixedPool)
	preflight = s.preflight(t, fieldRun)
	if !preflight.Valid {
		t.Fatalf("preflight = %+v, want valid for a field-only screener", preflight)
	}
	if preflight.EstimatedRows == nil || *preflight.EstimatedRows != 2 {
		t.Fatalf("estimated_rows = %v, want the two pool members", preflight.EstimatedRows)
	}

	// 3. Submit it and read the published result.
	first := s.submitScreenRun(t, fieldRun, "m1s-run-field")
	if first.Summary == nil {
		t.Fatal("published run has no summary")
	}
	// The stages conserve: 母池 = false + unknown + true, then
	// true = rank-insufficient + rankable, rankable = selected + not selected.
	got := *first.Summary
	if got.Population != 2 || got.ConditionFalse != 1 || got.ConditionUnknown != 0 || got.ConditionTrue != 1 {
		t.Fatalf("summary = %+v, want 2 members with one condition true", got)
	}
	if got.RankInsufficient != 0 || got.Rankable != 1 || got.Selected != 1 || got.NotSelected != 0 {
		t.Fatalf("summary = %+v, want exactly one selected and none left unselected", got)
	}
	if got.EmptyReason != nil {
		t.Fatalf("empty_reason = %v, want null for a selection that found members", *got.EmptyReason)
	}
	if first.EngineVersion == "" || first.ScoringPolicyVersion == "" || len(first.ArtifactIDs) != 1 {
		t.Fatalf("run = %+v, want the frozen versions and a sealed artifact", first)
	}
	if first.SnapshotHash != snapshot.ManifestHash || first.Config.RequiredValuePolicy != "exclude_instrument" {
		t.Fatalf("run = %+v, want the frozen snapshot hash and policy", first)
	}
	if first.Config.AsOf != "2026-01-15T00:00:00Z" || first.Config.DecisionTimezone != "Asia/Shanghai" {
		t.Fatalf("run config = %+v, want the submitted decision time and zone", first.Config)
	}

	// 4. The rows are the frozen selection in official rank order.
	rows := s.screenRows(t, first.ID, "")
	if len(rows.Items) != 2 {
		t.Fatalf("rows = %+v, want both pool members", rows.Items)
	}
	if row := rows.Items[0]; row.InstrumentID != "INST_A" || !row.Selected || row.Rank == nil || *row.Rank != 1 {
		t.Fatalf("first row = %+v, want INST_A selected with rank 1", rows.Items[0])
	}
	if rows.Items[0].Values["px"].Value != "11.40" {
		t.Fatalf("row values = %+v, want the last visible close", rows.Items[0].Values)
	}
	if row := rows.Items[1]; row.InstrumentID != "INST_B" || row.Selected || row.Rank != nil || row.Reason != "condition_false" {
		t.Fatalf("second row = %+v, want INST_B excluded without a rank", rows.Items[1])
	}
	if len(rows.Columns) != 1 || rows.Columns[0].Name != "px" || rows.Columns[0].Type != "decimal" {
		t.Fatalf("columns = %+v, want the display column described", rows.Columns)
	}

	// The state filter narrows the listing without touching the result.
	if filtered := s.screenRows(t, first.ID, "state=selected"); len(filtered.Items) != 1 || filtered.Items[0].InstrumentID != "INST_A" {
		t.Fatalf("selected rows = %+v, want only INST_A", filtered.Items)
	}
	if filtered := s.screenRows(t, first.ID, "state=excluded"); len(filtered.Items) != 1 || filtered.Items[0].InstrumentID != "INST_B" {
		t.Fatalf("excluded rows = %+v, want only INST_B", filtered.Items)
	}

	// 5. Explanations carry the frozen per-node evidence for both stages.
	if explanation := s.screenExplanation(t, first.ID, "INST_A"); explanation.Stage != "selected" || explanation.Nodes.Truth != "true" {
		t.Fatalf("explanation = %+v, want a true root at the selected stage", explanation)
	}
	if explanation := s.screenExplanation(t, first.ID, "INST_B"); explanation.Stage != "condition_false" || explanation.Nodes.Truth != "false" {
		t.Fatalf("explanation = %+v, want a false root for the excluded instrument", explanation)
	}

	// 6. Save the complete selection as a static pool, with its source evidence.
	code, _, raw := s.call(http.MethodPost, "/screen-runs/"+first.ID+"/universe", map[string]any{"name": "m1s-saved-pool"}, "m1s-save-pool")
	if code != http.StatusCreated {
		t.Fatalf("POST /screen-runs/{id}/universe: status %d body %s", code, raw)
	}
	pool := decodeBody[acceptanceUniverse](t, "saved pool", raw)
	if pool.Definition.Kind != "static" || len(pool.Definition.Members) != 1 || pool.Definition.Members[0] != "INST_A" {
		t.Fatalf("pool = %+v, want exactly the run's selection", pool.Definition)
	}
	if pool.DefinitionHash == "" || pool.SnapshotID != snapshot.ID {
		t.Fatalf("pool = %+v, want the run's snapshot and its definition hash", pool)
	}
	if pool.Source == nil || pool.Source.ScreenRunID != first.ID || pool.Source.SnapshotHash != snapshot.ManifestHash {
		t.Fatalf("pool source = %+v, want the run and the data hash it selected against", pool.Source)
	}

	// 7. Resubmitting the same frozen inputs publishes a second run with the same
	// selection — a new revision, never an overwrite of the first.
	second := s.submitScreenRun(t, fieldRun, "m1s-run-field-again")
	if second.ID == first.ID {
		t.Fatal("a second submission must create a new run, not republish the first")
	}
	secondRows := s.screenRows(t, second.ID, "")
	if len(secondRows.Items) != len(rows.Items) {
		t.Fatalf("second run rows = %+v, want the same result", secondRows.Items)
	}
	for i := range rows.Items {
		before, after := rows.Items[i], secondRows.Items[i]
		if before.InstrumentID != after.InstrumentID || before.Reason != after.Reason || before.Selected != after.Selected {
			t.Fatalf("row %d = %+v then %+v, want identical classification", i, before, after)
		}
		if (before.Rank == nil) != (after.Rank == nil) || (before.Rank != nil && *before.Rank != *after.Rank) {
			t.Fatalf("row %d rank = %v then %v, want identical ranks", i, before.Rank, after.Rank)
		}
	}
	if reloaded := s.screenRun(t, first.ID); reloaded.Summary == nil || reloaded.Summary.Selected != 1 {
		t.Fatalf("first run = %+v, want its published summary unchanged", reloaded.Summary)
	}

	// 8. A factor binding works end to end once the pool can be computed: the
	// single-member pool gives the momentum window two points, so the score-mode
	// ranking produces a rank and a score component.
	factorRun := s.runRequest(factorScreener, snapshot, singlePool)
	if preflight := s.preflight(t, factorRun); !preflight.Valid {
		t.Fatalf("preflight = %+v, want valid for a computable pool", preflight)
	}
	scored := s.submitScreenRun(t, factorRun, "m1s-run-factor")
	if scored.Summary == nil || scored.Summary.Selected != 1 || scored.Summary.Population != 1 {
		t.Fatalf("summary = %+v, want the single member selected", scored.Summary)
	}
	scoredRows := s.screenRows(t, scored.ID, "")
	if len(scoredRows.Items) != 1 {
		t.Fatalf("rows = %+v, want the single member", scoredRows.Items)
	}
	if scoredRows.Items[0].Score == nil || *scoredRows.Items[0].Score == "" {
		t.Fatalf("row = %+v, want the score of score-mode ranking", scoredRows.Items[0])
	}
	if scoredRows.Items[0].Values["mom"].Value == nil {
		t.Fatalf("row values = %+v, want the factor value of the display column", scoredRows.Items[0].Values)
	}
	explanation := s.screenExplanation(t, scored.ID, "INST_A")
	if len(explanation.Score) != 1 || explanation.Score[0].Weight != "1" || explanation.Score[0].Percentile == nil {
		t.Fatalf("score evidence = %+v, want the declared weight and its percentile", explanation.Score)
	}
	// One rankable member is the documented m=1 case: the average-rank percentile
	// is 0.5, never 0 or 1.
	if *explanation.Score[0].Percentile != "0.5" {
		t.Fatalf("percentile = %q, want the m=1 rule (0.5)", *explanation.Score[0].Percentile)
	}
}

// ingestSnapshot runs the provider pipeline and freezes the result, returning
// the published snapshot.
func (s *acceptanceStack) ingestSnapshot(t *testing.T, name string) acceptanceSnapshot {
	t.Helper()
	conn := s.createConnection("synthetic", `{}`, "m1s-conn")
	ingestion := s.startIngestion(conn.ID, conn.Version, "m1s-ingest")
	ingestion = s.waitJob(ingestion.ID, stateSucceeded)
	if len(ingestion.ResultRefs) != 1 || ingestion.ResultRefs[0].Kind != "batch" {
		t.Fatalf("ingestion result refs = %+v, want the batch it appended", ingestion.ResultRefs)
	}
	code, _, raw := s.call(http.MethodPost, "/snapshots", map[string]any{
		"name":       name,
		"batch_ids":  []string{ingestion.ResultRefs[0].ID},
		"strict_pit": false,
	}, "m1s-snapshot")
	if code != http.StatusAccepted {
		t.Fatalf("POST /snapshots: status %d body %s", code, raw)
	}
	job := s.waitJob(decodeBody[acceptanceJob](t, "snapshot job", raw).ID, stateSucceeded)
	if len(job.ResultRefs) != 1 || job.ResultRefs[0].Kind != "snapshot" {
		t.Fatalf("snapshot result refs = %+v, want the published snapshot", job.ResultRefs)
	}
	code, _, raw = s.call(http.MethodGet, "/snapshots/"+job.ResultRefs[0].ID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /snapshots/{id}: status %d body %s", code, raw)
	}
	return decodeBody[acceptanceSnapshot](t, "snapshot", raw)
}

// savePool freezes one static mother pool over the snapshot.
func (s *acceptanceStack) savePool(t *testing.T, name, snapshotID, idem string, members ...string) acceptanceUniverse {
	t.Helper()
	code, _, raw := s.call(http.MethodPost, "/universes", map[string]any{
		"name":        name,
		"snapshot_id": snapshotID,
		"definition":  map[string]any{"kind": "static", "members": members},
	}, idem)
	if code != http.StatusCreated {
		t.Fatalf("POST /universes: status %d body %s", code, raw)
	}
	return decodeBody[acceptanceUniverse](t, "universe", raw)
}

// saveScreener freezes one immutable revision and returns its address.
func (s *acceptanceStack) saveScreener(t *testing.T, name, idem string, body map[string]any) acceptanceScreener {
	t.Helper()
	body["name"] = name
	code, _, raw := s.call(http.MethodPost, "/screeners", body, idem)
	if code != http.StatusCreated {
		t.Fatalf("POST /screeners: status %d body %s", code, raw)
	}
	screener := decodeBody[acceptanceScreener](t, "screener", raw)
	if screener.Version != "v1" {
		t.Fatalf("screener version = %q, want v1", screener.Version)
	}
	return screener
}

// runRequest freezes one prospective run against a revision, a snapshot and a
// pool. The universe's version pin is its definition hash, which is what keeps
// a run from drifting away from the membership it selected.
func (s *acceptanceStack) runRequest(screener acceptanceScreener, snapshot acceptanceSnapshot, pool acceptanceUniverse) map[string]any {
	return map[string]any{
		"screener_ref":          map[string]any{"id": screener.ID, "version": screener.Version},
		"snapshot_id":           snapshot.ID,
		"universe_ref":          map[string]any{"id": pool.ID, "version": pool.DefinitionHash},
		"as_of":                 "2026-01-15T00:00:00Z",
		"decision_timezone":     "Asia/Shanghai",
		"strict_pit":            false,
		"required_value_policy": "exclude_instrument",
	}
}

func (s *acceptanceStack) preflight(t *testing.T, body map[string]any) acceptancePreflight {
	t.Helper()
	code, _, raw := s.call(http.MethodPost, "/screen-runs/preflight", body, "")
	if code != http.StatusOK {
		t.Fatalf("POST /screen-runs/preflight: status %d body %s", code, raw)
	}
	return decodeBody[acceptancePreflight](t, "preflight", raw)
}

// submitScreenRun posts a run, waits for its job and returns the published run.
func (s *acceptanceStack) submitScreenRun(t *testing.T, body map[string]any, idem string) acceptanceScreenRun {
	t.Helper()
	code, hdr, raw := s.call(http.MethodPost, "/screen-runs", body, idem)
	if code != http.StatusAccepted {
		t.Fatalf("POST /screen-runs: status %d body %s", code, raw)
	}
	job := decodeBody[acceptanceJob](t, "screen run job", raw)
	if job.Kind != screenrun.KindScreenRun {
		t.Fatalf("job kind = %q, want %q", job.Kind, screenrun.KindScreenRun)
	}
	if hdr.Get("Location") != "/api/v1/jobs/"+job.ID {
		t.Fatalf("Location = %q, want the job resource URL", hdr.Get("Location"))
	}
	finished := s.waitJob(job.ID, stateSucceeded)
	if len(finished.ResultRefs) != 1 || finished.ResultRefs[0].Kind != "screen_run" {
		t.Fatalf("result refs = %+v, want the run the job published", finished.ResultRefs)
	}
	return s.screenRun(t, finished.ResultRefs[0].ID)
}

func (s *acceptanceStack) screenRun(t *testing.T, runID string) acceptanceScreenRun {
	t.Helper()
	code, _, raw := s.call(http.MethodGet, "/screen-runs/"+runID, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET /screen-runs/%s: status %d body %s", runID, code, raw)
	}
	return decodeBody[acceptanceScreenRun](t, "screen run", raw)
}

func (s *acceptanceStack) screenRows(t *testing.T, runID, query string) acceptanceScreenRowPage {
	t.Helper()
	path := "/screen-runs/" + runID + "/rows?limit=50"
	if query != "" {
		path += "&" + query
	}
	code, _, raw := s.call(http.MethodGet, path, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", path, code, raw)
	}
	return decodeBody[acceptanceScreenRowPage](t, "screen rows", raw)
}

func (s *acceptanceStack) screenExplanation(t *testing.T, runID, instrumentID string) acceptanceScreenExplanation {
	t.Helper()
	path := "/screen-runs/" + runID + "/explanations/" + instrumentID
	code, _, raw := s.call(http.MethodGet, path, nil, "")
	if code != http.StatusOK {
		t.Fatalf("GET %s: status %d body %s", path, code, raw)
	}
	return decodeBody[acceptanceScreenExplanation](t, "screen explanation", raw)
}

func compareCondition(nodeID, bindingID, operator, value string) map[string]any {
	return map[string]any{
		"node_id":  nodeID,
		"kind":     "compare",
		"input":    map[string]any{"binding_id": bindingID},
		"operator": operator,
		"value":    map[string]any{"kind": "decimal", "value": value, "missing_reason": nil},
	}
}

func hasAcceptanceIssue(issues []acceptanceIssue, code, severity, contains string) bool {
	for _, issue := range issues {
		if issue.Code == code && issue.Severity == severity && strings.Contains(issue.Message, contains) {
			return true
		}
	}
	return false
}
