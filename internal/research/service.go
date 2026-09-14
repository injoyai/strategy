// Package research orchestrates the M1-09 closed loop over the landed
// M1-05..08 engines: universe resolution per decision date, synchronous
// factor runs over pinned views, server-side label construction, and the
// factor-evidence analysis with its canonical artifact persisted through
// the content-addressed artifact store.
//
// This package is the analysis engine's caller — the only place where
// future information (labels) is computed. internal/analysis itself
// stays pure (its import allowlist bans the data stack), so the §6.2
// label-isolation boundary is: price bars are read here, labels cross
// into analysis as explicit LabelSet inputs, and nothing else ever
// carries future data toward a factor computation.
package research

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/injoyai/strategy/internal/analysis"
	"github.com/injoyai/strategy/internal/artifacts"
	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
)

// availabilityPolicy names the DataView availability semantics every
// factor run here is computed under (available_at <= as_of). It rides in
// the factor cache key, so a future policy change can never serve frames
// computed under the old semantics.
const availabilityPolicy = factor.AvailabilityPolicyAvailableAt

// Stored analysis artifacts carry this name and media type.
const (
	analysisArtifactName      = "factor-analysis"
	analysisArtifactMediaType = "application/json"
)

// Service wires the M1 engines into the synchronous research surface.
type Service struct {
	data      *data.Store
	registry  *factor.Registry
	cache     *factor.Cache
	artifacts *artifacts.Store
	analysis  *analysis.Engine
}

// New builds the research service; every dependency is required.
func New(store *data.Store, registry *factor.Registry, cache *factor.Cache, artifactStore *artifacts.Store, checksummer ports.Checksummer) (*Service, error) {
	switch {
	case store == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: data store is required")
	case registry == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: factor registry is required")
	case cache == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: factor cache is required")
	case artifactStore == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: artifact store is required")
	case checksummer == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: checksummer is required")
	}
	analysisEngine, err := analysis.NewEngine(registry, checksummer)
	if err != nil {
		return nil, err
	}
	return &Service{data: store, registry: registry, cache: cache, artifacts: artifactStore, analysis: analysisEngine}, nil
}

// FactorRunRequest is the wire shape of one synchronous factor run: which
// factor, over which universe, against which snapshot, at which decision
// time and over which input window.
type FactorRunRequest struct {
	SnapshotID domain.ID         `json:"snapshot_id"`
	UniverseID domain.ID         `json:"universe_id"`
	FactorRef  domain.VersionRef `json:"factor_ref"`
	Params     map[string]any    `json:"params"`
	AsOf       time.Time         `json:"as_of"`
	WindowFrom time.Time         `json:"window_from"`
}

// AnalysisRequest is the wire shape of a factor-evidence analysis: the
// decision-date range, the research present (as_of, pinning the label
// view) and the fixed analysis configuration.
type AnalysisRequest struct {
	SnapshotID domain.ID          `json:"snapshot_id"`
	UniverseID domain.ID          `json:"universe_id"`
	FactorRef  domain.VersionRef  `json:"factor_ref"`
	Params     map[string]any     `json:"params"`
	Range      domain.Interval    `json:"range"`
	AsOf       time.Time          `json:"as_of"`
	Horizons   []int              `json:"horizons"`
	Groups     int                `json:"groups"`
	MinSamples int                `json:"min_samples"`
	Method     string             `json:"method"`
	Segments   []analysis.Segment `json:"segments"`
}

// AnalysisResult pairs the analysis artifact with its stored copy's
// metadata.
type AnalysisResult struct {
	Artifact *analysis.Artifact
	Stored   artifacts.Artifact
}

// runContext is everything one factor run needs after request
// validation: the bound universe, the snapshot (manifest hash feeds the
// cache key), the pinned view and the resolved members.
type runContext struct {
	universe domain.UniverseVersion
	snapshot domain.Snapshot
	view     ports.DataView
	members  []domain.ID
}

