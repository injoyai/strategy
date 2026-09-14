// Package screenrun orchestrates screener runs over the landed data and
// factor engines. It is the M1S counterpart of internal/research: the rule
// engine in internal/screening stays pure (no database, network or clock), and
// everything that has to touch a pinned DataView, the immutable versions or the
// factor registry lives here.
//
// The package is separate from internal/screening because ports already
// imports screening for the version store; orchestrating from screening would
// close that import cycle.
package screenrun

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
	"github.com/injoyai/strategy/internal/screening"
)

// Service wires the screener rule engine to the data and factor engines.
type Service struct {
	data     *data.Store
	registry *factor.Registry
	cache    *factor.Cache
}

// New builds the screening run service; every dependency is required.
func New(store *data.Store, registry *factor.Registry, cache *factor.Cache) (*Service, error) {
	switch {
	case store == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "screenrun: data store is required")
	case registry == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "screenrun: factor registry is required")
	case cache == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "screenrun: factor cache is required")
	}
	return &Service{data: store, registry: registry, cache: cache}, nil
}

// Request mirrors the contract's ScreenRunCreate: every input is frozen at
// submission, with no implicit snapshot, universe, time or policy defaults.
type Request struct {
	ScreenerRef         domain.VersionRef `json:"screener_ref"`
	SnapshotID          domain.ID         `json:"snapshot_id"`
	UniverseRef         domain.VersionRef `json:"universe_ref"`
	AsOf                time.Time         `json:"as_of"`
	DecisionTimezone    string            `json:"decision_timezone"`
	StrictPIT           bool              `json:"strict_pit"`
	RequiredValuePolicy string            `json:"required_value_policy"`
	SourceRunID         domain.ID         `json:"source_run_id"`
}

// Coverage reports whether one declared binding could be resolved for this
// run. It is per binding rather than per run so a caller can see exactly which
// input is unusable instead of a single opaque failure.
type Coverage struct {
	BindingID domain.ID `json:"binding_id"`
	Available bool      `json:"available"`
	Reason    *string   `json:"reason"`
}

// Preflight is the read-only answer for one prospective run. Valid is false
// exactly when at least one error-severity issue was found; warnings report
// what could not be checked without failing the request.
type Preflight struct {
	Valid             bool
	Issues            []domain.Issue
	Coverage          []Coverage
	EstimatedScanRows *int
	EstimatedRows     *int
}

// Result is one computed screening run: the mutually exclusive stage counts,
// the frozen rows, in the engine's canonical order (selected and rankable rows
// by rank, then excluded rows by instrument), the display-column descriptors
// derived from those rows, and the codes of the non-blocking findings the run
// was computed under.
type Result struct {
	Summary       screening.Summary
	Rows          []screening.Row
	Columns       []domain.Field
	QualityLimits []string
}

// ScoringPolicyVersion names the versioned scoring policy every run is computed
// under — the value the contract's ScreenRun.scoring_policy_version reports.
// Scoring places, tie handling and rounding are versioned because changing them
// changes published ranks.
const ScoringPolicyVersion = "scoring-policy/1"

// scoringPolicy returns the policy behind ScoringPolicyVersion: scores are
// rounded at the engine's maximum supported precision (12 places, the bound the
// engine validates against) with banker's rounding, so a published score is
// reproducible without discarding digits the engine computed.
func scoringPolicy() screening.Scoring {
	return screening.Scoring{Places: 12, Rounding: domain.RoundHalfEven}
}

// runPolicy assembles the engine policy for one request: the frozen required
// value policy plus the versioned scoring policy.
func runPolicy(req Request) screening.Policy {
	return screening.Policy{
		RequiredValue: screening.RequiredValuePolicy(req.RequiredValuePolicy),
		Scoring:       scoringPolicy(),
	}
}

// resolved is everything one frozen request resolves to: the immutable
// versions it pins, the view and mother pool it reads, the rule set, the input
// catalog the rule set is validated against, and the findings that came out of
// resolving it.
type resolved struct {
	universe domain.UniverseVersion
	snapshot domain.Snapshot
	view     ports.DataView
	members  []domain.ID
	def      screening.Definition
	catalog  screening.InputTypes
	coverage []Coverage
	issues   []domain.Issue
	// windows is the derived factor input window per factor binding; dates is
	// the widest window's date count.
	windows     map[domain.ID]time.Time
	windowDates int
}

