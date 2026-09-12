package pipeline

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/synthetic"
)

func waitFailed(t *testing.T, js *jobs.Store, jobID, contains string) jobs.Job {
	t.Helper()
	job := waitState(t, js, jobID, jobs.StateFailed)
	if !strings.Contains(job.Error, contains) {
		t.Fatalf("job %s error = %q, want containing %q", jobID, job.Error, contains)
	}
	return job
}

func TestIngestionRunBadDataBlocksSnapshot(t *testing.T) {
	h, js, _ := newHarness(t)
	ctx := context.Background()
	conn := createPipelineConn(t, h, `{"include_bad_data":true}`)
	job := createPipelineJob(t, js, "ingestion.run", ingestReq(conn, synthetic.DatasetBar, "daily"))
	startLoop(t, h, js)

	done := waitState(t, js, job.ID, jobs.StateSucceeded)
	if len(done.ResultRefs) != 1 || done.ResultRefs[0].Kind != "batch" {
		t.Fatalf("result refs = %+v, want one batch ref", done.ResultRefs)
	}
	batch, err := h.Data.GetBatch(ctx, domain.ID(done.ResultRefs[0].ID))
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.RowCount != 13 || len(batch.Issues) != 12 {
		t.Fatalf("batch rows = %d issues = %d, want 13 rows and 12 issues", batch.RowCount, len(batch.Issues))
	}

	snapJob := createPipelineJob(t, js, "snapshot.publish", domain.SnapshotRequest{
		Name:     "blocked-snapshot",
		BatchIDs: []domain.ID{batch.ID},
	})
	waitFailed(t, js, snapJob.ID, "blocked by 9 error-severity quality issue(s); publishing is fail-closed")
}

func TestIngestionRunConnectionVersionMismatch(t *testing.T) {
	h, js, _ := newHarness(t)
	conn := createPipelineConn(t, h, "{}")
	child, err := h.Data.CreateConnection(context.Background(), domain.ConnectionConfig{
		Provider: domain.VersionRef{ID: synthetic.ProviderID, Version: synthetic.ProviderVersion},
		Name:     "synthetic-demo-v2",
		Settings: json.RawMessage("{}"),
		ParentID: conn.ID.String(),
	})
	if err != nil {
		t.Fatalf("CreateConnection(child): %v", err)
	}
	if child.Version != "2" {
		t.Fatalf("child version = %q, want 2", child.Version)
	}
	job := createPipelineJob(t, js, "ingestion.run", ingestReq(conn, synthetic.DatasetBar, "daily"))
	startLoop(t, h, js)
	waitFailed(t, js, job.ID, "is at version 2, job pinned 1")
}

func TestIngestionRunRejectedRequests(t *testing.T) {
	h, js, _ := newHarness(t)
	conn := createPipelineConn(t, h, "{}")
	startLoop(t, h, js)

	t.Run("import rejected", func(t *testing.T) {
		req := ingestReq(conn, synthetic.DatasetBar, "daily")
		req.ConnectionRef = nil
		req.ImportID = "import_1"
		job := createPipelineJob(t, js, "ingestion.run", req)
		waitFailed(t, js, job.ID,
			"pipeline: file import is not supported; ingest from a connection_ref instead")
	})

	t.Run("unknown dataset", func(t *testing.T) {
		job := createPipelineJob(t, js, "ingestion.run", ingestReq(conn, "tick", "daily"))
		waitFailed(t, js, job.ID,
			"pipeline: provider synthetic does not serve dataset tick")
	})

	t.Run("unsupported frequency", func(t *testing.T) {
		job := createPipelineJob(t, js, "ingestion.run", ingestReq(conn, synthetic.DatasetBar, "hourly"))
		waitFailed(t, js, job.ID,
			"pipeline: dataset bar does not support frequency hourly")
	})

	t.Run("unknown connection", func(t *testing.T) {
		req := ingestReq(conn, synthetic.DatasetBar, "daily")
		req.ConnectionRef = &domain.VersionRef{ID: "conn_missing", Version: "1"}
		job := createPipelineJob(t, js, "ingestion.run", req)
		waitFailed(t, js, job.ID, "data: connection conn_missing not found")
	})

	t.Run("decode error", func(t *testing.T) {
		job := createPipelineJob(t, js, "ingestion.run", json.RawMessage(`{"nope":1}`))
		waitFailed(t, js, job.ID, "pipeline: decode job config")
	})
}