// prepareRun validates the shared request contract and resolves the run
// context: the universe must exist and be bound to the request's
// snapshot, and members resolve through a view pinned to that snapshot
// at as_of — never through current tables.
func (s *Service) prepareRun(ctx context.Context, req FactorRunRequest) (*runContext, error) {
	if err := req.FactorRef.Validate(); err != nil {
		return nil, err
	}
	if req.AsOf.IsZero() {
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: as_of is required")
	}
	if req.WindowFrom.IsZero() {
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: window_from is required")
	}
	if !req.WindowFrom.Before(req.AsOf) {
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: window_from must precede as_of")
	}
	universe, err := s.data.GetUniverseVersion(ctx, req.UniverseID)
	if err != nil {
		return nil, err
	}
	if universe.SnapshotID != req.SnapshotID {
		return nil, domain.NewError(domain.CodeValidationInvalid,
			"research: universe %s is bound to snapshot %s, not %s", universe.ID, universe.SnapshotID, req.SnapshotID)
	}
	snapshot, err := s.data.GetSnapshot(ctx, req.SnapshotID)
	if err != nil {
		return nil, err
	}
	view, err := s.data.OpenView(ctx, req.SnapshotID, req.AsOf)
	if err != nil {
		return nil, err
	}
	members, err := data.ResolveUniverse(ctx, universe, view)
	if err != nil {
		return nil, err
	}
	return &runContext{universe: universe, snapshot: snapshot, view: view, members: members}, nil
}

// engineRequest builds the factor.RunRequest for one resolved context.
// The cache key pins the snapshot manifest hash, universe id + definition
// hash, params, the window and the availability policy — everything the
// frame depends on.
func engineRequest(req FactorRunRequest, rc *runContext) factor.RunRequest {
	return factor.RunRequest{
		Ref:                factor.FactorRef{ID: string(req.FactorRef.ID), Version: req.FactorRef.Version},
		Params:             req.Params,
		Members:            rc.members,
		Range:              domain.Interval{From: req.WindowFrom, To: req.AsOf},
		UniverseID:         rc.universe.ID.String(),
		UniverseHash:       rc.universe.DefinitionHash,
		SnapshotHash:       rc.snapshot.ManifestHash,
		AvailabilityPolicy: availabilityPolicy,
	}
}

// ListFactors returns the registered factor catalog ordered by
// (id, version) — the capability rows forms and preflight pages read.
func (s *Service) ListFactors() []factor.Spec {
	return s.registry.List()
}

// GetFactor returns one registered spec by id and version.
func (s *Service) GetFactor(id, version string) (*factor.Spec, error) {
	return s.registry.Lookup(factor.FactorRef{ID: id, Version: version})
}

// PreflightFactor checks a run without computing: every problem (graph,
// params, universe, per-input data availability) comes back in one
// round. An error return signals infrastructure failure only.
func (s *Service) PreflightFactor(ctx context.Context, req FactorRunRequest) ([]factor.Problem, error) {
	rc, err := s.prepareRun(ctx, req)
	if err != nil {
		return nil, err
	}
	engine, err := factor.NewEngine(s.registry, rc.view, s.cache)
	if err != nil {
		return nil, err
	}
	return engine.Preflight(ctx, engineRequest(req, rc))
}

// RunFactor computes one synchronous cross-section. Failures carry the
// engine's stable codes (factor.preflight_failed maps to 422, unknown
// refs to 404) — see internal/server/errors.go.
func (s *Service) RunFactor(ctx context.Context, req FactorRunRequest) (*factor.Frame, error) {
	rc, err := s.prepareRun(ctx, req)
	if err != nil {
		return nil, err
	}
	engine, err := factor.NewEngine(s.registry, rc.view, s.cache)
	if err != nil {
		return nil, err
	}
	return engine.Run(ctx, engineRequest(req, rc))
}

