package data

import (
	"context"
	"strconv"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

// runConfig is the frozen configuration every run test stores.
func runConfig() screening.WireRunConfig {
	return screening.WireRunConfig{
		ScreenerRef:         domain.VersionRef{ID: "scr_1", Version: "v1"},
		SnapshotID:          "snap_1",
		UniverseRef:         domain.VersionRef{ID: "univ_1", Version: "hash_1"},
		AsOf:                mustTime("2026-01-15T00:00:00Z"),
		DecisionTimezone:    "Asia/Shanghai",
		RequiredValuePolicy: string(screening.PolicyExcludeInstrument),
	}
}

func createRun(t *testing.T, store *Store, jobID domain.ID) screening.RunRecord {
	t.Helper()
	record, err := store.CreateScreenRun(context.Background(), screening.RunRequest{
		JobID:                jobID,
		Config:               runConfig(),
		EngineVersion:        screening.EngineVersion,
		ScoringPolicyVersion: "scoring-policy/1",
		SnapshotHash:         "manifest-hash",
	})
	if err != nil {
		t.Fatalf("create screening run: %v", err)
	}
	return record
}

// runResult is a two-member result: INST_A passes the condition and ranks
// first, INST_B fails it.
func runResult() screening.RunPublishRequest {
	rows := []screening.Row{
		{
			InstrumentID: "INST_A",
			Stage:        screening.StageSelected,
			Rank:         1,
			Score:        domain.Decimal("0.75"),
			HasScore:     true,
			Selected:     true,
			Values:       screening.Inputs{"px": {Kind: domain.ValueDecimal, Encoded: "10.40"}},
			Nodes: []screening.NodeEvaluation{{
				NodeID: "gt", Kind: "compare", Truth: screening.TruthTrue,
				Input: &screening.Input{BindingID: "px"},
			}},
			ScoreDetail: []screening.ScoreEvidence{{
				BindingID:    "px",
				RawValue:     domain.Value{Kind: domain.ValueDecimal, Encoded: "10.40"},
				Percentile:   domain.Decimal("0.75"),
				Weight:       domain.Decimal("1"),
				Contribution: domain.Decimal("0.75"),
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
	return screening.RunPublishRequest{
		Summary: screening.Summary{
			Population: 2, ConditionFalse: 1, ConditionTrue: 1, Rankable: 1, Selected: 1,
		},
		Rows:        rows,
		Columns:     screening.ResultColumns([]domain.ID{"px"}, rows),
		ResultHash:  "result-hash",
		ArtifactIDs: []domain.ID{"art_1"},
	}
}

// TestScreenRunPublishIsAtomicAndImmutable walks the whole persistence path: a
// run exists unpublished the moment it is created, publishing writes the
// summary, the rows and the result hash together, and a second publish is a
// conflict rather than an overwrite.
func TestScreenRunPublishIsAtomicAndImmutable(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	record := createRun(t, store, "job_1")

	if record.Published() || record.Summary != nil {
		t.Fatalf("a fresh run = %+v, want unpublished with no summary", record)
	}
	if record.ConfigHash == "" || record.Config.ScreenerRef.ID != "scr_1" {
		t.Fatalf("run = %+v, want the frozen config and its hash", record)
	}
	if loaded, err := store.GetScreenRun(ctx, record.ID); err != nil || loaded.Published() {
		t.Fatalf("get unpublished run = %+v, %v", loaded, err)
	}

	request := runResult()
	if err := store.PublishScreenRun(ctx, record.ID, request); err != nil {
		t.Fatalf("publish: %v", err)
	}
	published, err := store.GetScreenRun(ctx, record.ID)
	if err != nil {
		t.Fatalf("get published run: %v", err)
	}
	if !published.Published() {
		t.Fatal("run is still unpublished after a successful publish")
	}
	if published.ResultHash != request.ResultHash {
		t.Fatalf("result hash = %q, want %q", published.ResultHash, request.ResultHash)
	}
	if published.Summary == nil || published.Summary.Selected != 1 {
		t.Fatalf("summary = %+v, want the published rollup", published.Summary)
	}
	if len(published.ArtifactIDs) != 1 || published.ArtifactIDs[0] != "art_1" {
		t.Fatalf("artifact ids = %+v, want the sealed artifact", published.ArtifactIDs)
	}
	if len(published.Columns) != 1 || published.Columns[0].Name != "px" || published.Columns[0].Type != domain.FieldDecimal {
		t.Fatalf("columns = %+v, want the derived display column", published.Columns)
	}

	// A published result is evidence: republishing must not overwrite it.
	err = store.PublishScreenRun(ctx, record.ID, request)
	if code := domain.ErrorCode(err); code != domain.CodeResourceConflict {
		t.Fatalf("second publish code = %q (%v), want %q", code, err, domain.CodeResourceConflict)
	}
}

// TestScreenRunRowsPageInCanonicalOrder checks the row listing walks the
// engine's canonical order with an ordinal cursor and honours the state filter.
func TestScreenRunRowsPageInCanonicalOrder(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	record := createRun(t, store, "job_1")
	if err := store.PublishScreenRun(ctx, record.ID, runResult()); err != nil {
		t.Fatalf("publish: %v", err)
	}

	first, err := store.ListScreenRunRows(ctx, record.ID, ports.ScreenRunRowFilter{Limit: 1})
	if err != nil {
		t.Fatalf("first page: %v", err)
	}
	if len(first.Items) != 1 || first.Items[0].InstrumentID != "INST_A" || first.NextCursor != "0" {
		t.Fatalf("first page = %+v (next %q), want INST_A and a resume point", first.Items, first.NextCursor)
	}
	ordinal, err := parseOrdinal(first.NextCursor)
	if err != nil {
		t.Fatalf("resume point: %v", err)
	}
	second, err := store.ListScreenRunRows(ctx, record.ID, ports.ScreenRunRowFilter{AfterOrdinal: &ordinal, Limit: 1})
	if err != nil {
		t.Fatalf("second page: %v", err)
	}
	if len(second.Items) != 1 || second.Items[0].InstrumentID != "INST_B" || second.NextCursor != "" {
		t.Fatalf("second page = %+v (next %q), want INST_B and no more pages", second.Items, second.NextCursor)
	}
	if second.Items[0].Rank != 0 || second.Items[0].Stage != screening.StageConditionFalse {
		t.Fatalf("excluded row = %+v, want no rank and a condition_false stage", second.Items[0])
	}

	selected := true
	onlySelected, err := store.ListScreenRunRows(ctx, record.ID, ports.ScreenRunRowFilter{Selected: &selected})
	if err != nil {
		t.Fatalf("selected filter: %v", err)
	}
	if len(onlySelected.Items) != 1 || onlySelected.Items[0].InstrumentID != "INST_A" {
		t.Fatalf("selected filter = %+v, want only INST_A", onlySelected.Items)
	}

	// The explanation reads the same frozen evidence.
	row, err := store.GetScreenRunRow(ctx, record.ID, "INST_A")
	if err != nil {
		t.Fatalf("explanation row: %v", err)
	}
	if len(row.Nodes) != 1 || row.Nodes[0].Truth != screening.TruthTrue {
		t.Fatalf("nodes = %+v, want the frozen condition evidence", row.Nodes)
	}
	if len(row.ScoreDetail) != 1 || row.ScoreDetail[0].Weight != domain.Decimal("1") {
		t.Fatalf("score detail = %+v, want the frozen score components", row.ScoreDetail)
	}
	if _, err := store.GetScreenRunRow(ctx, record.ID, "INST_MISSING"); domain.ErrorCode(err) != domain.CodeResourceNotFound {
		t.Fatalf("unknown instrument error = %v, want a not-found code", err)
	}
}

// TestScreenRunRefusesANonConservingSummary pins the publish invariant: stage
// counts that do not describe the rows they claim to summarise are refused by
// the store, and the refusal leaves the run unpublished.
func TestScreenRunRefusesANonConservingSummary(t *testing.T) {
	store, _ := newTestStore(t)
	ctx := context.Background()
	record := createRun(t, store, "job_1")

	request := runResult()
	request.Summary.Population = 3 // two rows cannot populate three members
	err := store.PublishScreenRun(ctx, record.ID, request)
	if code := domain.ErrorCode(err); code != domain.CodeInternalError {
		t.Fatalf("code = %q (%v), want %q", code, err, domain.CodeInternalError)
	}
	loaded, err := store.GetScreenRun(ctx, record.ID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if loaded.Published() {
		t.Fatal("a refused publish must leave the run unpublished")
	}
	rows, err := store.ListScreenRunRows(ctx, record.ID, ports.ScreenRunRowFilter{})
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows.Items) != 0 {
		t.Fatalf("rows = %+v, want nothing written by a refused publish", rows.Items)
	}
}

// TestScreenRunListFiltersByJobState proves the listing's state filter joins
// the run's job, so runs can be separated by what their job is doing.
func TestScreenRunListFiltersByJobState(t *testing.T) {
	store, clock := newTestStore(t)
	ctx := context.Background()
	jobStore := jobs.NewStore(store.db, clock, nil)
	job, err := jobStore.Create(ctx, "screen.run", []byte(`{}`), "")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	createRun(t, store, domain.ID(job.ID))

	queued, err := store.ListScreenRuns(ctx, ports.ScreenRunFilter{JobState: string(jobs.StateQueued)})
	if err != nil {
		t.Fatalf("list queued: %v", err)
	}
	if len(queued.Items) != 1 {
		t.Fatalf("queued runs = %d, want the run whose job is queued", len(queued.Items))
	}
	succeeded, err := store.ListScreenRuns(ctx, ports.ScreenRunFilter{JobState: string(jobs.StateSucceeded)})
	if err != nil {
		t.Fatalf("list succeeded: %v", err)
	}
	if len(succeeded.Items) != 0 {
		t.Fatalf("succeeded runs = %d, want none", len(succeeded.Items))
	}
	byScreener, err := store.ListScreenRuns(ctx, ports.ScreenRunFilter{ScreenerID: "scr_1", Sort: "-id"})
	if err != nil {
		t.Fatalf("list by screener: %v", err)
	}
	if len(byScreener.Items) != 1 {
		t.Fatalf("screener runs = %d, want one", len(byScreener.Items))
	}
	unfiltered, err := store.ListScreenRuns(ctx, ports.ScreenRunFilter{ScreenerID: "scr_other"})
	if err != nil {
		t.Fatalf("list unrelated screener: %v", err)
	}
	if len(unfiltered.Items) != 0 {
		t.Fatalf("other screener runs = %d, want none", len(unfiltered.Items))
	}
}

// parseOrdinal reads the decimal resume point a row page returns.
func parseOrdinal(cursor string) (int64, error) {
	return strconv.ParseInt(cursor, 10, 64)
}
