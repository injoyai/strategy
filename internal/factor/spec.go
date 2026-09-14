// Package factor implements the factor registry, dependency DAG, run
// preflight and staged result cache (design D5 in
// docs/implementation/m1-data-factor.md). Go-written builtin factors and
// restricted expression factors share one registry; every run is
// preflighted against the pinned data view and its result is
// content-addressed so reruns hit the cache.
//
// The package owns no storage: registration is an in-process concern and
// the cache is an in-process staging area, mirroring how screening keeps
// its policy in memory.
package factor

import (
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// Kind distinguishes hand-written Go factors (KindBuiltin, Compute set)
// from restricted expression factors (KindExpression, Expression set).
// Both register into the same DAG and are interchangeable as
// dependencies.
type Kind string

const (
	KindBuiltin    Kind = "builtin"
	KindExpression Kind = "expression"
)

// FactorRef addresses one registered factor by id and version. String is
// "id@version" — the key used by memo maps, dep frames and messages.
// The JSON tags make refs self-describing wherever a ref is embedded in
// an artifact payload (e.g. the D6 analysis artifact config).
type FactorRef struct {
	ID      string `json:"id"`
	Version string `json:"version"`
}

// String renders "id@version".
func (r FactorRef) String() string { return r.ID + "@" + r.Version }

// Validate checks the ref's shape; registered-ness is the registry's job.
func (r FactorRef) Validate() error {
	if r.ID == "" {
		return domain.NewError(domain.CodeValidationInvalid, "factor: ref id is required")
	}
	if r.Version == "" {
		return domain.NewError(domain.CodeValidationInvalid, "factor: ref %q is missing a version", r.ID)
	}
	return nil
}

// Point is one usable (PIT-clean, decodable) observation of an input at
// one event time. A member's points are ordered ascending by time.
type Point struct {
	Time  time.Time
	Value domain.Decimal
}

// ComputeContext carries everything a builtin compute may see for one
// member: the decision time, the canonical parameters and the usable
// points per declared input.
type ComputeContext struct {
	AsOf   time.Time
	Params map[string]any
	Inputs map[string][]Point
}

// ComputeFunc evaluates one instrument. A non-empty reason marks the
// member missing (recoverable, part of the frame); an error aborts the
// whole run — compute authors must classify recoverable situations as
// reasons, not errors.
type ComputeFunc func(*ComputeContext) (domain.Decimal, string, error)

// Input declares one data dependency: where to read (dataset, field,
// frequency), how many usable points the compute needs, the unit contract
// and the PIT / staleness policies. Unit is mandatory so unit-mismatched
// arithmetic is caught at registration, never mid-run.
type Input struct {
	Name      string
	Dataset   string
	Field     string
	Frequency string
	// Lookback is the static number of usable points required. Zero means
	// "whatever the latest value is" (last-value factors); a member with
	// no points at all is then reported missing_input.
	Lookback int
	// LookbackFor resolves a parameterized window per request (momentum
	// needs n+1 closes). It wins over Lookback when set. It is a Go
	// closure, so the definition hash cannot see it — RuntimeVersion
	// covers its semantics.
	LookbackFor func(params map[string]any) int
	Unit        string
	// PIT marks the input as requiring verified point-in-time evidence:
	// rows without published_at or flagged pit_unverified are excluded
	// and reported.
	PIT bool
	// Staleness rejects members whose latest point is older than
	// as_of - staleness (stale_input).
	Staleness time.Duration
}

// EffectiveLookback resolves the required point count for one request.
func (i Input) EffectiveLookback(params map[string]any) int {
	if i.LookbackFor != nil {
		return i.LookbackFor(params)
	}
	return i.Lookback
}

// Spec is one registered factor definition.
type Spec struct {
	ID           string
	Version      string
	Title        string
	Kind         Kind
	Params       []Param
	Inputs       []Input
	Deps         []FactorRef
	OutputUnit   string
	AssetClasses []string
	// Expression must be set for KindExpression (and only then).
	Expression *Expression
	// Compute must be set for KindBuiltin (and only then).
	Compute ComputeFunc
}

// validate checks the spec's local shape. Dependencies are checked for
// existence here too — a spec whose deps are not yet registered is a
// registration error, not a deferred graph problem.
func (s *Spec) validate(lookup func(FactorRef) *Spec) error {
	if s == nil {
		return domain.NewError(codeSpecInvalid, "factor: spec is nil")
	}
	if s.ID == "" {
		return domain.NewError(codeSpecInvalid, "factor: spec id is required")
	}
	if s.Version == "" {
		return domain.NewError(codeSpecInvalid, "factor: spec %q is missing a version", s.ID)
	}
	if s.Title == "" {
		return domain.NewError(codeSpecInvalid, "factor: spec %s is missing a title", s.ID)
	}
	if s.OutputUnit == "" {
		return domain.NewError(codeSpecInvalid, "factor: spec %s is missing an output unit", s.ID)
	}
	if len(s.AssetClasses) == 0 {
		return domain.NewError(codeSpecInvalid, "factor: spec %s must declare at least one asset class", s.ID)
	}
	switch s.Kind {
	case KindBuiltin:
		if s.Compute == nil {
			return domain.NewError(codeSpecInvalid, "factor: builtin spec %s requires a compute function", s.ID)
		}
		if s.Expression != nil {
			return domain.NewError(codeSpecInvalid, "factor: builtin spec %s must not carry an expression", s.ID)
		}
	case KindExpression:
		if s.Expression == nil {
			return domain.NewError(codeSpecInvalid, "factor: expression spec %s requires an expression", s.ID)
		}
		if s.Compute != nil {
			return domain.NewError(codeSpecInvalid, "factor: expression spec %s must not carry a compute function", s.ID)
		}
		if err := validateExpression(s.Expression, s, lookup); err != nil {
			return err
		}
	default:
		return domain.NewError(codeSpecInvalid, "factor: spec %s has unknown kind %q", s.ID, s.Kind)
	}

	paramNames := make(map[string]bool, len(s.Params))
	for _, p := range s.Params {
		if err := p.validate(); err != nil {
			return err
		}
		if paramNames[p.Name] {
			return domain.NewError(codeSpecInvalid, "factor: spec %s declares parameter %q twice", s.ID, p.Name)
		}
		paramNames[p.Name] = true
	}

	inputNames := make(map[string]bool, len(s.Inputs))
	for _, in := range s.Inputs {
		if err := in.validate(); err != nil {
			return err
		}
		if inputNames[in.Name] {
			return domain.NewError(codeSpecInvalid, "factor: spec %s declares input %q twice", s.ID, in.Name)
		}
		inputNames[in.Name] = true
	}

	depKeys := make(map[string]bool, len(s.Deps))
	self := FactorRef{ID: s.ID, Version: s.Version}
	for _, dep := range s.Deps {
		if err := dep.Validate(); err != nil {
			return domain.Wrap(err, codeSpecInvalid, "factor: spec %s has an invalid dependency", s.ID)
		}
		if depKeys[dep.String()] {
			return domain.NewError(codeSpecInvalid, "factor: spec %s declares dependency %s twice", s.ID, dep)
		}
		depKeys[dep.String()] = true
		if dep == self {
			return domain.NewError(codeSpecInvalid, "factor: spec %s depends on itself", s.ID)
		}
		if lookup(dep) == nil {
			return domain.NewError(codeSpecInvalid, "factor: spec %s depends on %s which is not registered", s.ID, dep)
		}
	}
	return nil
}

// validate checks one declared input's shape.
func (i Input) validate() error {
	if i.Name == "" {
		return domain.NewError(codeSpecInvalid, "factor: input name is required")
	}
	if i.Dataset == "" {
		return domain.NewError(codeSpecInvalid, "factor: input %q is missing a dataset", i.Name)
	}
	if i.Field == "" {
		return domain.NewError(codeSpecInvalid, "factor: input %q is missing a field", i.Name)
	}
	if i.Frequency == "" {
		return domain.NewError(codeSpecInvalid, "factor: input %q is missing a frequency", i.Name)
	}
	if i.Unit == "" {
		return domain.NewError(codeSpecInvalid, "factor: input %q is missing a unit", i.Name)
	}
	if i.LookbackFor == nil && i.Lookback < 0 {
		return domain.NewError(codeSpecInvalid, "factor: input %q lookback must be non-negative", i.Name)
	}
	if i.Staleness < 0 {
		return domain.NewError(codeSpecInvalid, "factor: input %q staleness must be non-negative", i.Name)
	}
	return nil
}