func (r *resolved) hasErrors() bool { return hasError(r.issues) }

// Preflight checks a run without computing anything. An error return means the
// request cannot be evaluated at all (malformed, a referenced version does not
// exist, or the infrastructure underneath failed); a nil error with
// Valid=false carries the findings, so one round reports every problem that is
// detectable from the frozen inputs.
//
// What is deliberately not claimed here:
//   - the rule set is validated against what the dataset catalog knows, so a
//     condition's operator and literal kind are checked; what the catalog
//     cannot type (a factor's output kind, a field with no observed values)
//     stays an explicit unknown and its kind checks are skipped rather than
//     assumed.
//   - units are not compared: the rule engine compares value kinds, and a
//     condition literal carries no unit, so unit compatibility has no hook yet
//     even though the catalog records units.
func (s *Service) Preflight(ctx context.Context, req Request) (*Preflight, error) {
	res, err := s.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	out := &Preflight{
		Issues:   append([]domain.Issue{}, res.issues...),
		Coverage: res.coverage,
	}
	if len(res.members) == 0 {
		out.Issues = append(out.Issues, domain.Issue{
			Code:     codeEmptyPopulation,
			Path:     "universe_ref",
			Message:  "the mother pool has no members at this decision time; the run would succeed with an empty result",
			Severity: domain.SeverityWarning,
		})
	}
	if res.windowDates > 0 {
		// One scanned row per member per date in the derived window: that number
		// exists only for a factor window, so a field-only run leaves it unset
		// rather than inventing one.
		scanRows := len(res.members) * res.windowDates
		out.EstimatedScanRows = &scanRows
	}
	// The result row count is the mother pool size — one row per member,
	// whatever the inputs are — so it is reported whenever the pool resolved.
	resultRows := len(res.members)
	out.EstimatedRows = &resultRows
	out.Valid = !hasError(out.Issues)
	return out, nil
}

// Execute computes one run over the frozen inputs. It re-resolves everything
// (the contract re-validates on submission) and refuses to compute when
// resolution produced any error-severity finding: a run whose inputs are known
// to be unusable would publish a result that looks authoritative and is not.
func (s *Service) Execute(ctx context.Context, req Request) (*Result, error) {
	res, err := s.resolve(ctx, req)
	if err != nil {
		return nil, err
	}
	if res.hasErrors() {
		return nil, domain.NewError(CodePreflightFailed, "screenrun: preflight failed: %s", SummarizeIssues(res.issues))
	}
	universe, err := s.instrumentInputs(ctx, res)
	if err != nil {
		return nil, err
	}
	computed, err := screening.Run(res.def, res.catalog, runPolicy(req), screening.DefaultLimits(), universe)
	if err != nil {
		return nil, err
	}
	return &Result{
		Summary:       computed.Summary,
		Rows:          computed.Rows,
		Columns:       screening.ResultColumns(res.def.DisplayColumns, computed.Rows),
		QualityLimits: warningCodes(res.issues),
	}, nil
}

// warningCodes collects the distinct codes of the non-blocking findings one
// resolution reported. They are what a pool saved from this run must carry
// forward, so they travel with the published result; error-severity findings
// never reach here because a run refuses to compute while any is present.
func warningCodes(issues []domain.Issue) []string {
	seen := make(map[string]struct{}, len(issues))
	codes := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Severity == domain.SeverityError {
			continue
		}
		if _, dup := seen[issue.Code]; dup {
			continue
		}
		seen[issue.Code] = struct{}{}
		codes = append(codes, issue.Code)
	}
	return codes
}

