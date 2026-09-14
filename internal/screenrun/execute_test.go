package screenrun

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
	"github.com/injoyai/strategy/internal/store"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// executeStack is a real store with one snapshot, one static universe and one
// field-only screener version, so the whole run path is exercised end to end
// without any factor complexity.
type executeStack struct {
	service *Service
	request Request
}

func barObservation(inst, close string, at time.Time) domain.Observation {
	published := at
	instrument := domain.ID(inst)
	return domain.Observation{
		InstrumentID: &instrument,
		Dataset:      "bar",
		EventTime:    at.Add(-30 * time.Minute),
		Values:       map[string]domain.Value{"close": {Kind: domain.ValueDecimal, Encoded: close}},
		Provenance: domain.Provenance{
			SourceID: "synthetic", SourceRecordID: "bar-" + inst, RevisionID: "rev-001",
			AvailableAt: at, IngestedAt: at, PublishedAt: &published, SchemaVersion: "bar/v1",
		},
	}
}

func newExecuteStack(t *testing.T) *executeStack {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	if err := store.Migrate(ctx, db, discardLogger()); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	dataStore := data.New(db, ports.NewFixedClock(time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)))
	at := time.Date(2026, 1, 5, 15, 30, 0, 0, time.UTC)
	receipt, err := dataStore.Append(ctx, ports.BatchInput{
		JobID:        "job-1",
		Dataset:      "bar",
		Frequency:    "daily",
		Observations: []domain.Observation{barObservation("INST_A", "10.40", at), barObservation("INST_B", "4.00", at)},
		Declaration: &domain.DatasetDeclaration{
			Frequency:             "daily",
			AvailabilityPolicyRef: domain.VersionRef{ID: "availability", Version: "v1"},
			Fields:                []domain.DatasetField{{Name: "close", Unit: "cny"}},
		},
	})
	if err != nil {
		t.Fatalf("append batch: %v", err)
	}
	snapshot, err := dataStore.PublishSnapshot(ctx, domain.SnapshotRequest{Name: "snap", BatchIDs: []domain.ID{receipt.BatchID}})
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
	registry, err := factor.NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		t.Fatalf("new registry: %v", err)
	}
	service, err := New(dataStore, registry, factor.NewCache())
	if err != nil {
		t.Fatalf("build service: %v", err)
	}
	return &executeStack{
		service: service,
		request: Request{
			ScreenerRef:         domain.VersionRef{ID: "placeholder", Version: "v1"},
			SnapshotID:          snapshot.ID,
			UniverseRef:         domain.VersionRef{ID: universe.ID, Version: universe.DefinitionHash},
			AsOf:                time.Date(2026, 1, 15, 0, 0, 0, 0, time.UTC),
			DecisionTimezone:    "Asia/Shanghai",
			RequiredValuePolicy: string(screening.PolicyExcludeInstrument),
		},
	}
}

// fieldOnlyScreener keeps the close-above-threshold rule as a field binding, so
// the run reads its input from the snapshot instead of a factor.
func fieldOnlyScreener() screening.Definition {
	return screening.Definition{
		Name:          "close-above",
		InputBindings: []screening.InputBinding{{BindingID: "px", Kind: screening.BindingField, Dataset: "bar", Field: "close"}},
		ConditionTree: screening.Compare{
			NodeID:   "gt",
			Input:    screening.Input{BindingID: "px"},
			Operator: screening.OpGt,
			Value:    domain.Value{Kind: domain.ValueDecimal, Encoded: "10"},
		},
		Ranking:   screening.Ranking{Mode: screening.RankingSort, Fields: []screening.RankField{{Input: screening.Input{BindingID: "px"}, Direction: screening.DirectionDesc}}},
		Selection: screening.Selection{Mode: screening.SelectionTopN, N: 1},
	}
}

func (s *executeStack) saveScreener(t *testing.T, def screening.Definition) {
	t.Helper()
	if _, err := s.service.data.CreateScreenerVersion(context.Background(), screening.VersionRequest{Name: def.Name, Definition: def}); err != nil {
		t.Fatalf("save screener: %v", err)
	}
}

