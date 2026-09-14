package factor

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// RuntimeVersion identifies the semantics of Go code invisible to the
// definition digest: builtin compute functions and LookbackFor closures.
// Bump it whenever any of those changes behavior — every cache key derives
// from it, so the change invalidates all cached results at once.
const RuntimeVersion = "factor-runtime/1"

// supportedFrequencies is the frequency whitelist for M1-07's synthetic
// scope (daily bars only). The M1-09 capability catalog replaces it.
var supportedFrequencies = map[string]bool{"daily": true}

// Registry is the in-memory factor registry: definition lookup, DAG
// validation, content-addressed definition hashes and cache keys. It owns
// no storage — registration is a process-lifetime concern, mirroring
// screening's in-memory policy.
type Registry struct {
	checksummer ports.Checksummer
	specs       map[string]*Spec // key: specKey
}

// NewRegistry builds an empty registry.
func NewRegistry(checksummer ports.Checksummer) (*Registry, error) {
	if checksummer == nil {
		return nil, domain.NewError(domain.CodeValidationInvalid, "factor: checksummer is required")
	}
	return &Registry{checksummer: checksummer, specs: make(map[string]*Spec)}, nil
}

// specKey is the internal map key for one spec.
func specKey(id, version string) string { return id + "\x00" + version }

// Register validates the spec, inserts it and then validates the full
// dependency closure — on failure the insertion is rolled back so a bad
// registration never poisons the graph.
func (r *Registry) Register(spec *Spec) error {
	lookup := func(ref FactorRef) *Spec {
		return r.specs[specKey(ref.ID, ref.Version)]
	}
	if err := spec.validate(lookup); err != nil {
		return err
	}
	key := specKey(spec.ID, spec.Version)
	if _, dup := r.specs[key]; dup {
		return domain.NewError(codeDuplicateFactor, "factor: %s@%s is already registered", spec.ID, spec.Version)
	}
	r.specs[key] = spec
	if problems := r.ValidateGraph(FactorRef{ID: spec.ID, Version: spec.Version}); len(problems) > 0 {
		delete(r.specs, key)
		return domain.NewError(codeSpecInvalid, "factor: %s@%s has dependency problems: %s", spec.ID, spec.Version, summarizeProblems(problems))
	}
	return nil
}

// Lookup returns the spec for ref.
func (r *Registry) Lookup(ref FactorRef) (*Spec, error) {
	if err := ref.Validate(); err != nil {
		return nil, err
	}
	spec, ok := r.specs[specKey(ref.ID, ref.Version)]
	if !ok {
		return nil, domain.NewError(codeNotRegistered, "factor: %s is not registered", ref)
	}
	return spec, nil
}

// List returns all registered specs ordered by (id, version).
func (r *Registry) List() []Spec {
	out := make([]Spec, 0, len(r.specs))
	for _, spec := range r.specs {
		out = append(out, *spec)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].ID != out[j].ID {
			return out[i].ID < out[j].ID
		}
		return out[i].Version < out[j].Version
	})
	return out
}

// ValidateGraph walks the dependency closure of root and reports every
// cycle (and, defensively, unresolvable dependencies). It is public so
// Preflight can surface graph problems attached to the run's ref.
func (r *Registry) ValidateGraph(root FactorRef) []Problem {
	spec, ok := r.specs[specKey(root.ID, root.Version)]
	if !ok {
		return []Problem{{
			Ref:     root,
			Code:    ProblemDepMissing,
			Message: fmt.Sprintf("factor %s is not registered", root),
		}}
	}
	const (
		unvisited = 0
		inStack   = 1
		done      = 2
	)
	state := make(map[string]int)
	var problems []Problem
	var visit func(ref FactorRef, spec *Spec)
	visit = func(ref FactorRef, spec *Spec) {
		key := specKey(ref.ID, ref.Version)
		switch state[key] {
		case inStack:
			problems = append(problems, Problem{
				Ref:     ref,
				Code:    ProblemCycle,
				Message: fmt.Sprintf("dependency cycle through %s", ref),
			})
			return
		case done:
			return
		}
		state[key] = inStack
		for _, dep := range spec.Deps {
			depSpec, ok := r.specs[specKey(dep.ID, dep.Version)]
			if !ok {
				problems = append(problems, Problem{
					Ref:     dep,
					Code:    ProblemDepMissing,
					Message: fmt.Sprintf("factor %s is not registered", dep),
				})
				continue
			}
			visit(dep, depSpec)
		}
		state[key] = done
	}
	visit(root, spec)
	return problems
}

