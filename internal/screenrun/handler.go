// Screening-run job execution: the application half of M1S S2. A run is
// created when its job starts, computed over the frozen inputs, sealed into a
// content-addressed artifact and published atomically — the frozen rows become
// readable only once the whole result exists, so an interrupted or retried run
// never leaves a half product that looks like a result.
package screenrun

import (
	"bytes"
	"context"
	"encoding/json"

	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/screening"
)

// KindScreenRun is the job kind of one submitted screening run.
const KindScreenRun = "screen.run"

const (
	// The sealed result artifact: one canonical JSON document holding the
	// summary, the display-column descriptors and every frozen row. Storing it
	// content-addressed means the result hash identifies the exact bytes a
	// published run reports, so a cursor or a download can be tied to them.
	resultArtifactName      = "screen-run"
	resultArtifactMediaType = "application/json"
	// resultKindScreenRun is the job result-ref kind naming the run a job
	// produced.
	resultKindScreenRun = "screen_run"
)

// Handlers executes queued screening runs. Every dependency is required: a
// partially wired handler would silently fail jobs instead of refusing them.
type Handlers struct {
	Jobs      *jobs.Store
	Runs      *data.Store
	Service   *Service
	Artifacts *artifacts.Store
}

// Map exposes the handler under its job kind for the worker loop.
func (h *Handlers) Map() map[string]jobs.HandlerFunc {
	return map[string]jobs.HandlerFunc{KindScreenRun: h.ScreenRun}
}

// runArtifact is the canonical published document of one run. Derived values
// are deliberately excluded: everything here is either an input, a hash of an
// input, or a computed result, so the document alone identifies what was run
// and what came out.
type runArtifact struct {
	RunID                string            `json:"run_id"`
	JobID                string            `json:"job_id"`
	EngineVersion        string            `json:"engine_version"`
	ScoringPolicyVersion string            `json:"scoring_policy_version"`
	ConfigHash           string            `json:"config_hash"`
	SnapshotHash         string            `json:"snapshot_hash"`
	Summary              screening.Summary `json:"summary"`
	Columns              []domain.Field    `json:"columns"`
	Rows                 []screening.Row   `json:"rows"`
}

// ScreenRun executes one claimed screen.run job.
func (h *Handlers) ScreenRun(ctx context.Context, task *jobs.Task) error {
	if h.Jobs == nil || h.Runs == nil || h.Service == nil || h.Artifacts == nil {
		return domain.NewError(domain.CodeInternalError, "screenrun: run handler is not fully wired")
	}
	frozen, err := h.frozenConfig(ctx, task.JobID())
	if err != nil {
		return err
	}
	req := RequestOfFrozen(frozen)
	// The snapshot hash is frozen onto the run row: it names the data the run
	// actually read, independent of what the snapshot resource reports later.
	snapshot, err := h.Runs.GetSnapshot(ctx, req.SnapshotID)
	if err != nil {
		return err
	}
	run, err := h.Runs.CreateScreenRun(ctx, screening.RunRequest{
		JobID:                domain.ID(task.JobID()),
		Config:               frozen,
		EngineVersion:        screening.EngineVersion,
		ScoringPolicyVersion: ScoringPolicyVersion,
		SnapshotHash:         snapshot.ManifestHash,
	})
	if err != nil {
		return err
	}
	if err := task.Progress("computing", 0, nil); err != nil {
		return err
	}
	result, err := h.Service.Execute(ctx, req)
	if err != nil {
		return err
	}
	sealed, err := h.seal(ctx, run, *result)
	if err != nil {
		return err
	}
	if err := h.Runs.PublishScreenRun(ctx, run.ID, screening.RunPublishRequest{
		Summary:     result.Summary,
		Rows:        result.Rows,
		Columns:     result.Columns,
		ResultHash:  sealed.Checksum,
		ArtifactIDs: []domain.ID{domain.ID(sealed.ID)},
	}); err != nil {
		return err
	}
	if err := task.Progress("published", int64(result.Summary.Selected), nil); err != nil {
		return err
	}
	task.SetResultRefs([]jobs.ResultRef{{Kind: resultKindScreenRun, ID: run.ID.String()}})
	return nil
}

// frozenConfig recovers the exact request the job was submitted with. A retried
// job replays its original inputs even if the referenced resources moved on.
func (h *Handlers) frozenConfig(ctx context.Context, jobID string) (screening.WireRunConfig, error) {
	config, err := h.Jobs.Config(ctx, jobID)
	if err != nil {
		return screening.WireRunConfig{}, err
	}
	var frozen screening.WireRunConfig
	dec := json.NewDecoder(bytes.NewReader(config))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&frozen); err != nil {
		return screening.WireRunConfig{}, domain.Wrap(err, domain.CodeInternalError,
			"screenrun: decode frozen run config")
	}
	return frozen, nil
}

// seal stores the canonical result document through the content-addressed
// artifact store and returns the recorded artifact. Identical bytes always
// resolve to the same storage key, so a re-executed job cannot mint duplicates.
func (h *Handlers) seal(ctx context.Context, run screening.RunRecord, result Result) (artifacts.Artifact, error) {
	encoded, err := json.Marshal(runArtifact{
		RunID:                run.ID.String(),
		JobID:                run.JobID.String(),
		EngineVersion:        run.EngineVersion,
		ScoringPolicyVersion: run.ScoringPolicyVersion,
		ConfigHash:           run.ConfigHash,
		SnapshotHash:         run.SnapshotHash,
		Summary:              result.Summary,
		Columns:              nonNilColumns(result.Columns),
		Rows:                 nonNilRows(result.Rows),
	})
	if err != nil {
		return artifacts.Artifact{}, domain.Wrap(err, domain.CodeInternalError, "screenrun: encode result artifact")
	}
	placement, err := h.Artifacts.Ingest(bytes.NewReader(encoded))
	if err != nil {
		return artifacts.Artifact{}, domain.Wrap(err, domain.CodeInternalError, "screenrun: store result artifact")
	}
	stored, err := h.Artifacts.Record(ctx, artifacts.RecordInput{
		Name:      resultArtifactName,
		MediaType: resultArtifactMediaType,
		Placement: placement,
	})
	if err != nil {
		return artifacts.Artifact{}, domain.Wrap(err, domain.CodeInternalError, "screenrun: record result artifact")
	}
	return stored, nil
}

func nonNilColumns(columns []domain.Field) []domain.Field {
	if columns == nil {
		return []domain.Field{}
	}
	return columns
}

func nonNilRows(rows []screening.Row) []screening.Row {
	if rows == nil {
		return []screening.Row{}
	}
	return rows
}
