package screenrun

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

// The screening-run job handler: the run row is created when the job starts,
// the frozen result is sealed and published atomically, and a run whose inputs
// cannot be resolved stays unpublished instead of exposing a partial result.

// runStack wires the handler onto the execute fixture's real store.
type runStack struct {
	*executeStack
	jobs      *jobs.Store
	artifacts *artifacts.Store
	handlers  *Handlers
}

func newRunStack(t *testing.T) *runStack {
	t.Helper()
	stack := newExecuteStack(t)
	jstore := jobs.NewStore(stack.db, stack.clock, nil)
	artifactStore := artifacts.New(t.TempDir(), stack.db)
	return &runStack{
		executeStack: stack,
		jobs:         jstore,
		artifacts:    artifactStore,
		handlers: &Handlers{
			Jobs:      jstore,
			Runs:      stack.service.data,
			Service:   stack.service,
			Artifacts: artifactStore,
		},
	}
}

func (s *runStack) submit(t *testing.T, request Request) jobs.Job {
	t.Helper()
	config, err := json.Marshal(FrozenConfigOf(request))
	if err != nil {
		t.Fatalf("freeze config: %v", err)
	}
	job, err := s.jobs.Create(context.Background(), KindScreenRun, config, "")
	if err != nil {
		t.Fatalf("create job: %v", err)
	}
	return job
}

// startLoop runs one worker loop over the handler map and stops it when the
// test ends.
func (s *runStack) startLoop(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	loop := &jobs.Loop{
		Store:    s.jobs,
		Owner:    "test-owner",
		Handlers: s.handlers.Map(),
		Lease:    5 * time.Second,
		Poll:     5 * time.Millisecond,
		Log:      discardLogger(),
	}
	done := make(chan struct{})
	go func() {
		loop.Run(ctx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
	})
}

func (s *runStack) waitState(t *testing.T, jobID string, want jobs.State) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := s.jobs.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("get job: %v", err)
		}
		if job.State == want {
			return job
		}
		if job.State.Terminal() {
			t.Fatalf("job reached %s (phase %s, error %q), want %s", job.State, job.Phase, job.Error, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("job did not reach %s (state %s, phase %s)", want, job.State, job.Phase)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestScreenRunHandlerPublishesTheFrozenResult drives the whole worker path:
// the queued job resolves the frozen inputs, computes the selection, seals the
// canonical artifact and publishes the run — then the published evidence is
// readable through the store.
func TestScreenRunHandlerPublishesTheFrozenResult(t *testing.T) {
	stack := newRunStack(t)
	stack.saveScreener(t, fieldOnlyScreener())
	version := stack.latestScreenerVersion(t)
	request := stack.request
	request.ScreenerRef = domain.VersionRef{ID: version.ID, Version: string(version.Version)}

	job := stack.submit(t, request)
	stack.startLoop(t)
	final := stack.waitState(t, job.ID, jobs.StateSucceeded)

	if len(final.ResultRefs) != 1 || final.ResultRefs[0].Kind != resultKindScreenRun {
		t.Fatalf("result refs = %+v, want the run the job produced", final.ResultRefs)
	}
	runID := domain.ID(final.ResultRefs[0].ID)
	record, err := stack.service.data.GetScreenRun(context.Background(), runID)
	if err != nil {
		t.Fatalf("get run: %v", err)
	}
	if !record.Published() || record.Summary == nil {
		t.Fatalf("run = %+v, want a published summary", record)
	}
	if record.Summary.Population != 2 || record.Summary.Selected != 1 {
		t.Fatalf("summary = %+v, want two members and one selection", record.Summary)
	}
	if record.JobID != domain.ID(job.ID) || record.EngineVersion != screening.EngineVersion {
		t.Fatalf("run = %+v, want the job link and the engine version", record)
	}
	if len(record.ArtifactIDs) != 1 || record.ResultHash == "" {
		t.Fatalf("run = %+v, want a sealed artifact and its hash", record)
	}
	if len(record.Columns) != 1 || record.Columns[0].Name != "px" {
		t.Fatalf("columns = %+v, want the derived display column", record.Columns)
	}

	rows, err := stack.service.data.ListScreenRunRows(context.Background(), runID, ports.ScreenRunRowFilter{})
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows.Items) != 2 || rows.Items[0].InstrumentID != "INST_A" || !rows.Items[0].Selected {
		t.Fatalf("rows = %+v, want INST_A selected first", rows.Items)
	}

	// The sealed artifact holds the same frozen result; it is content
	// addressed, so its checksum is the run's result hash.
	_, content, err := stack.artifacts.Open(context.Background(), record.ArtifactIDs[0].String())
	if err != nil {
		t.Fatalf("open artifact: %v", err)
	}
	defer content.Close()
	encoded, err := io.ReadAll(content)
	if err != nil {
		t.Fatalf("read artifact: %v", err)
	}
	for _, want := range []string{`"run_id":"` + runID.String() + `"`, `"selected":1`, `"INST_A"`} {
		if !strings.Contains(string(encoded), want) {
			t.Fatalf("artifact %s does not contain %s", encoded, want)
		}
	}
}

// TestScreenRunHandlerLeavesUnresolvableRunsUnpublished proves the publish
// boundary: a run whose frozen inputs can no longer be resolved fails its job
// and stays an unpublished record, never a result.
func TestScreenRunHandlerLeavesUnresolvableRunsUnpublished(t *testing.T) {
	stack := newRunStack(t)
	request := stack.request
	request.ScreenerRef = domain.VersionRef{ID: "scr_missing", Version: "v1"}

	job := stack.submit(t, request)
	stack.startLoop(t)
	stack.waitState(t, job.ID, jobs.StateFailed)

	runs, err := stack.service.data.ListScreenRuns(context.Background(), ports.ScreenRunFilter{
		JobState: string(jobs.StateFailed),
	})
	if err != nil {
		t.Fatalf("list failed runs: %v", err)
	}
	if len(runs.Items) != 1 {
		t.Fatalf("failed runs = %d, want the run whose job failed", len(runs.Items))
	}
	record := runs.Items[0]
	if record.Published() || record.Summary != nil {
		t.Fatalf("run = %+v, want nothing published", record)
	}
	rows, err := stack.service.data.ListScreenRunRows(context.Background(), record.ID, ports.ScreenRunRowFilter{})
	if err != nil {
		t.Fatalf("list rows: %v", err)
	}
	if len(rows.Items) != 0 {
		t.Fatalf("rows = %+v, want no rows for an unpublished run", rows.Items)
	}
}