// definitionFile is the JSON shape of a spec for hashing. Function fields
// (Compute, LookbackFor) are excluded — RuntimeVersion covers them.
type definitionFile struct {
	ID           string      `json:"id"`
	Version      string      `json:"version"`
	Title        string      `json:"title"`
	Kind         string      `json:"kind"`
	Params       []paramFile `json:"params"`
	Inputs       []inputFile `json:"inputs"`
	Deps         []string    `json:"deps"`
	OutputUnit   string      `json:"output_unit"`
	AssetClasses []string    `json:"asset_classes"`
	Expression   *Expression `json:"expression,omitempty"`
}

type paramFile struct {
	Name     string   `json:"name"`
	Type     string   `json:"type"`
	Required bool     `json:"required"`
	Default  any      `json:"default,omitempty"`
	Min      *float64 `json:"min,omitempty"`
	Max      *float64 `json:"max,omitempty"`
	Enum     []string `json:"enum,omitempty"`
}

type inputFile struct {
	Name      string `json:"name"`
	Dataset   string `json:"dataset"`
	Field     string `json:"field"`
	Frequency string `json:"frequency"`
	Lookback  int    `json:"lookback"`
	Unit      string `json:"unit"`
	PIT       bool   `json:"pit"`
	Staleness string `json:"staleness,omitempty"`
}

// DefinitionHash digests a spec's declarative content. Inputs keep
// declaration order (the ordering is part of the contract); deps are
// sorted so insertion order never changes the hash. The same definition
// always hashes identically.
func (r *Registry) DefinitionHash(ref FactorRef) (string, error) {
	spec, err := r.Lookup(ref)
	if err != nil {
		return "", err
	}
	file := definitionFile{
		ID:           spec.ID,
		Version:      spec.Version,
		Title:        spec.Title,
		Kind:         string(spec.Kind),
		Params:       make([]paramFile, len(spec.Params)),
		Inputs:       make([]inputFile, len(spec.Inputs)),
		OutputUnit:   spec.OutputUnit,
		AssetClasses: spec.AssetClasses,
		Expression:   spec.Expression,
	}
	for i, p := range spec.Params {
		file.Params[i] = paramFile{
			Name:     p.Name,
			Type:     string(p.Type),
			Required: p.Required,
			Default:  p.Default,
			Min:      p.Min,
			Max:      p.Max,
			Enum:     p.Enum,
		}
	}
	for i, in := range spec.Inputs {
		file.Inputs[i] = inputFile{
			Name:      in.Name,
			Dataset:   in.Dataset,
			Field:     in.Field,
			Frequency: in.Frequency,
			Lookback:  in.Lookback,
			Unit:      in.Unit,
			PIT:       in.PIT,
		}
		if in.Staleness != 0 {
			file.Inputs[i].Staleness = in.Staleness.String()
		}
	}
	deps := make([]string, len(spec.Deps))
	for i, dep := range spec.Deps {
		deps[i] = dep.String()
	}
	sort.Strings(deps)
	file.Deps = deps

	encoded, err := json.Marshal(file)
	if err != nil {
		return "", domain.Wrap(err, domain.CodeInternalError, "factor: encode definition of %s", ref)
	}
	return r.checksummer.Checksum(encoded), nil
}

