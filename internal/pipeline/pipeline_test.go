package pipeline

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/store"
	"github.com/injoyai/strategy/internal/synthetic"
)

var harnessNow = time.Date(2026, 1, 15, 12, 0, 0, 0, time.UTC)

func silentLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func mustTime(value string) time.Time {
	t, err := time.Parse(time.RFC3339, value)
	if err != nil {
		panic(err)
	}
	return t
}

func newHarness(t *testing.T) (*Handlers, *jobs.Store, *ports.FixedClock) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "metadata.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := store.Migrate(context.Background(), db, silentLogger()); err != nil {
		t.Fatalf("store.Migrate: %v", err)
	}
	clk := ports.NewFixedClock(harnessNow)
	h := &Handlers{
		Data:      data.New(db, clk),
		Jobs:      jobs.NewStore(db, clk, nil),
		Factories: map[domain.ID]ports.ProviderFactory{synthetic.ProviderID: synthetic.Factory{}},
		Clock:     clk,
	}
	return h, h.Jobs, clk
}

func startLoop(t *testing.T, h *Handlers, js *jobs.Store) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	loop := &jobs.Loop{
		Store:    js,
		Owner:    "test-owner",
		Handlers: h.Map(),
		Lease:    5 * time.Second,
		Poll:     10 * time.Millisecond,
		Log:      silentLogger(),
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

func waitState(t *testing.T, js *jobs.Store, jobID string, want jobs.State) jobs.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		job, err := js.Get(context.Background(), jobID)
		if err != nil {
			t.Fatalf("jobs.Get(%s): %v", jobID, err)
		}
		if job.State == want {
			return job
		}
		if job.State == jobs.StateSucceeded || job.State == jobs.StateFailed || job.State == jobs.StateCancelled {
			t.Fatalf("job %s reached terminal state %s (phase %s, error %q), want %s",
				jobID, job.State, job.Phase, job.Error, want)
		}
		if time.Now().After(deadline) {
			t.Fatalf("job %s did not reach %s within 5s (state %s, phase %s, error %q)",
				jobID, want, job.State, job.Phase, job.Error)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func createPipelineConn(t *testing.T, h *Handlers, settings string) domain.Connection {
	t.Helper()
	conn, err := h.Data.CreateConnection(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: synthetic.ProviderID, Version: synthetic.ProviderVersion},
		Name:     "synthetic-demo",
		Settings: json.RawMessage(settings),
	})
	if err != nil {
		t.Fatalf("CreateConnection: %v", err)
	}
	return conn
}

func ingestReq(conn domain.Connection, dataset, frequency string) domain.IngestionRequest {
	return domain.IngestionRequest{
		ConnectionRef: &domain.VersionRef{ID: conn.ID, Version: conn.Version},
		Dataset:       dataset,
		Frequency:     frequency,
		InstrumentIDs: []domain.ID{"INST_A", "INST_B"},
		Range: domain.Interval{
			From: mustTime("2026-01-01T00:00:00Z"),
			To:   mustTime("2026-02-01T00:00:00Z"),
		},
		Mode: domain.IngestBackfill,
		Mapping: []domain.Mapping{
			{SourceField: "close", TargetField: "close", SourceUnit: "CNY", TargetUnit: "CNY", Scale: "1"},
			{SourceField: "price", TargetField: "price", SourceUnit: "CNY", TargetUnit: "CNY", Scale: "1"},
		},
		Timezone:              "UTC",
		AvailabilityPolicyRef: domain.VersionRef{ID: "default-policy", Version: "v1"},
	}
}

func createPipelineJob(t *testing.T, js *jobs.Store, kind string, config any) jobs.Job {
	t.Helper()
	raw, err := json.Marshal(config)
	if err != nil {
		t.Fatalf("marshal %s config: %v", kind, err)
	}
	job, err := js.Create(context.Background(), kind, raw, "")
	if err != nil {
		t.Fatalf("jobs.Create(%s): %v", kind, err)
	}
	return job
}

