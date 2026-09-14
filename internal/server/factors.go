package server

import (
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/analysis"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/research"
)

// The M1-09 factor surface: the registered capability catalog, one
// synchronous preflight and run per request, and the synchronous
// factor-evidence analysis whose canonical artifact lands in the
// content-addressed artifact store (downloadable through the existing
// /artifacts endpoints).

func (a *API) RegisterFactors() {
	a.Handle(http.MethodGet, "/factors", a.listFactors, RouteOptions{})
	a.Handle(http.MethodGet, "/factors/{id}", a.getFactor, RouteOptions{})
	// Pure computations, no persisted state: replaying them cannot change
	// the answer, so they share /data/query's read-only POST treatment.
	a.Handle(http.MethodPost, "/factor-runs/preflight", a.preflightFactorRun, RouteOptions{})
	a.Handle(http.MethodPost, "/factor-runs", a.runFactor, RouteOptions{})
	// The analysis persists an artifact, so it takes the Idempotency-Key.
	a.Handle(http.MethodPost, "/factor-analyses", a.runFactorAnalysis, RouteOptions{IdempotencyRequired: true})
}

type factorParamWire struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Default  any      `json:"default,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

type factorInputWire struct {
	Name                string `json:"name"`
	Dataset             string `json:"dataset"`
	Field               string `json:"field"`
	Frequency           string `json:"frequency"`
	Lookback            int    `json:"lookback"`
	Unit                string `json:"unit"`
	PIT                 bool   `json:"pit"`
	MaxStalenessSeconds *int64 `json:"max_staleness_seconds,omitempty"`
}

type factorWire struct {
	ID           string              `json:"id"`
	Version      string              `json:"version"`
	Title        string              `json:"title"`
	Kind         string              `json:"kind"`
	Params       []factorParamWire   `json:"params"`
	Inputs       []factorInputWire   `json:"inputs"`
	Dependencies []domain.VersionRef `json:"dependencies"`
	OutputUnit   string              `json:"output_unit"`
	AssetClasses []string            `json:"asset_classes"`
}

func factorWireOf(spec *factor.Spec) factorWire {
	params := make([]factorParamWire, 0, len(spec.Params))
	for _, p := range spec.Params {
		params = append(params, factorParamWire{
			Name:     p.Name,
			Type:     string(p.Type),
			Required: p.Required,
			Default:  p.Default,
			Min:      p.Min,
			Max:      p.Max,
			Enum:     p.Enum,
		})
	}
	inputs := make([]factorInputWire, 0, len(spec.Inputs))
	for _, in := range spec.Inputs {
		w := factorInputWire{
			Name:      in.Name,
			Dataset:   in.Dataset,
			Field:     in.Field,
			Frequency: in.Frequency,
			Lookback:  in.Lookback,
			Unit:      in.Unit,
			PIT:       in.PIT,
		}
		if in.Staleness > 0 {
			seconds := int64(in.Staleness.Seconds())
			w.MaxStalenessSeconds = &seconds
		}
		inputs = append(inputs, w)
	}
	deps := make([]domain.VersionRef, 0, len(spec.Deps))
	for _, dep := range spec.Deps {
		deps = append(deps, domain.VersionRef{ID: domain.ID(dep.ID), Version: dep.Version})
	}
	classes := spec.AssetClasses
	if classes == nil {
		classes = []string{}
	}
	return factorWire{
		ID:           spec.ID,
		Version:      spec.Version,
		Title:        spec.Title,
		Kind:         string(spec.Kind),
		Params:       params,
		Inputs:       inputs,
		Dependencies: deps,
		OutputUnit:   spec.OutputUnit,
		AssetClasses: classes,
	}
}

type factorPage struct {
	Items      []factorWire `json:"items"`
	NextCursor *string      `json:"next_cursor"`
}

func (a *API) listFactors(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
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
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))

	// The registry is in memory: page over the (id, version) catalog the
	// same way listProviders pages over descriptors. The composite key
	// "id@version" matches the registry's (id, version) ordering.
	specs := a.research.ListFactors()
	matched := make([]factor.Spec, 0, len(specs))
	for _, spec := range specs {
		if q != "" &&
			!strings.Contains(strings.ToLower(spec.ID), q) &&
			!strings.Contains(strings.ToLower(spec.Title), q) {
			continue
		}
		matched = append(matched, spec)
	}
	if page.Sort == SortDesc {
		for i, j := 0, len(matched)-1; i < j; i, j = i+1, j-1 {
			matched[i], matched[j] = matched[j], matched[i]
		}
	}

	items := make([]factorWire, 0, page.Limit+1)
	next := ""
	for i := range matched {
		spec := matched[i]
		key := spec.ID + "@" + spec.Version
		if cursor.LastID != "" {
			if page.Sort == SortDesc {
				if key >= cursor.LastID {
					continue
				}
			} else if key <= cursor.LastID {
				continue
			}
		}
		items = append(items, factorWireOf(&spec))
		if len(items) > page.Limit {
			items = items[:page.Limit]
			next = EncodeCursor(page.Sort, key, a.clock.Now())
			break
		}
	}
	out := factorPage{Items: items}
	if next != "" {
		out.NextCursor = &next
	}
	WriteJSON(w, http.StatusOK, out)
}

func (a *API) getFactor(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
		return
	}
	version := r.URL.Query().Get("version")
	if version == "" {
		a.writeError(w, r, domain.NewError(domain.CodeValidationInvalid, "factor: version query parameter is required"))
		return
	}
	spec, err := a.research.GetFactor(r.PathValue("id"), version)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	WriteJSON(w, http.StatusOK, factorWireOf(spec))
}

type preflightWire struct {
	Valid         bool           `json:"valid"`
	Issues        []domain.Issue `json:"issues"`
	EstimatedRows *int           `json:"estimated_rows"`
}

// problemIssues maps factor preflight problems onto the wire Issue
// schema. Problems are findings, not errors: the endpoint answers 200
// with valid=false so a caller can fix the whole request in one round.
func problemIssues(problems []factor.Problem) []domain.Issue {
	issues := make([]domain.Issue, 0, len(problems))
	for _, p := range problems {
		issues = append(issues, domain.Issue{
			Code:     p.Code,
			Path:     p.Ref.String(),
			Message:  p.Message,
			Severity: domain.SeverityError,
		})
	}
	return issues
}

func (a *API) preflightFactorRun(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
		return
	}
	var req research.FactorRunRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	problems, err := a.research.PreflightFactor(r.Context(), req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	// estimated_rows stays null: the M1 preflight reports findings, not
	// row estimates.
	WriteJSON(w, http.StatusOK, preflightWire{Valid: len(problems) == 0, Issues: problemIssues(problems)})
}

type factorMemberWire struct {
	InstrumentID  string  `json:"instrument_id"`
	Value         *string `json:"value"`
	MissingReason *string `json:"missing_reason"`
}

type factorRunResultWire struct {
	FactorRef  domain.VersionRef  `json:"factor_ref"`
	SnapshotID domain.ID          `json:"snapshot_id"`
	UniverseID domain.ID          `json:"universe_id"`
	AsOf       time.Time          `json:"as_of"`
	Covered    int                `json:"covered"`
	Total      int                `json:"total"`
	Members    []factorMemberWire `json:"members"`
}

func (a *API) runFactor(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
		return
	}
	var req research.FactorRunRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	frame, err := a.research.RunFactor(r.Context(), req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}

	// Members render in instrument-id order — the canonical frame order —
	// each carrying exactly one of value or missing reason.
	ids := make([]string, 0, len(frame.Values)+len(frame.Missing))
	for id := range frame.Values {
		ids = append(ids, string(id))
	}
	for id := range frame.Missing {
		ids = append(ids, string(id))
	}
	sort.Strings(ids)
	members := make([]factorMemberWire, 0, len(ids))
	for _, id := range ids {
		member := factorMemberWire{InstrumentID: id}
		if value, ok := frame.Values[domain.ID(id)]; ok {
			v := string(value)
			member.Value = &v
		} else {
			reason := frame.Missing[domain.ID(id)]
			member.MissingReason = &reason
		}
		members = append(members, member)
	}
	WriteJSON(w, http.StatusOK, factorRunResultWire{
		FactorRef:  req.FactorRef,
		SnapshotID: req.SnapshotID,
		UniverseID: req.UniverseID,
		AsOf:       frame.AsOf,
		Covered:    len(frame.Values),
		Total:      len(frame.Values) + len(frame.Missing),
		Members:    members,
	})
}

type factorAnalysisResultWire struct {
	Artifact     artifactWire            `json:"artifact"`
	Config       analysis.ArtifactConfig `json:"config"`
	Summary      analysis.Summary        `json:"summary"`
	EvidenceNote string                  `json:"evidence_note"`
}

func (a *API) runFactorAnalysis(w http.ResponseWriter, r *http.Request) {
	if !a.requireResearch(w, r) {
		return
	}
	var req research.AnalysisRequest
	if !DecodeJSON(w, r, &req) {
		return
	}
	res, err := a.research.RunAnalysis(r.Context(), req)
	if err != nil {
		a.writeError(w, r, err)
		return
	}
	stored := res.Stored
	WriteJSON(w, http.StatusOK, factorAnalysisResultWire{
		Artifact: artifactWire{
			ID:        stored.ID,
			Name:      stored.Name,
			MediaType: stored.MediaType,
			Checksum:  stored.Checksum,
			SizeBytes: stored.Size,
			CreatedAt: stored.CreatedAt.Format(time.RFC3339Nano),
		},
		Config:       res.Artifact.Config,
		Summary:      res.Artifact.Summary,
		EvidenceNote: res.Artifact.EvidenceNote,
	})
}