// resolve performs the shared resolution both Preflight and Execute need:
// pinned versions, snapshot binding, strict PIT, the mother pool through a
// pinned view, the rule set, the input catalog and every finding that comes out
// of that.
func (s *Service) resolve(ctx context.Context, req Request) (*resolved, error) {
	if err := req.validate(); err != nil {
		return nil, err
	}
	version, err := s.data.GetScreenerVersion(ctx, req.ScreenerRef.ID, domain.ID(req.ScreenerRef.Version))
	if err != nil {
		return nil, err
	}
	universe, err := s.data.GetUniverseVersion(ctx, req.UniverseRef.ID)
	if err != nil {
		return nil, err
	}
	// A universe id already names one immutable revision, so the reference's
	// version must be its definition hash: accepting an arbitrary label would
	// let a run proceed against a row that no longer matches what the caller
	// pinned.
	if req.UniverseRef.Version != universe.DefinitionHash {
		return nil, domain.NewError(domain.CodeResourceConflict,
			"screenrun: universe %s version pin %q does not match its definition hash", universe.ID, req.UniverseRef.Version)
	}
	if universe.SnapshotID != req.SnapshotID {
		return nil, domain.NewError(domain.CodeResourceConflict,
			"screenrun: universe %s is bound to snapshot %s, not %s", universe.ID, universe.SnapshotID, req.SnapshotID)
	}
	snapshot, err := s.data.GetSnapshot(ctx, req.SnapshotID)
	if err != nil {
		return nil, err
	}
	// The snapshot's strictness is decided when it is published, so a run
	// demanding strict PIT against a snapshot that admits unverified rows is
	// asking for evidence the snapshot cannot provide.
	if req.StrictPIT && !snapshot.StrictPIT {
		return nil, domain.NewError(domain.CodeResourceConflict,
			"screenrun: snapshot %s is not strict PIT", snapshot.ID)
	}
	view, err := s.data.OpenView(ctx, req.SnapshotID, req.AsOf)
	if err != nil {
		return nil, err
	}
	members, err := data.ResolveUniverse(ctx, universe, view)
	if err != nil {
		return nil, err
	}
	def, err := screening.DefinitionOfWire(version.Definition, version.Name, version.Description, version.ParentID)
	if err != nil {
		return nil, err
	}
	res := &resolved{
		universe: universe,
		snapshot: snapshot,
		view:     view,
		members:  members,
		def:      def,
	}
	if err := s.resolveBindings(ctx, res); err != nil {
		return nil, err
	}
	// Validation runs with the resolved catalog so literal kinds and operators
	// are checked against what the inputs actually are; a binding the catalog
	// cannot type stays an explicit unknown and its kind checks are skipped
	// rather than assumed.
	res.issues = append(screening.Validate(def, res.catalog, screening.DefaultLimits()), res.issues...)
	return res, nil
}

// resolveBindings resolves every declared binding, records why one is unusable
// and fills the input catalog the rule set is validated against.
func (s *Service) resolveBindings(ctx context.Context, res *resolved) error {
	catalog := screening.InputTypes{}
	windows := map[domain.ID]time.Time{}
	var coverage []Coverage
	var issues []domain.Issue
	for _, binding := range res.def.InputBindings {
		switch binding.Kind {
		case screening.BindingFactor:
			// A factor's output kind is not declared anywhere (its spec carries a
			// unit, not a value kind), so it enters the catalog as an explicit
			// unknown: the validator then skips kind checks for it instead of
			// assuming decimal.
			catalog[binding.BindingID] = ""
			result, err := s.checkFactorBinding(ctx, binding, res.view, res.members, res.universe, res.snapshot)
			if err != nil {
				return err
			}
			coverage = append(coverage, result.coverage)
			issues = append(issues, result.issues...)
			if !result.window.IsZero() {
				windows[binding.BindingID] = result.window
			}
			if result.dates > res.windowDates {
				res.windowDates = result.dates
			}
		case screening.BindingField:
			kind, cov, problem, err := s.checkFieldBinding(ctx, binding)
			if err != nil {
				return err
			}
			if problem != nil {
				issues = append(issues, *problem)
			} else {
				catalog[binding.BindingID] = kind
			}
			coverage = append(coverage, cov)
		default:
			// An unknown binding kind cannot be resolved against anything; the
			// definition validator reports the kind itself as invalid.
			reason := codeCatalogUnavailable
			coverage = append(coverage, Coverage{BindingID: binding.BindingID, Available: false, Reason: &reason})
		}
	}
	res.catalog = catalog
	res.coverage = coverage
	res.issues = issues
	res.windows = windows
	return nil
}