func assertCodeErr(t *testing.T, err error, code, contains string) {
	t.Helper()
	if err == nil {
		t.Fatalf("want error containing %q, got nil", contains)
	}
	var derr *domain.Error
	if !errors.As(err, &derr) {
		t.Fatalf("error %v is not *domain.Error", err)
	}
	if derr.Code != code {
		t.Fatalf("error code = %q, want %q (message %q)", derr.Code, code, derr.Message)
	}
	if !strings.Contains(derr.Message, contains) {
		t.Fatalf("error message %q does not contain %q", derr.Message, contains)
	}
}

func TestMapHandlers(t *testing.T) {
	h, _, _ := newHarness(t)
	handlers := h.Map()
	want := []string{"connection.check", "snapshot.publish", "ingestion.run", "import.validate"}
	if len(handlers) != len(want) {
		t.Fatalf("Map() has %d handlers, want %d (%v)", len(handlers), len(want), want)
	}
	for _, kind := range want {
		if handlers[kind] == nil {
			t.Fatalf("Map()[%q] is nil", kind)
		}
	}
}

func TestDecodeConfig(t *testing.T) {
	var req domain.IngestionRequest
	if err := decodeConfig([]byte(`{"dataset":"bar"}`), &req); err != nil {
		t.Fatalf("decodeConfig: %v", err)
	}
	if req.Dataset != "bar" {
		t.Fatalf("dataset = %q, want %q", req.Dataset, "bar")
	}
	err := decodeConfig([]byte(`{"nope":1}`), &req)
	assertCodeErr(t, err, domain.CodeInternalError, "pipeline: decode job config")
}

func TestWrapUpstream(t *testing.T) {
	orig := domain.NewError(domain.CodeRateLimited, "synthetic: injected rate limit")
	got := wrapUpstream(orig, "pipeline: fetch %s page %d", "bar", 1)
	var derr *domain.Error
	if !errors.As(got, &derr) {
		t.Fatalf("wrapUpstream(*domain.Error) = %v, want the same *domain.Error", got)
	}
	if derr != orig {
		t.Fatalf("wrapUpstream must preserve *domain.Error verbatim, got code=%q message=%q", derr.Code, derr.Message)
	}
	plain := errors.New("boom")
	wrapped := wrapUpstream(plain, "pipeline: fetch %s page %d", "bar", 2)
	assertCodeErr(t, wrapped, domain.CodeInternalError, "pipeline: fetch bar page 2")
	if !errors.Is(wrapped, plain) {
		t.Fatalf("wrapped error must unwrap to the original cause")
	}
}

func TestCapabilityAndFrequencyGuards(t *testing.T) {
	provider, err := synthetic.Factory{}.Open(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: synthetic.ProviderID, Version: synthetic.ProviderVersion},
		Settings: json.RawMessage("{}"),
	})
	if err != nil {
		t.Fatalf("Factory.Open: %v", err)
	}
	desc, err := provider.Describe(context.Background())
	if err != nil {
		t.Fatalf("Describe: %v", err)
	}
	capability, err := capabilityOf(desc, synthetic.DatasetBar)
	if err != nil {
		t.Fatalf("capabilityOf(bar): %v", err)
	}
	if capability.Dataset != synthetic.DatasetBar {
		t.Fatalf("capability dataset = %q, want %q", capability.Dataset, synthetic.DatasetBar)
	}
	if err := requireFrequency(capability, "daily"); err != nil {
		t.Fatalf("requireFrequency(daily): %v", err)
	}
	_, err = capabilityOf(desc, "tick")
	assertCodeErr(t, err, domain.CodeValidationInvalid,
		"pipeline: provider synthetic does not serve dataset tick")
	assertCodeErr(t, requireFrequency(capability, "hourly"), domain.CodeValidationInvalid,
		"pipeline: dataset bar does not support frequency hourly")
}