// restrictParams filters incoming parameters down to the names a spec
// declares — the same pass-through rule the engine applies to
// dependencies (intersectParams), so a composite factor's window
// parameter reaches matching child declarations.
func restrictParams(spec *factor.Spec, incoming map[string]any) map[string]any {
	declared := make(map[string]bool, len(spec.Params))
	for _, p := range spec.Params {
		declared[p.Name] = true
	}
	out := make(map[string]any, len(incoming))
	for name, value := range incoming {
		if declared[name] {
			out[name] = value
		}
	}
	return out
}

// requiredLookback walks the dependency closure and returns the deepest
// static-or-parameterized lookback any input needs, with local params
// canonicalized per spec. RunAnalysis turns it into a warmup window so
// per-date runs never fail preflight for lack of history when the data
// exists.
func (s *Service) requiredLookback(ref factor.FactorRef, params map[string]any) (int, error) {
	deepest := 0
	visited := make(map[string]bool)
	var visit func(factor.FactorRef, map[string]any) error
	visit = func(ref factor.FactorRef, incoming map[string]any) error {
		if visited[ref.String()] {
			return nil
		}
		visited[ref.String()] = true
		spec, err := s.registry.Lookup(ref)
		if err != nil {
			return err
		}
		local, err := s.registry.CanonicalParams(ref, restrictParams(spec, incoming))
		if err != nil {
			return err
		}
		for i := range spec.Inputs {
			if n := spec.Inputs[i].EffectiveLookback(local); n > deepest {
				deepest = n
			}
		}
		for _, dep := range spec.Deps {
			if err := visit(dep, local); err != nil {
				return err
			}
		}
		return nil
	}
	if err := visit(ref, params); err != nil {
		return 0, err
	}
	return deepest, nil
}

// lookbackMargin pads the derived warmup: factor runs read points with
// event_time strictly before the decision time, so the window needs one
// extra session beyond the raw lookback; the second session guards
// calendar-day boundaries around midnight truncation.
const lookbackMargin = 2