// TestExecuteSelectsThroughTheFrozenInputs drives the whole run path: resolve
// the pinned versions, read each member's field value from the snapshot, run
// the condition tree, rank and select — then check the mutually exclusive stage
// counts conserve.
func TestExecuteSelectsThroughTheFrozenInputs(t *testing.T) {
	stack := newExecuteStack(t)
	stack.saveScreener(t, fieldOnlyScreener())
	version := stack.latestScreenerVersion(t)
	stack.request.ScreenerRef = domain.VersionRef{ID: version.ID, Version: string(version.Version)}

	result, err := stack.service.Execute(context.Background(), stack.request)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if result.Summary.Population != 2 || result.Summary.ConditionTrue != 1 || result.Summary.ConditionFalse != 1 {
		t.Fatalf("summary = %+v, want 2 members with one passing", result.Summary)
	}
	if result.Summary.Selected != 1 || result.Summary.NotSelected != 0 || result.Summary.Rankable != 1 {
		t.Fatalf("summary = %+v, want exactly one selected", result.Summary)
	}
	// Conservation: population = false + unknown + true and true = insufficient + rankable.
	if result.Summary.ConditionFalse+result.Summary.ConditionUnknown+result.Summary.ConditionTrue != result.Summary.Population {
		t.Fatalf("stage counts do not conserve: %+v", result.Summary)
	}
	if result.Summary.RankInsufficient+result.Summary.Rankable != result.Summary.ConditionTrue {
		t.Fatalf("rankable counts do not conserve: %+v", result.Summary)
	}
	if result.Summary.Selected+result.Summary.NotSelected != result.Summary.Rankable {
		t.Fatalf("selection counts do not conserve: %+v", result.Summary)
	}
	if len(result.Rows) != 2 {
		t.Fatalf("rows = %d, want one per member", len(result.Rows))
	}
	if result.Rows[0].InstrumentID != "INST_A" || result.Rows[0].Stage != screening.StageSelected || result.Rows[0].Rank != 1 {
		t.Fatalf("first row = %+v, want INST_A selected at rank 1", result.Rows[0])
	}
	if result.Rows[1].InstrumentID != "INST_B" || result.Rows[1].Stage != screening.StageConditionFalse {
		t.Fatalf("second row = %+v, want INST_B excluded by the condition", result.Rows[1])
	}
	if result.Rows[0].Values["px"].Encoded != "10.40" {
		t.Fatalf("row input = %+v, want the snapshot's close value", result.Rows[0].Values["px"])
	}
}

// TestExecuteRefusesToPublishOnUnresolvableInputs pins the fail-closed
// submission rule: an unusable input is a refusal, never a plausible-looking
// result.
func TestExecuteRefusesToPublishOnUnresolvableInputs(t *testing.T) {
	stack := newExecuteStack(t)
	broken := fieldOnlyScreener()
	broken.Name = "missing-field"
	broken.InputBindings[0].Field = "eps"
	broken.ConditionTree = screening.Compare{
		NodeID:   "gt",
		Input:    screening.Input{BindingID: "px"},
		Operator: screening.OpGt,
		Value:    domain.Value{Kind: domain.ValueDecimal, Encoded: "10"},
	}
	stack.saveScreener(t, broken)
	version := stack.latestScreenerVersion(t)
	stack.request.ScreenerRef = domain.VersionRef{ID: version.ID, Version: string(version.Version)}

	_, err := stack.service.Execute(context.Background(), stack.request)
	if err == nil {
		t.Fatal("a run whose field does not exist must be refused")
	}
	if code := domain.ErrorCode(err); code != codePreflightFailed {
		t.Fatalf("code = %q, want %q", code, codePreflightFailed)
	}
}

func (s *executeStack) latestScreenerVersion(t *testing.T) screening.Version {
	t.Helper()
	page, err := s.service.data.ListScreenerVersions(context.Background(), ports.ScreenerFilter{Sort: "-id", Limit: 1})
	if err != nil {
		t.Fatalf("list screeners: %v", err)
	}
	if len(page.Items) != 1 {
		t.Fatalf("screeners = %d, want 1", len(page.Items))
	}
	return page.Items[0]
}