func TestIngestionRunCleanEndToEnd(t *testing.T) {
	h, js, clk := newHarness(t)
	ctx := context.Background()
	conn := createPipelineConn(t, h, "{}")
	job := createPipelineJob(t, js, "ingestion.run", ingestReq(conn, synthetic.DatasetBar, "daily"))
	startLoop(t, h, js)

	done := waitState(t, js, job.ID, jobs.StateSucceeded)
	if done.Phase != "appended" {
		t.Fatalf("phase = %q, want %q", done.Phase, "appended")
	}
	if done.Done != 8 {
		t.Fatalf("done = %d, want 8 rows", done.Done)
	}
	if len(done.ResultRefs) != 1 || done.ResultRefs[0].Kind != "batch" {
		t.Fatalf("result refs = %+v, want one batch ref", done.ResultRefs)
	}
	if !strings.HasPrefix(done.ResultRefs[0].ID, "batch_") {
		t.Fatalf("batch ref id = %q, want batch_ prefix", done.ResultRefs[0].ID)
	}
	batchID := domain.ID(done.ResultRefs[0].ID)
	batch, err := h.Data.GetBatch(ctx, batchID)
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.RowCount != 8 {
		t.Fatalf("batch row count = %d, want 8", batch.RowCount)
	}
	if len(batch.Issues) != 2 {
		t.Fatalf("batch issues = %d (%+v), want 2 missing-trading-day warnings", len(batch.Issues), batch.Issues)
	}
	for _, issue := range batch.Issues {
		if issue.Code != "quality.missing_trading_day" {
			t.Fatalf("issue code = %q, want quality.missing_trading_day", issue.Code)
		}
	}

	snapJob := createPipelineJob(t, js, "snapshot.publish", domain.SnapshotRequest{
		Name:     "demo-snapshot",
		BatchIDs: []domain.ID{batchID},
	})
	snapDone := waitState(t, js, snapJob.ID, jobs.StateSucceeded)
	if len(snapDone.ResultRefs) != 1 || snapDone.ResultRefs[0].Kind != "snapshot" {
		t.Fatalf("snapshot result refs = %+v, want one snapshot ref", snapDone.ResultRefs)
	}
	if !strings.HasPrefix(snapDone.ResultRefs[0].ID, "snap_") {
		t.Fatalf("snapshot ref id = %q, want snap_ prefix", snapDone.ResultRefs[0].ID)
	}
	snapID := domain.ID(snapDone.ResultRefs[0].ID)

	view, err := h.Data.OpenView(ctx, snapID, clk.Now())
	if err != nil {
		t.Fatalf("OpenView: %v", err)
	}
	page, err := view.Query(ctx, domain.DataQuery{
		SnapshotID:    snapID,
		AsOf:          clk.Now(),
		Dataset:       synthetic.DatasetBar,
		Frequency:     "daily",
		InstrumentIDs: []domain.ID{"INST_A", "INST_B"},
		Fields:        []string{"close"},
		Range: domain.Interval{
			From: mustTime("2026-01-01T00:00:00Z"),
			To:   mustTime("2026-02-01T00:00:00Z"),
		},
	})
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if len(page.Items) != 8 || page.NextCursor != "" {
		t.Fatalf("query page = %d items (cursor %q), want 8 items with empty cursor", len(page.Items), page.NextCursor)
	}
	first := page.Items[0]
	if first.InstrumentID == nil || *first.InstrumentID != "INST_A" {
		t.Fatalf("first item instrument = %v, want INST_A", first.InstrumentID)
	}
	if !first.EventTime.Equal(mustTime("2026-01-05T15:00:00Z")) {
		t.Fatalf("first item event_time = %s, want 2026-01-05T15:00:00Z", first.EventTime)
	}
	if got := first.Values["close"].Encoded; got != "10.40" {
		t.Fatalf("first item close = %q, want %q", got, "10.40")
	}
	if first.Provenance.PublishedAt == nil || !first.Provenance.PublishedAt.Equal(first.Provenance.AvailableAt) {
		t.Fatalf("published_at = %v, want conservative policy value %s", first.Provenance.PublishedAt, first.Provenance.AvailableAt)
	}

	strictJob := createPipelineJob(t, js, "snapshot.publish", domain.SnapshotRequest{
		Name:      "strict-demo-snapshot",
		BatchIDs:  []domain.ID{batchID},
		StrictPIT: true,
	})
	waitState(t, js, strictJob.ID, jobs.StateSucceeded)
}