// RunAnalysis orchestrates the D6 factor-evidence analysis: derive the
// decision dates from the label view's bar calendar, compute one frame
// per decision date through a per-date PIT view (members resolve at that
// date), build the fixed-policy labels from the as_of-pinned view, and
// seal the canonical artifact into the artifact store. Equal inputs
// always produce a byte-identical artifact, so retries resolve to the
// same content-addressed storage.
func (s *Service) RunAnalysis(ctx context.Context, req AnalysisRequest) (*AnalysisResult, error) {
	if err := req.FactorRef.Validate(); err != nil {
		return nil, err
	}
	window, err := domain.NewInterval(req.Range.From, req.Range.To)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeValidationInvalid, "research: analysis range invalid")
	}
	if req.AsOf.IsZero() {
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: as_of is required")
	}
	// as_of is the research present: every decision date must lie at or
	// before it. An as_of inside the range would silently truncate the
	// series, so it fails closed instead.
	if req.AsOf.Before(window.To) {
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: as_of must not precede range.to")
	}
	if len(req.Horizons) == 0 {
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: at least one horizon is required")
	}
	for _, h := range req.Horizons {
		if h <= 0 {
			return nil, domain.NewError(domain.CodeValidationInvalid, "research: horizons must be positive, got %d", h)
		}
	}

	universe, err := s.data.GetUniverseVersion(ctx, req.UniverseID)
	if err != nil {
		return nil, err
	}
	if universe.SnapshotID != req.SnapshotID {
		return nil, domain.NewError(domain.CodeValidationInvalid,
			"research: universe %s is bound to snapshot %s, not %s", universe.ID, universe.SnapshotID, req.SnapshotID)
	}
	snapshot, err := s.data.GetSnapshot(ctx, req.SnapshotID)
	if err != nil {
		return nil, err
	}

	// The label view is pinned at the research present. Decision dates
	// derive from the final membership's bar dates inside the window
	// (market-wide trading days), while per-date membership still
	// resolves through per-date views — a member that left the dataset
	// keeps contributing the dates it actually traded.
	labelView, err := s.data.OpenView(ctx, req.SnapshotID, req.AsOf)
	if err != nil {
		return nil, err
	}
	finalMembers, err := data.ResolveUniverse(ctx, universe, labelView)
	if err != nil {
		return nil, err
	}

	maxHorizon := req.Horizons[0]
	for _, h := range req.Horizons {
		if h > maxHorizon {
			maxHorizon = h
		}
	}
	series, err := loadBarSeries(ctx, labelView, finalMembers, window, maxHorizon)
	if err != nil {
		return nil, err
	}
	decisionDates := series.decisionDates(window)
	if len(decisionDates) == 0 {
		return nil, domain.NewError(domain.CodeValidationInvalid, "research: no bar dates in the analysis range")
	}

	ref := factor.FactorRef{ID: string(req.FactorRef.ID), Version: req.FactorRef.Version}
	lookback, err := s.requiredLookback(ref, req.Params)
	if err != nil {
		return nil, err
	}
	windowFrom := decisionDates[0].AddDate(0, 0, -(lookback + lookbackMargin))

	sections := make([]analysis.CrossSection, 0, len(decisionDates))
	memberUnion := make(map[domain.ID]bool)
	for _, date := range decisionDates {
		view, err := s.data.OpenView(ctx, req.SnapshotID, date)
		if err != nil {
			return nil, err
		}
		members, err := data.ResolveUniverse(ctx, universe, view)
		if err != nil {
			return nil, err
		}
		engine, err := factor.NewEngine(s.registry, view, s.cache)
		if err != nil {
			return nil, err
		}
		frame, err := engine.Run(ctx, factor.RunRequest{
			Ref:                ref,
			Params:             req.Params,
			Members:            members,
			Range:              domain.Interval{From: windowFrom, To: date},
			UniverseID:         universe.ID.String(),
			UniverseHash:       universe.DefinitionHash,
			SnapshotHash:       snapshot.ManifestHash,
			AvailabilityPolicy: availabilityPolicy,
		})
		if err != nil {
			// Keep the engine's stable code (factor.preflight_failed maps
			// to 422) while adding the failing date; the cause text stays
			// on the wire because domain.Error renders Message only.
			return nil, domain.Wrap(err, domain.ErrorCode(err),
				"research: factor run at %s failed: %s", date.Format(time.RFC3339Nano), err.Error())
		}
		for id := range frame.Values {
			memberUnion[id] = true
		}
		for id := range frame.Missing {
			memberUnion[id] = true
		}
		sections = append(sections, analysis.CrossSection{Time: date, Frame: frame})
	}

	labels := series.labelSets(sortedMemberIDs(memberUnion), decisionDates, req.Horizons)
	artifact, err := s.analysis.Run(analysis.Request{
		Config: analysis.Config{
			Ref:        ref,
			Params:     req.Params,
			LabelPrice: LabelPrice,
			LabelEntry: LabelEntry,
			LabelCost:  LabelCost,
			Method:     req.Method,
			Groups:     req.Groups,
			MinSamples: req.MinSamples,
			Range:      window,
			Segments:   req.Segments,
		},
		Series: sections,
		Labels: labels,
	})
	if err != nil {
		return nil, err // analysis.* codes map on the wire
	}

	// The canonical artifact (checksum included) is stored through the
	// content-addressed artifact store; identical content always resolves
	// to the same storage key, so retries never mint duplicates.
	encoded, err := json.Marshal(artifact)
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "research: encode analysis artifact")
	}
	placement, err := s.artifacts.Ingest(bytes.NewReader(encoded))
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "research: store analysis artifact")
	}
	stored, err := s.artifacts.Record(ctx, artifacts.RecordInput{
		Name:      analysisArtifactName,
		MediaType: analysisArtifactMediaType,
		Placement: placement,
	})
	if err != nil {
		return nil, domain.Wrap(err, domain.CodeInternalError, "research: record analysis artifact")
	}
	return &AnalysisResult{Artifact: artifact, Stored: stored}, nil
}

func sortedMemberIDs(union map[domain.ID]bool) []domain.ID {
	ids := make([]domain.ID, 0, len(union))
	for id := range union {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
