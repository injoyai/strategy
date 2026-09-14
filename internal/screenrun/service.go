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
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/data"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/screening"
)

// Service wires the screener rule engine to the data and factor engines.
type Service struct {
	data     *data.Store
	registry *factor.Registry
}

// New builds the screening run service; both dependencies are required.
func New(store *data.Store, registry *factor.Registry) (*Service, error) {
	switch {
	case store == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "screenrun: data store is required")
	case registry == nil:
		return nil, domain.NewError(domain.CodeValidationInvalid, "screenrun: factor registry is required")
	}
	return &Service{data: store, registry: registry}, nil
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
// request cannot be evaluated at all (malformed, or a referenced version does
// not exist); a nil error with Valid=false carries the findings, so one round
// reports every problem that is detectable from the frozen inputs.
//
// What is deliberately not claimed here:
//   - field bindings cannot be resolved yet: the ingestion field mapping
//     (units in particular) is not persisted, so there is no input catalog to
//     check dataset/field/unit against. They are reported as unavailable with
//     an explicit reason instead of being assumed usable.
//   - factor data availability (PIT, lookback, staleness) is not evaluated:
//     the window a factor needs is derived from its declaration, and that
//     derivation is not defined yet. Registration and parameter validity are
//     checked, which is what a caller can act on today.
//   - row estimates stay null for the same reason the factor preflight leaves
//     them null: findings, not estimates, are what this contract returns.
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
	result.Issues = append(result.Issues, screening.Validate(def, nil, screening.DefaultLimits())...)
	result.Issues = append(result.Issues, s.coverage(def, &result.Coverage)...)
	if len(members) == 0 {
		result.Issues = append(result.Issues, domain.Issue{
			Code:     codeEmptyPopulation,
			Path:     "universe_ref",
			Message:  "the mother pool has no members at this decision time; the run would succeed with an empty result",
			Severity: domain.SeverityWarning,
		})
	}
	result.Valid = !hasError(result.Issues)
	return result, nil
}

// coverage resolves every declared binding and records why one is unusable.
// Findings are returned as issues so the verdict and the per-binding detail
// never disagree.
func (s *Service) coverage(def screening.Definition, out *[]Coverage) []domain.Issue {
	var issues []domain.Issue
	unresolvableFields := make([]domain.ID, 0)
	checkedFactors := 0
	for _, binding := range def.InputBindings {
		switch binding.Kind {
		case screening.BindingFactor:
			if problem := s.checkFactorBinding(binding); problem != nil {
				*out = append(*out, Coverage{BindingID: binding.BindingID, Available: false, Reason: &problem.Code})
				issues = append(issues, *problem)
				continue
			}
			checkedFactors++
			*out = append(*out, Coverage{BindingID: binding.BindingID, Available: true})
		case screening.BindingField:
			// No input catalog exists yet, so the dataset/field/unit cannot be
			// checked against anything. Say so instead of assuming it works.
			reason := codeCatalogUnavailable
			*out = append(*out, Coverage{BindingID: binding.BindingID, Available: false, Reason: &reason})
			unresolvableFields = append(unresolvableFields, binding.BindingID)
		default:
			reason := codeCatalogUnavailable
			*out = append(*out, Coverage{BindingID: binding.BindingID, Available: false, Reason: &reason})
		}
	}
	if len(unresolvableFields) > 0 {
		issues = append(issues, domain.Issue{
			Code:     codeCatalogUnavailable,
			Path:     "input_bindings",
			Message:  "field bindings cannot be resolved: dataset field metadata (units in particular) is not persisted, so no input catalog exists yet",
			Severity: domain.SeverityWarning,
		})
	}
	if checkedFactors > 0 {
		// Registration and parameters were verified; the data behind them was
		// not. Saying that out loud keeps "available" from reading as "ready".
		issues = append(issues, domain.Issue{
			Code:     codeDataAvailabilityUnchecked,
			Path:     "input_bindings",
			Message:  "factor data availability (PIT, lookback, staleness) was not evaluated: the window derivation over a pinned view is not defined yet",
			Severity: domain.SeverityWarning,
		})
	}
	return issues
}

// checkFactorBinding verifies what the registry can answer today: the factor
// version exists and its parameters canonicalize. The returned issue carries
// the stable code of the underlying failure.
func (s *Service) checkFactorBinding(binding screening.InputBinding) *domain.Issue {
	ref := factor.FactorRef{ID: string(binding.FactorRef.ID), Version: binding.FactorRef.Version}
	if _, err := s.registry.Lookup(ref); err != nil {
		return &domain.Issue{
			Code:     domain.ErrorCode(err),
			Path:     "input_bindings[" + binding.BindingID.String() + "]",
			Message:  err.Error(),
			Severity: domain.SeverityError,
		}
	}
	if _, err := s.registry.CanonicalParams(ref, binding.Params); err != nil {
		return &domain.Issue{
			Code:     domain.ErrorCode(err),
			Path:     "input_bindings[" + binding.BindingID.String() + "]",
			Message:  err.Error(),
			Severity: domain.SeverityError,
		}
	}
	return nil
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