// checkFieldBinding resolves one field binding against the dataset catalog: the
// dataset must exist and the field must be known there. The returned kind is
// empty when the field is known but its values were never observed — the
// catalog reports "unknown" for that, and an unknown kind must not become a
// silent assumption.
func (s *Service) checkFieldBinding(ctx context.Context, binding screening.InputBinding) (domain.ValueKind, Coverage, *domain.Issue, error) {
	dataset, err := s.data.GetDataset(ctx, domain.ID(binding.Dataset))
	if err != nil {
		if domain.ErrorCode(err) != domain.CodeResourceNotFound {
			return "", Coverage{}, nil, err
		}
		reason := codeDatasetUnknown
		problem := issueFor(binding.BindingID, reason,
			fmt.Sprintf("dataset %q referenced by binding %s does not exist", binding.Dataset, binding.BindingID))
		return "", Coverage{BindingID: binding.BindingID, Available: false, Reason: &reason}, &problem, nil
	}
	field, ok := datasetField(dataset, binding.Field)
	if !ok {
		reason := codeFieldUnknown
		problem := issueFor(binding.BindingID, reason,
			fmt.Sprintf("dataset %q has no field %q", binding.Dataset, binding.Field))
		return "", Coverage{BindingID: binding.BindingID, Available: false, Reason: &reason}, &problem, nil
	}
	kind := screening.ValueKindOfFieldType(field.Type)
	if kind == "" {
		reason := codeFieldUnobserved
		problem := issueFor(binding.BindingID, reason,
			fmt.Sprintf("field %s/%s has no observed values, so its type is unknown", binding.Dataset, binding.Field))
		return "", Coverage{BindingID: binding.BindingID, Available: false, Reason: &reason}, &problem, nil
	}
	return kind, Coverage{BindingID: binding.BindingID, Available: true}, nil, nil
}

// checkFactorInputUnits verifies every input a factor declares against the
// dataset catalog: the dataset and field must be known, and the field's
// declared unit must be exactly the unit the factor contract expects. Units are
// compared by equality — the same rule the expression unit checker applies — so
// "price" is not "shares" even though both are numbers.
//
// An undeclared unit is a finding, not a pass: the ingestion that never declared
// the field's unit leaves the contract unverifiable, and treating that as
// compatible would silently use values under a unit nobody stated.
func (s *Service) checkFactorInputUnits(ctx context.Context, bindingID domain.ID, spec *factor.Spec) ([]domain.Issue, error) {
	var issues []domain.Issue
	for _, input := range spec.Inputs {
		dataset, err := s.data.GetDataset(ctx, domain.ID(input.Dataset))
		if err != nil {
			if domain.ErrorCode(err) != domain.CodeResourceNotFound {
				return nil, err
			}
			issues = append(issues, issueFor(bindingID, codeDatasetUnknown,
				fmt.Sprintf("factor input %s reads dataset %q, which does not exist", input.Name, input.Dataset)))
			continue
		}
		field, ok := datasetField(dataset, input.Field)
		if !ok {
			issues = append(issues, issueFor(bindingID, codeFieldUnknown,
				fmt.Sprintf("factor input %s reads %s/%s, which the dataset does not have", input.Name, input.Dataset, input.Field)))
			continue
		}
		switch {
		case field.Unit == "":
			issues = append(issues, issueFor(bindingID, codeUnitUndeclared,
				fmt.Sprintf("factor input %s expects unit %q but %s/%s was never declared with a unit, so the contract cannot be verified",
					input.Name, input.Unit, input.Dataset, input.Field)))
		case field.Unit != input.Unit:
			issues = append(issues, issueFor(bindingID, codeUnitMismatch,
				fmt.Sprintf("factor input %s expects unit %q but %s/%s is declared in %q",
					input.Name, input.Unit, input.Dataset, input.Field, field.Unit)))
		}
	}
	return issues, nil
}

// datasetField finds one declared field of a dataset by name. The catalog
// reports declared fields first and observed ones after, so a match is a match
// wherever it sits.
func datasetField(dataset domain.Dataset, name string) (domain.Field, bool) {
	for _, field := range dataset.Fields {
		if field.Name == name {
			return field, true
		}
	}
	return domain.Field{}, false
}

// bindingResult is one resolved factor binding: its coverage, its findings and
// the window it needs (zero when the factor declares no inputs).
type bindingResult struct {
	coverage Coverage
	issues   []domain.Issue
	window   time.Time
	dates    int
}

