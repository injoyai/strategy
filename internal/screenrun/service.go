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

// Preflight checks a run without computing anything. An error return means the
// request cannot be evaluated at all (malformed, a referenced version does not
// exist, or the infrastructure underneath failed); a nil error with
// Valid=false carries the findings, so one round reports every problem that is
// detectable from the frozen inputs.
//
// What is deliberately not claimed here:
//   - field bindings cannot be resolved yet: the ingestion field mapping
//     (units in particular) is not persisted, so there is no input catalog to
//     check dataset/field/unit against. They are reported as unavailable with
//     an explicit reason instead of being assumed usable.
//   - the rule set is validated structurally only, for the same reason: with no
//     catalog, literal kinds and units have nothing to be compared against.
func (s *Service) Preflight(ctx context.Context, req Request) (*Preflight, error) {
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

	result := &Preflight{}
	catalog, coverage, bindingIssues, windowDates, err := s.resolveBindings(ctx, def, view, members, universe, snapshot)
	if err != nil {
		return nil, err
	}
	// Validation runs with the resolved catalog so literal kinds and operators
	// are checked against what the inputs actually are; a binding the catalog
	// cannot type stays an explicit unknown and its kind checks are skipped
	// rather than assumed.
	result.Issues = append(result.Issues, screening.Validate(def, catalog, screening.DefaultLimits())...)
	result.Coverage = coverage
	result.Issues = append(result.Issues, bindingIssues...)
	if len(members) == 0 {
		result.Issues = append(result.Issues, domain.Issue{
			Code:     codeEmptyPopulation,
			Path:     "universe_ref",
			Message:  "the mother pool has no members at this decision time; the run would succeed with an empty result",
			Severity: domain.SeverityWarning,
		})
	}
	if windowDates > 0 {
		// One scanned row per member per date in the derived window; one result
		// row per member. Both are derived from the resolved inputs, never
		// guessed from the rule set alone.
		scanRows := len(members) * windowDates
		resultRows := len(members)
		result.EstimatedScanRows = &scanRows
		result.EstimatedRows = &resultRows
	}
	result.Valid = !hasError(result.Issues)
	return result, nil
}

// resolveBindings resolves every declared binding, records why one is unusable
// and returns the input catalog the rule set is validated against. It also
// returns the highest date count any factor window covered, which the scan
// estimate is built from.
func (s *Service) resolveBindings(
	ctx context.Context,
	def screening.Definition,
	view ports.DataView,
	members []domain.ID,
	universe domain.UniverseVersion,
	snapshot domain.Snapshot,
) (screening.InputTypes, []Coverage, []domain.Issue, int, error) {
	catalog := screening.InputTypes{}
	var coverage []Coverage
	var issues []domain.Issue
	windowDates := 0
	for _, binding := range def.InputBindings {
		switch binding.Kind {
		case screening.BindingFactor:
			// A factor's output kind is not declared anywhere (its spec carries a
			// unit, not a value kind), so it enters the catalog as an explicit
			// unknown: the validator then skips kind checks for it instead of
			// assuming decimal.
			catalog[binding.BindingID] = ""
			cov, bindingIssues, dates, err := s.checkFactorBinding(ctx, binding, view, members, universe, snapshot)
			if err != nil {
				return nil, nil, nil, 0, err
			}
			coverage = append(coverage, cov)
			issues = append(issues, bindingIssues...)
			if dates > windowDates {
				windowDates = dates
			}
		case screening.BindingField:
			kind, cov, problem, err := s.checkFieldBinding(ctx, binding)
			if err != nil {
				return nil, nil, nil, 0, err
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
	return catalog, coverage, issues, windowDates, nil
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
	for _, field := range dataset.Fields {
		if field.Name != binding.Field {
			continue
		}
		kind := valueKindOf(field.Type)
		if kind == "" {
			reason := codeFieldUnobserved
			problem := issueFor(binding.BindingID, reason,
				fmt.Sprintf("field %s/%s has no observed values, so its type is unknown", binding.Dataset, binding.Field))
			return "", Coverage{BindingID: binding.BindingID, Available: false, Reason: &reason}, &problem, nil
		}
		return kind, Coverage{BindingID: binding.BindingID, Available: true}, nil, nil
	}
	reason := codeFieldUnknown
	problem := issueFor(binding.BindingID, reason,
		fmt.Sprintf("dataset %q has no field %q", binding.Dataset, binding.Field))
	return "", Coverage{BindingID: binding.BindingID, Available: false, Reason: &reason}, &problem, nil
}

// valueKindOf maps a catalog field type to the value kind the rule engine
// compares literals against. An unknown type has no kind, which is how the
// validator learns to skip it rather than guess.
func valueKindOf(fieldType domain.FieldType) domain.ValueKind {
	switch fieldType {
	case domain.FieldDecimal:
		return domain.ValueDecimal
	case domain.FieldNumber:
		return domain.ValueNumber
	case domain.FieldString:
		return domain.ValueString
	case domain.FieldBoolean:
		return domain.ValueBoolean
	case domain.FieldTimestamp:
		return domain.ValueTimestamp
	default:
		return ""
	}
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
) (Coverage, []domain.Issue, int, error) {
	ref := factor.FactorRef{ID: string(binding.FactorRef.ID), Version: binding.FactorRef.Version}
	spec, err := s.registry.Lookup(ref)
	if err != nil {
		return unavailable(binding.BindingID, domain.ErrorCode(err)),
			[]domain.Issue{issueFor(binding.BindingID, domain.ErrorCode(err), err.Error())}, 0, nil
	}
	params, err := s.registry.CanonicalParams(ref, binding.Params)
	if err != nil {
		return unavailable(binding.BindingID, domain.ErrorCode(err)),
			[]domain.Issue{issueFor(binding.BindingID, domain.ErrorCode(err), err.Error())}, 0, nil
	}
	windowFrom, dates, problem, err := deriveWindow(ctx, view, members, spec, params)
	if err != nil {
		return Coverage{}, nil, 0, err
	}
	if problem != nil {
		return unavailable(binding.BindingID, problem.Code), []domain.Issue{*problem}, dates, nil
	}
	engine, err := factor.NewEngine(s.registry, view, s.cache)
	if err != nil {
		return Coverage{}, nil, 0, err
	}
	problems, err := engine.Preflight(ctx, factor.RunRequest{
		Ref:                ref,
		Params:             params,
		Members:            members,
		Range:              domain.Interval{From: windowFrom, To: view.AsOf()},
		UniverseID:         universe.ID.String(),
		UniverseHash:       universe.DefinitionHash,
		SnapshotHash:       snapshot.ManifestHash,
		AvailabilityPolicy: factor.AvailabilityPolicyAvailableAt,
	})
	if err != nil {
		return Coverage{}, nil, 0, err
	}
	if len(problems) > 0 {
		bindingIssues := make([]domain.Issue, 0, len(problems))
		for _, problem := range problems {
			bindingIssues = append(bindingIssues, issueFor(binding.BindingID, problem.Code, problem.Message))
		}
		return unavailable(binding.BindingID, problems[0].Code), bindingIssues, dates, nil
	}
	// No input at all means no data to check: registration and parameters were
	// the whole contract.
	if windowFrom.IsZero() {
		return Coverage{BindingID: binding.BindingID, Available: true}, nil, 0, nil
	}
	return Coverage{BindingID: binding.BindingID, Available: true}, nil, dates, nil
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