// CanonicalParams validates supplied parameters against the spec and
// returns the canonical typed map with defaults filled in. Analysis
// uses it to freeze, fail-closed, the exact parameters an artifact was
// computed with; the coercion rules are the ones CacheKey shares, so a
// canonical map here hashes identically there.
func (r *Registry) CanonicalParams(ref FactorRef, supplied map[string]any) (map[string]any, error) {
	spec, err := r.Lookup(ref)
	if err != nil {
		return nil, err
	}
	params, problems := canonicalParams(spec, supplied)
	if len(problems) > 0 {
		return nil, domain.NewError(codeRequestInvalid, "factor: params for %s invalid: %s", ref, summarizeProblems(problems))
	}
	return params, nil
}

// CacheKeyRequest carries everything that determines one run's output:
// the snapshot content, the universe selection, the factor, the
// parameters, the window and the availability policy. Any field change
// must yield a different key.
type CacheKeyRequest struct {
	SnapshotHash       string
	UniverseID         string
	UniverseHash       string
	Ref                FactorRef
	Params             map[string]any
	Range              domain.Interval
	AvailabilityPolicy string
}

// cacheKeyPayload is the digested shape. Params is a map, so encoding
// sorts keys — parameter key order never affects the key.
type cacheKeyPayload struct {
	DefinitionHash     string         `json:"definition_hash"`
	RuntimeVersion     string         `json:"runtime_version"`
	SnapshotHash       string         `json:"snapshot_hash"`
	UniverseID         string         `json:"universe_id"`
	UniverseHash       string         `json:"universe_hash"`
	Frequency          string         `json:"frequency"`
	Params             map[string]any `json:"params"`
	From               string         `json:"from"`
	To                 string         `json:"to"`
	AvailabilityPolicy string         `json:"availability_policy"`
}

// CacheKey digests one run's identity. Parameters are canonicalized
// first, so an explicit default and an omitted parameter produce the same
// key.
func (r *Registry) CacheKey(req CacheKeyRequest) (string, error) {
	if err := req.Ref.Validate(); err != nil {
		return "", domain.Wrap(err, codeRequestInvalid, "factor: cache key ref invalid")
	}
	if req.SnapshotHash == "" || req.UniverseID == "" || req.UniverseHash == "" || req.AvailabilityPolicy == "" {
		return "", domain.NewError(codeRequestInvalid, "factor: cache key requires snapshot_hash, universe_id, universe_hash and availability_policy")
	}
	if _, err := domain.NewInterval(req.Range.From, req.Range.To); err != nil {
		return "", domain.Wrap(err, codeRequestInvalid, "factor: cache key range invalid")
	}
	spec, err := r.Lookup(req.Ref)
	if err != nil {
		return "", err
	}
	definitionHash, err := r.DefinitionHash(req.Ref)
	if err != nil {
		return "", err
	}
	params, problems := canonicalParams(spec, req.Params)
	if len(problems) > 0 {
		return "", domain.NewError(codeRequestInvalid, "factor: cache key params invalid: %s", summarizeProblems(problems))
	}
	payload := cacheKeyPayload{
		DefinitionHash:     definitionHash,
		RuntimeVersion:     RuntimeVersion,
		SnapshotHash:       req.SnapshotHash,
		UniverseID:         req.UniverseID,
		UniverseHash:       req.UniverseHash,
		Frequency:          specFrequency(spec),
		Params:             params,
		From:               req.Range.From.UTC().Format(time.RFC3339Nano),
		To:                 req.Range.To.UTC().Format(time.RFC3339Nano),
		AvailabilityPolicy: req.AvailabilityPolicy,
	}
	encoded, err := json.Marshal(payload)
	if err != nil {
		return "", domain.Wrap(err, domain.CodeInternalError, "factor: encode cache key")
	}
	return r.checksummer.Checksum(encoded), nil
}

// specFrequency derives the run frequency from the spec's inputs: the
// single declared frequency, "" when the factor reads no data, "mixed"
// when inputs disagree — all three distinguish cache entries.
func specFrequency(spec *Spec) string {
	frequency := ""
	for i, in := range spec.Inputs {
		if i == 0 {
			frequency = in.Frequency
			continue
		}
		if in.Frequency != frequency {
			return "mixed"
		}
	}
	return frequency
}