// checkFactorBinding verifies one factor binding end to end: the version is
// registered, its parameters canonicalize, a window wide enough for its
// declared lookback exists in the snapshot, and the engine's own preflight
// (graph, per-input availability, PIT and staleness) reports nothing.
func (s *Service) checkFactorBinding(
	ctx context.Context,
	binding screening.InputBinding,
	view ports.DataView,
	members []domain.ID,
	universe domain.UniverseVersion,
	snapshot domain.Snapshot,
) (bindingResult, error) {
	ref := factor.FactorRef{ID: string(binding.FactorRef.ID), Version: binding.FactorRef.Version}
	spec, err := s.registry.Lookup(ref)
	if err != nil {
		return bindingResult{
			coverage: unavailable(binding.BindingID, domain.ErrorCode(err)),
			issues:   []domain.Issue{issueFor(binding.BindingID, domain.ErrorCode(err), err.Error())},
		}, nil
	}
	params, err := s.registry.CanonicalParams(ref, binding.Params)
	if err != nil {
		return bindingResult{
			coverage: unavailable(binding.BindingID, domain.ErrorCode(err)),
			issues:   []domain.Issue{issueFor(binding.BindingID, domain.ErrorCode(err), err.Error())},
		}, nil
	}
	// The unit contract is checked before anything is computed: a factor input
	// bound to a field whose declared unit is missing or different would silently
	// treat the values as something they are not.
	unitIssues, err := s.checkFactorInputUnits(ctx, binding.BindingID, spec)
	if err != nil {
		return bindingResult{}, err
	}
	if len(unitIssues) > 0 {
		return bindingResult{
			coverage: unavailable(binding.BindingID, unitIssues[0].Code),
			issues:   unitIssues,
		}, nil
	}
	windowFrom, dates, problem, err := deriveWindow(ctx, view, members, spec, params)
	if err != nil {
		return bindingResult{}, err
	}
	if problem != nil {
		return bindingResult{
			coverage: unavailable(binding.BindingID, problem.Code),
			issues:   []domain.Issue{*problem},
			dates:    dates,
		}, nil
	}
	engine, err := factor.NewEngine(s.registry, view, s.cache)
	if err != nil {
		return bindingResult{}, err
	}
	engineReq := factor.RunRequest{
		Ref:                ref,
		Params:             params,
		Members:            members,
		Range:              factorWindow(windowFrom, view.AsOf()),
		UniverseID:         universe.ID.String(),
		UniverseHash:       universe.DefinitionHash,
		SnapshotHash:       snapshot.ManifestHash,
		AvailabilityPolicy: factor.AvailabilityPolicyAvailableAt,
		// Screening reports the members it cannot rank as a stage of its own, so a
		// member the factor engine cannot compute at this decision time is a
		// caveat rather than a refusal. What the engine tolerates is asked of the
		// request itself, so the two sides cannot drift apart.
		TolerateMemberGaps: true,
	}
	problems, err := engine.Preflight(ctx, engineReq)
	if err != nil {
		return bindingResult{}, err
	}
	if len(problems) > 0 {
		var blocking, caveats []domain.Issue
		for _, problem := range problems {
			issue := issueFor(binding.BindingID, problem.Code, problem.Message)
			if engineReq.Tolerates(problem.Code) {
				issue.Severity = domain.SeverityWarning
				caveats = append(caveats, issue)
				continue
			}
			blocking = append(blocking, issue)
		}
		if len(blocking) > 0 {
			return bindingResult{
				coverage: unavailable(binding.BindingID, blocking[0].Code),
				issues:   append(blocking, caveats...),
				dates:    dates,
			}, nil
		}
		// The binding resolves and carries caveats: the run proceeds and the
		// members named by the caveats land in the stages the screening engine
		// already reports (rank_insufficient, or condition_unknown).
		return bindingResult{
			coverage: Coverage{BindingID: binding.BindingID, Available: true},
			issues:   caveats,
			window:   windowFrom,
			dates:    dates,
		}, nil
	}
	return bindingResult{
		coverage: Coverage{BindingID: binding.BindingID, Available: true},
		window:   windowFrom,
		dates:    dates,
	}, nil
}

// factorWindow turns a derived window start into the half-open range the factor
// engine requires. A factor that declares no inputs has no derived start, and
// the engine insists on a non-empty half-open range, so the smallest range
// before the decision time is used — it reads no data because nothing is
// declared to read.
func factorWindow(from, asOf time.Time) domain.Interval {
	if from.IsZero() {
		return domain.Interval{From: asOf.Add(-time.Nanosecond), To: asOf}
	}
	return domain.Interval{From: from, To: asOf}
}

