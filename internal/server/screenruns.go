package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/jobs"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
	"github.com/injoyai/strategy/internal/screenrun"
)

// The M1S screening-run surface. Preflight is a read-only check that persists
// nothing, so it carries /data/query's read-only POST treatment: no
// Idempotency-Key, and replaying it can never change an answer. Submitting a
// run is a command: it creates a job, so it requires the Idempotency-Key and
// answers 202 with the job's Location.

// rowColumnMissingReason explains a display column that has no value for one
// instrument. The engine reports absence by omitting the entry, so the reason
// is filled in here rather than invented per binding.
const rowColumnMissingReason = "missing_value"

func (a *API) RegisterScreenRuns() {
	a.Handle(http.MethodPost, "/screen-runs", a.startScreenRun, RouteOptions{IdempotencyRequired: true})
	a.Handle(http.MethodGet, "/screen-runs", a.listScreenRuns, RouteOptions{})
	a.Handle(http.MethodGet, "/screen-runs/{id}", a.getScreenRun, RouteOptions{})
	a.Handle(http.MethodGet, "/screen-runs/{id}/rows", a.listScreenRows, RouteOptions{})
	a.Handle(http.MethodGet, "/screen-runs/{id}/explanations/{instrument_id}", a.getScreenExplanation, RouteOptions{})
	a.Handle(http.MethodPost, "/screen-runs/preflight", a.preflightScreenRun, RouteOptions{})
}

// requireScreenRuns guards the surface: it is only mounted when the screening
// run service is supplied.
func (a *API) requireScreenRuns(w http.ResponseWriter, r *http.Request) bool {
	if a.screenRuns != nil {
		return true
	}
	a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "screening runs are not mounted on this API"))
	return false
}

// requireScreenRunJobs guards the command side: a run is executed by a job, so
// without a job store the submission cannot be recorded.
func (a *API) requireScreenRunJobs(w http.ResponseWriter, r *http.Request) bool {
	if a.jobs != nil && a.data != nil {
		return true
	}
	a.writeError(w, r, domain.NewError(domain.CodeInternalUnavailable, "the job store is not mounted on this API"))
	return false
}

type screenCoverageWire struct {
	BindingID string  `json:"binding_id"`
	Available bool    `json:"available"`
	Reason    *string `json:"reason"`
}

type screenPreflightWire struct {
	Valid             bool                 `json:"valid"`
	Issues            []domain.Issue       `json:"issues"`
	Coverage          []screenCoverageWire `json:"coverage"`
	EstimatedScanRows *int                 `json:"estimated_scan_rows"`
	EstimatedRows     *int                 `json:"estimated_rows"`
}