func TestIngestionRunFaultGate(t *testing.T) {
	h, js, _ := newHarness(t)
	conn := createPipelineConn(t, h, `{"faults":{"fail_first_n_fetches":1}}`)
	job := createPipelineJob(t, js, "ingestion.run", ingestReq(conn, synthetic.DatasetBar, "daily"))
	startLoop(t, h, js)

	done := waitState(t, js, job.ID, jobs.StateFailed)
	const want = "synthetic: injected upstream failure 1 of 1"
	if done.Error != want {
		t.Fatalf("job error = %q, want exact %q", done.Error, want)
	}
	if len(done.ResultRefs) != 0 {
		t.Fatalf("result refs = %+v, want empty", done.ResultRefs)
	}
}

func TestSnapshotPublishUnknownBatch(t *testing.T) {
	h, js, _ := newHarness(t)
	job := createPipelineJob(t, js, "snapshot.publish", domain.SnapshotRequest{
		Name:     "ghost",
		BatchIDs: []domain.ID{"batch_missing"},
	})
	startLoop(t, h, js)
	waitFailed(t, js, job.ID, "snapshot: batch batch_missing not found")
}

func TestConnectionCheckLifecycle(t *testing.T) {
	h, js, _ := newHarness(t)
	conn := createPipelineConn(t, h, "{}")
	startLoop(t, h, js)

	t.Run("happy", func(t *testing.T) {
		job := createPipelineJob(t, js, "connection.check", connectionCheckConfig{
			ConnectionID:      conn.ID,
			ConnectionVersion: conn.Version,
		})
		done := waitState(t, js, job.ID, jobs.StateSucceeded)
		if done.Phase != "checked" {
			t.Fatalf("phase = %q, want %q", done.Phase, "checked")
		}
		if done.Done != 0 {
			t.Fatalf("done = %d, want 0 issues for clean connection", done.Done)
		}
	})

	t.Run("unknown connection", func(t *testing.T) {
		job := createPipelineJob(t, js, "connection.check", connectionCheckConfig{
			ConnectionID: "conn_missing",
		})
		waitFailed(t, js, job.ID, "data: connection conn_missing not found")
	})
}

func TestIngestionRunInstrumentDataset(t *testing.T) {
	h, js, _ := newHarness(t)
	ctx := context.Background()
	conn := createPipelineConn(t, h, "{}")
	job := createPipelineJob(t, js, "ingestion.run", ingestReq(conn, synthetic.DatasetInstrument, "static"))
	startLoop(t, h, js)

	done := waitState(t, js, job.ID, jobs.StateSucceeded)
	if done.Phase != "appended" || done.Done != 2 {
		t.Fatalf("phase = %q done = %d, want appended/2", done.Phase, done.Done)
	}
	if len(done.ResultRefs) != 1 || done.ResultRefs[0].Kind != "batch" {
		t.Fatalf("result refs = %+v, want one batch ref", done.ResultRefs)
	}
	batch, err := h.Data.GetBatch(ctx, domain.ID(done.ResultRefs[0].ID))
	if err != nil {
		t.Fatalf("GetBatch: %v", err)
	}
	if batch.RowCount != 2 || len(batch.Issues) != 0 {
		t.Fatalf("batch rows = %d issues = %d, want 2 rows and 0 issues", batch.RowCount, len(batch.Issues))
	}
}