// deriveWindow resolves the input window from the data instead of converting a
// declared period count into a time span: the window starts at the
// lookback-th most recent event time before as_of, so the snapshot itself says
// how far back the required points reach. The widest requirement across the
// factor's inputs wins, and a snapshot that does not carry the history is
// reported as insufficient rather than silently shortened.
//
// A returned zero time with no problem means the factor declares no inputs.
func deriveWindow(ctx context.Context, view ports.DataView, members []domain.ID, spec *factor.Spec, params map[string]any) (time.Time, int, *domain.Issue, error) {
	var from time.Time
	dates := 0
	for _, input := range spec.Inputs {
		need := input.EffectiveLookback(params)
		if need < 1 {
			// A latest-value input still needs its newest point inside the
			// half-open range.
			need = 1
		}
		times, err := view.RecentEventTimes(ctx, input.Dataset, input.Frequency, members, need)
		if err != nil {
			return time.Time{}, 0, nil, err
		}
		if len(times) > dates {
			dates = len(times)
		}
		if len(times) < need {
			problem := domain.Issue{
				Code: factor.ProblemInsufficientHistory,
				Path: "input_bindings[" + input.Name + "]",
				Message: fmt.Sprintf("input %s needs %d points of %s/%s before as_of but the snapshot carries %d",
					input.Name, need, input.Dataset, input.Frequency, len(times)),
				Severity: domain.SeverityError,
			}
			return time.Time{}, dates, &problem, nil
		}
		candidate := times[len(times)-1]
		if from.IsZero() || candidate.Before(from) {
			from = candidate
		}
	}
	return from, dates, nil, nil
}

func unavailable(bindingID domain.ID, reason string) Coverage {
	return Coverage{BindingID: bindingID, Available: false, Reason: &reason}
}

// instrumentInputs assembles the engine's per-member input values: factor
// bindings come from a factor run over their derived window, field bindings
// from each member's latest visible value. A member with no value for a binding
// simply has no entry, which the engine reports as missing — never zero-filled.
func (s *Service) instrumentInputs(ctx context.Context, res *resolved) ([]screening.InstrumentInputs, error) {
	byMember := make(map[domain.ID]screening.Inputs, len(res.members))
	for _, member := range res.members {
		byMember[member] = screening.Inputs{}
	}
	for _, binding := range res.def.InputBindings {
		switch binding.Kind {
		case screening.BindingFactor:
			frame, err := s.runFactorBinding(ctx, res, binding)
			if err != nil {
				return nil, err
			}
			for member, value := range frame.Values {
				if inputs, ok := byMember[member]; ok {
					inputs[binding.BindingID] = domain.Value{Kind: domain.ValueDecimal, Encoded: string(value)}
				}
			}
			for member, reason := range frame.Missing {
				if inputs, ok := byMember[member]; ok {
					inputs[binding.BindingID] = domain.Value{MissingReason: reason}
				}
			}
		case screening.BindingField:
			// A field binding declares a dataset and a field but no frequency, so
			// the latest visible value of that field is the only faithful reading:
			// choosing a frequency here would invent a decision the screener never
			// made, and the catalog reports the dataset's declared frequency for
			// anyone who needs it.
			values, err := res.view.LatestValues(ctx, binding.Dataset, "", binding.Field, res.members)
			if err != nil {
				return nil, err
			}
			for member, value := range values {
				if inputs, ok := byMember[member]; ok {
					inputs[binding.BindingID] = value
				}
			}
		}
	}
	out := make([]screening.InstrumentInputs, 0, len(res.members))
	for _, member := range res.members {
		out = append(out, screening.InstrumentInputs{InstrumentID: member, Values: byMember[member]})
	}
	return out, nil
}