func (a *API) preflightScreenRun(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenRuns(w, r) {
		return
	}
	var req screenrun.Request
	if !DecodeJSON(w, r, &req) {
		return
	}
	result, err := a.screenRuns.Preflight(r.Context(), req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := screenPreflightWire{
		Valid:             result.Valid,
		Issues:            result.Issues,
		Coverage:          make([]screenCoverageWire, 0, len(result.Coverage)),
		EstimatedScanRows: result.EstimatedScanRows,
		EstimatedRows:     result.EstimatedRows,
	}
	if out.Issues == nil {
		out.Issues = []domain.Issue{}
	}
	for _, coverage := range result.Coverage {
		out.Coverage = append(out.Coverage, screenCoverageWire{
			BindingID: coverage.BindingID.String(),
			Available: coverage.Available,
			Reason:    coverage.Reason,
		})
	}
	WriteJSON(w, http.StatusOK, out)
}

// startScreenRun answers POST /screen-runs: it re-resolves the frozen inputs
// and refuses to queue a run whose inputs are already known to be unusable
// (422 with the findings), otherwise it creates the run's job and answers 202
// with the job's Location. The run row itself is created by the worker when it
// starts, so a queued job never exposes a run that has not begun.
func (a *API) startScreenRun(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenRuns(w, r) || !a.requireScreenRunJobs(w, r) {
		return
	}
	var req screenrun.Request
	if !DecodeJSON(w, r, &req) {
		return
	}
	result, err := a.screenRuns.Preflight(r.Context(), req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	if !result.Valid {
		issues := result.Issues
		if issues == nil {
			issues = []domain.Issue{}
		}
		a.writeErrorEnvelope(w, r, http.StatusUnprocessableEntity, errorEnvelope{
			Code:      screenrun.CodePreflightFailed,
			Message:   "the frozen inputs cannot be evaluated: " + screenrun.SummarizeIssues(issues),
			RequestID: RequestIDFrom(r.Context()),
			Issues:    issues,
		})
		return
	}
	config, err := json.Marshal(screenrun.FrozenConfigOf(req))
	if err != nil {
		a.writeError(w, r, domain.Wrap(err, domain.CodeInternalError, "encode screening run config"))
		return
	}
	job, err := a.jobs.Create(r.Context(), screenrun.KindScreenRun, config, "")
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteAccepted(w, r, job.ID, job)
}

// screenSummaryWire mirrors the ScreenSummary schema. empty_reason is null when
// the run selected something: there is nothing to explain.
type screenSummaryWire struct {
	Population       int     `json:"population"`
	ConditionFalse   int     `json:"condition_false"`
	ConditionUnknown int     `json:"condition_unknown"`
	ConditionTrue    int     `json:"condition_true"`
	RankInsufficient int     `json:"rank_insufficient"`
	Rankable         int     `json:"rankable"`
	Selected         int     `json:"selected"`
	NotSelected      int     `json:"not_selected"`
	EmptyReason      *string `json:"empty_reason"`
}

type screenRunWire struct {
	ID                   string                  `json:"id"`
	JobID                string                  `json:"job_id"`
	Config               screening.WireRunConfig `json:"config"`
	EngineVersion        string                  `json:"engine_version"`
	ScoringPolicyVersion string                  `json:"scoring_policy_version"`
	ConfigHash           string                  `json:"config_hash"`
	SnapshotHash         string                  `json:"snapshot_hash"`
	CreatedAt            time.Time               `json:"created_at"`
	Summary              *screenSummaryWire      `json:"summary"`
	ArtifactIDs          []string                `json:"artifact_ids"`
}

type screenRunPage struct {
	Items      []screenRunWire `json:"items"`
	NextCursor *string         `json:"next_cursor"`
}

func screenRunWireOf(record screening.RunRecord) screenRunWire {
	out := screenRunWire{
		ID:                   record.ID.String(),
		JobID:                record.JobID.String(),
		Config:               record.Config,
		EngineVersion:        record.EngineVersion,
		ScoringPolicyVersion: record.ScoringPolicyVersion,
		ConfigHash:           record.ConfigHash,
		SnapshotHash:         record.SnapshotHash,
		CreatedAt:            record.CreatedAt.UTC(),
		ArtifactIDs:          make([]string, 0, len(record.ArtifactIDs)),
	}
	for _, id := range record.ArtifactIDs {
		out.ArtifactIDs = append(out.ArtifactIDs, id.String())
	}
	if record.Summary != nil {
		summary := record.Summary
		wire := screenSummaryWire{
			Population:       summary.Population,
			ConditionFalse:   summary.ConditionFalse,
			ConditionUnknown: summary.ConditionUnknown,
			ConditionTrue:    summary.ConditionTrue,
			RankInsufficient: summary.RankInsufficient,
			Rankable:         summary.Rankable,
			Selected:         summary.Selected,
			NotSelected:      summary.NotSelected,
		}
		if summary.EmptyReason != "" {
			reason := summary.EmptyReason
			wire.EmptyReason = &reason
		}
		out.Summary = &wire
	}
	return out
}

// listScreenRuns answers GET /screen-runs. The state filter narrows by the
// run's job state; the run itself carries no state.
func (a *API) listScreenRuns(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenRuns(w, r) || !a.requireScreenRunJobs(w, r) {
		return
	}
	page, ok := ParsePage(w, r)
	if !ok {
		return
	}
	cursor, ok := ParseCursor(w, r, page.Cursor, page.Sort, a.clock.Now())
	if !ok {
		return
	}
	filter := ports.ScreenRunFilter{
		Sort:    page.Sort,
		AfterID: cursor.LastID,
		Limit:   page.Limit,
	}
	if raw := r.URL.Query().Get("screener_id"); raw != "" {
		id, err := domain.ParseID(raw)
		if err != nil {
			a.writeError(w, r, err)
			return
		}
		filter.ScreenerID = id.String()
	}
	if raw := r.URL.Query().Get("state"); raw != "" {
		if !validJobState(raw) {
			writeBoundaryError(w, r, domain.CodeValidationInvalid,
				"state must be one of: queued, running, cancel_requested, succeeded, failed, cancelled")
			return
		}
		filter.JobState = raw
	}
	res, err := a.data.ListScreenRuns(r.Context(), filter)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := screenRunPage{Items: make([]screenRunWire, 0, len(res.Items))}
	for _, record := range res.Items {
		out.Items = append(out.Items, screenRunWireOf(record))
	}
	if res.NextCursor != "" {
		next := EncodeCursor(page.Sort, res.NextCursor, a.clock.Now())
		out.NextCursor = &next
	}
	WriteJSON(w, http.StatusOK, out)
}

// validJobState reports whether raw names a job state of the contract enum.
func validJobState(raw string) bool {
	switch jobs.State(raw) {
	case jobs.StateQueued, jobs.StateRunning, jobs.StateCancelRequested,
		jobs.StateSucceeded, jobs.StateFailed, jobs.StateCancelled:
		return true
	default:
		return false
	}
}

func (a *API) getScreenRun(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenRuns(w, r) || !a.requireScreenRunJobs(w, r) {
		return
	}
	record, err := a.data.GetScreenRun(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, screenRunWireOf(record))
}

type screenRowWire struct {
	InstrumentID string                  `json:"instrument_id"`
	Symbol       *string                 `json:"symbol"`
	Name         *string                 `json:"name"`
	Selected     bool                    `json:"selected"`
	Rank         *int64                  `json:"rank"`
	Score        *domain.Decimal         `json:"score"`
	Values       map[string]domain.Value `json:"values"`
	Reason       string                  `json:"reason"`
	QualityFlags []string                `json:"quality_flags"`
}

type screenRowPage struct {
	Items      []screenRowWire `json:"items"`
	NextCursor *string         `json:"next_cursor"`
	Columns    []domain.Field  `json:"columns"`
}

// screenRowWireOf projects one frozen row onto the contract shape. symbol and
// name stay null: the platform holds no instrument reference data yet, and a
// fabricated label would be worse than an explicit absence.
func screenRowWireOf(record screening.RunRecord, row screening.Row) screenRowWire {
	out := screenRowWire{
		InstrumentID: row.InstrumentID.String(),
		Selected:     row.Selected,
		Values:       make(map[string]domain.Value, len(record.Columns)),
		Reason:       string(row.Stage),
		QualityFlags: []string{},
	}
	for _, column := range record.Columns {
		if value, ok := row.Values[domain.ID(column.Name)]; ok {
			out.Values[column.Name] = value
			continue
		}
		// The column has no value for this instrument. Its kind is stated when
		// the column's type is known and left empty when it is not: an unknown
		// type has no kind to report, and guessing one would fabricate evidence.
		out.Values[column.Name] = domain.Value{
			Kind:          screening.ValueKindOfFieldType(column.Type),
			MissingReason: rowColumnMissingReason,
		}
	}
	if row.Rank > 0 {
		// Ranks are 1-based, so only a rankable row carries one; an excluded
		// row has no official rank and reports null.
		rank := row.Rank
		out.Rank = &rank
	}
	if row.HasScore {
		score := row.Score
		out.Score = &score
	}
	return out
}

// listScreenRows answers GET /screen-runs/{id}/rows. Rows are readable only
// after the run publishes; before that the caller is told the result is not
// ready rather than shown an empty page.
func (a *API) listScreenRows(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenRuns(w, r) || !a.requireScreenRunJobs(w, r) {
		return
	}
	record, ok := a.publishedScreenRun(w, r)
	if !ok {
		return
	}
	page, ok := ParsePage(w, r)
	if !ok {
		return
	}
	var selected *bool
	state := r.URL.Query().Get("state")
	switch state {
	case "":
	case "selected":
		value := true
		selected = &value
	case "excluded":
		value := false
		selected = &value
	default:
		writeBoundaryError(w, r, domain.CodeValidationInvalid, "state must be one of: selected, excluded")
		return
	}
	// The cursor is bound to the run, the frozen result and the state filter:
	// resuming a page against a different result or filter would silently skip
	// or repeat rows.
	scope := record.ID.String() + ":" + record.ResultHash + ":" + state
	cursor, ok := ParseScopedCursor(w, r, page.Cursor, SortAsc, scope, a.clock.Now())
	if !ok {
		return
	}
	var after *int64
	if cursor.LastID != "" {
		offset, err := strconv.ParseInt(cursor.LastID, 10, 64)
		if err != nil {
			writeBoundaryError(w, r, domain.CodeValidationInvalid, "cursor is malformed")
			return
		}
		after = &offset
	}
	res, err := a.data.ListScreenRunRows(r.Context(), record.ID, ports.ScreenRunRowFilter{
		AfterOrdinal: after,
		Selected:     selected,
		Limit:        page.Limit,
	})
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := screenRowPage{
		Items:   make([]screenRowWire, 0, len(res.Items)),
		Columns: record.Columns,
	}
	if out.Columns == nil {
		out.Columns = []domain.Field{}
	}
	for _, row := range res.Items {
		out.Items = append(out.Items, screenRowWireOf(record, row))
	}
	if res.NextCursor != "" {
		next := EncodeScopedCursor(SortAsc, scope, res.NextCursor, a.clock.Now())
		out.NextCursor = &next
	}
	WriteJSON(w, http.StatusOK, out)
}

// publishedScreenRun loads a run and refuses to serve its rows before it
// published them.
func (a *API) publishedScreenRun(w http.ResponseWriter, r *http.Request) (screening.RunRecord, bool) {
	record, err := a.data.GetScreenRun(r.Context(), domain.ID(r.PathValue("id")))
	if err != nil {
		a.writeError(w, r, err)
		return screening.RunRecord{}, false
	}
	if !record.Published() {
		a.writeError(w, r, domain.NewError(screening.CodeResultNotReady,
			"run %s has not published its result yet; follow job %s", record.ID, record.JobID))
		return screening.RunRecord{}, false
	}
	return record, true
}

type screenNodeWire struct {
	NodeID        string           `json:"node_id"`
	Truth         string           `json:"truth"`
	Input         *screenInputWire `json:"input"`
	Threshold     *domain.Value    `json:"threshold"`
	MissingReason *string          `json:"missing_reason"`
	// Per-node data provenance is not recorded in the frozen evidence yet, so
	// it is reported as an explicit absence instead of a plausible value.
	DataTime   *time.Time       `json:"data_time"`
	RevisionID *string          `json:"revision_id"`
	Children   []screenNodeWire `json:"children"`
}

type screenInputWire struct {
	BindingID string `json:"binding_id"`
}

type screenExplanationWire struct {
	RunID        string                    `json:"run_id"`
	InstrumentID string                    `json:"instrument_id"`
	Stage        string                    `json:"stage"`
	Nodes        []screenNodeWire          `json:"nodes"`
	Score        []screening.ScoreEvidence `json:"score"`
}

// getScreenExplanation answers GET /screen-runs/{id}/explanations/{instrument_id}
// with the frozen per-condition evidence and score components of one row.
func (a *API) getScreenExplanation(w http.ResponseWriter, r *http.Request) {
	if !a.requireScreenRuns(w, r) || !a.requireScreenRunJobs(w, r) {
		return
	}
	record, ok := a.publishedScreenRun(w, r)
	if !ok {
		return
	}
	instrumentID, err := domain.ParseID(r.PathValue("instrument_id"))
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	row, err := a.data.GetScreenRunRow(r.Context(), record.ID, instrumentID)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	out := screenExplanationWire{
		RunID:        record.ID.String(),
		InstrumentID: row.InstrumentID.String(),
		Stage:        string(row.Stage),
		Nodes:        screenNodeWires(row.Nodes),
		Score:        row.ScoreDetail,
	}
	if out.Score == nil {
		out.Score = []screening.ScoreEvidence{}
	}
	WriteJSON(w, http.StatusOK, out)
}

func screenNodeWires(nodes []screening.NodeEvaluation) []screenNodeWire {
	out := make([]screenNodeWire, 0, len(nodes))
	for _, node := range nodes {
		wire := screenNodeWire{
			NodeID:    node.NodeID.String(),
			Truth:     string(node.Truth),
			Threshold: node.Threshold,
			Children:  screenNodeWires(node.Children),
		}
		if node.Input != nil {
			wire.Input = &screenInputWire{BindingID: node.Input.BindingID.String()}
		}
		if node.MissingReason != "" {
			reason := node.MissingReason
			wire.MissingReason = &reason
		}
		out = append(out, wire)
	}
	return out
}