// runFactorBinding computes one factor binding over the run's pinned view and
// the window resolution derived for it.
func (s *Service) runFactorBinding(ctx context.Context, res *resolved, binding screening.InputBinding) (*factor.Frame, error) {
	ref := factor.FactorRef{ID: string(binding.FactorRef.ID), Version: binding.FactorRef.Version}
	params, err := s.registry.CanonicalParams(ref, binding.Params)
	if err != nil {
		return nil, err
	}
	engine, err := factor.NewEngine(s.registry, res.view, s.cache)
	if err != nil {
		return nil, err
	}
	return engine.Run(ctx, factor.RunRequest{
		Ref:                ref,
		Params:             params,
		Members:            res.members,
		Range:              factorWindow(res.windows[binding.BindingID], res.view.AsOf()),
		UniverseID:         res.universe.ID.String(),
		UniverseHash:       res.universe.DefinitionHash,
		SnapshotHash:       res.snapshot.ManifestHash,
		AvailabilityPolicy: factor.AvailabilityPolicyAvailableAt,
		// The same mode the binding's preflight used: the two must agree or a run
		// could be accepted and then fail at compute time.
		TolerateMemberGaps: true,
	})
}

// SummarizeIssues renders the error-severity findings into one message.
// domain.Error surfaces only its message, so the findings travel inline instead
// of being dropped on the wire.
func SummarizeIssues(issues []domain.Issue) string {
	parts := make([]string, 0, len(issues))
	for _, issue := range issues {
		if issue.Severity != domain.SeverityError {
			continue
		}
		parts = append(parts, issue.Code+" ("+issue.Path+"): "+issue.Message)
	}
	return strings.Join(parts, "; ")
}

func issueFor(bindingID domain.ID, code, message string) domain.Issue {
	return domain.Issue{
		Code:     code,
		Path:     "input_bindings[" + bindingID.String() + "]",
		Message:  message,
		Severity: domain.SeverityError,
	}
}

func hasError(issues []domain.Issue) bool {
	for _, issue := range issues {
		if issue.Severity == domain.SeverityError {
			return true
		}
	}
	return false
}

// FrozenConfigOf projects a request onto the payload a run freezes: exactly
// the contract's ScreenRunCreate fields, so the stored configuration and the
// API response are the same document.
func FrozenConfigOf(req Request) screening.WireRunConfig {
	return screening.WireRunConfig{
		ScreenerRef:         req.ScreenerRef,
		SnapshotID:          req.SnapshotID,
		UniverseRef:         req.UniverseRef,
		AsOf:                req.AsOf.UTC(),
		DecisionTimezone:    req.DecisionTimezone,
		StrictPIT:           req.StrictPIT,
		RequiredValuePolicy: req.RequiredValuePolicy,
		SourceRunID:         req.SourceRunID,
	}
}

// RequestOfFrozen rebuilds the executable request from a stored configuration.
// A retry replays the exact frozen inputs, never a re-read of current state.
func RequestOfFrozen(cfg screening.WireRunConfig) Request {
	return Request{
		ScreenerRef:         cfg.ScreenerRef,
		SnapshotID:          cfg.SnapshotID,
		UniverseRef:         cfg.UniverseRef,
		AsOf:                cfg.AsOf,
		DecisionTimezone:    cfg.DecisionTimezone,
		StrictPIT:           cfg.StrictPIT,
		RequiredValuePolicy: cfg.RequiredValuePolicy,
		SourceRunID:         cfg.SourceRunID,
	}
}

// validate rejects a request that cannot be evaluated at all. Failures are
// request errors (HTTP 400/404/409 class), never findings: without a resolved
// screener, universe and decision time there is nothing to preflight.
func (r Request) validate() error {
	if err := r.ScreenerRef.Validate(); err != nil {
		return err
	}
	if err := r.UniverseRef.Validate(); err != nil {
		return err
	}
	if r.SnapshotID == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screenrun: snapshot_id is required")
	}
	if r.AsOf.IsZero() {
		return domain.NewError(domain.CodeValidationInvalid, "screenrun: as_of is required")
	}
	if strings.TrimSpace(r.DecisionTimezone) == "" {
		return domain.NewError(domain.CodeValidationInvalid, "screenrun: decision_timezone is required")
	}
	if _, err := time.LoadLocation(r.DecisionTimezone); err != nil {
		return domain.NewError(domain.CodeValidationInvalid, "screenrun: decision_timezone %q is not a known time zone", r.DecisionTimezone)
	}
	switch screening.RequiredValuePolicy(r.RequiredValuePolicy) {
	case screening.PolicyExcludeInstrument, screening.PolicyFailRun:
	default:
		return domain.NewError(domain.CodeValidationInvalid,
			"screenrun: required_value_policy must be one of: exclude_instrument, fail_run")
	}
	return nil
}
