package factor

import (
	"context"
	"sort"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// Engine executes factor runs against one pinned DataView. Every Run is
// request-validated, preflighted in a single pass, content-addressed,
// computed cross-sectionally over the sorted universe and staged then
// published into the cache — a failed or cancelled run never leaves a
// hit behind.
type Engine struct {
	registry *Registry
	view     ports.DataView
	cache    *Cache
}

// NewEngine binds a registry, one point-in-time view and a cache. All
// three are required: a run without preflight data or without a cache is
// not meaningful.
// AvailabilityPolicyAvailableAt names the DataView availability semantics
// every factor computation runs under (available_at <= as_of). It is part of
// the cache key, so its spelling is single-sourced here rather than repeated
// by each caller.
const AvailabilityPolicyAvailableAt = "available_at"

func NewEngine(registry *Registry, view ports.DataView, cache *Cache) (*Engine, error) {
	if registry == nil {
		return nil, domain.NewError(domain.CodeValidationInvalid, "factor: registry is required")
	}
	if view == nil {
		return nil, domain.NewError(domain.CodeValidationInvalid, "factor: view is required")
	}
	if cache == nil {
		return nil, domain.NewError(domain.CodeValidationInvalid, "factor: cache is required")
	}
	return &Engine{registry: registry, view: view, cache: cache}, nil
}

// Frame is one factor's output over the universe: per member a value or a
// missing reason, plus the as_of the frame was computed at.
type Frame struct {
	Ref     FactorRef
	AsOf    time.Time
	Values  map[domain.ID]domain.Decimal
	Missing map[domain.ID]string
}

// complete checks the frame invariant — every member carries exactly one
// of value or missing reason. It is the last gate before a frame is
// staged, so an internal bug fails closed instead of publishing a
// half-populated partition.
func (f *Frame) complete(members []domain.ID) error {
	for _, m := range members {
		_, hasValue := f.Values[m]
		_, hasMissing := f.Missing[m]
		if hasValue == hasMissing {
			return domain.NewError(codeComputeFailed, "factor: %s member %s must carry exactly one of value or missing reason", f.Ref, m)
		}
	}
	return nil
}

// memberData is one member's loaded input state.
type memberData struct {
	// points are the usable (PIT-clean, decodable) observations, ordered
	// by event time ascending.
	points []Point
	// anyRows records that the dataset had rows for the member even when
	// none were usable — distinguishing pit_unverified from
	// missing_input.
	anyRows bool
	// pitRejected counts rows dropped by the PIT policy.
	pitRejected int
}

// inputData is one input's loaded state over the whole universe.
type inputData struct {
	byMember  map[domain.ID]*memberData
	rows      int  // winner rows scanned (before field/PIT filters)
	fieldSeen bool // the requested field appeared at least once
}

func newInputData() *inputData {
	return &inputData{byMember: make(map[domain.ID]*memberData)}
}

// usable returns how many usable points member m has.
func (d *inputData) usable(m domain.ID) int {
	if md := d.byMember[m]; md != nil {
		return len(md.points)
	}
	return 0
}

// loadInput pages through the view for one input over all members and
// builds the usable per-member point series. Corrupt decimals fail
// closed — stored damage must surface, not silently become a missing
// value.
func (e *Engine) loadInput(ctx context.Context, input *Input, req RunRequest, members []domain.ID) (*inputData, error) {
	query := domain.DataQuery{
		SnapshotID:    e.view.SnapshotID(),
		AsOf:          e.view.AsOf(),
		Dataset:       input.Dataset,
		Frequency:     input.Frequency,
		InstrumentIDs: members,
		Fields:        []string{input.Field},
		Range:         req.Range,
	}
	data := newInputData()
	for {
		page, err := e.view.Query(ctx, query)
		if err != nil {
			return nil, domain.Wrap(err, domain.CodeInternalError, "factor: query input %q (dataset %q) failed", input.Name, input.Dataset)
		}
		for i := range page.Items {
			obs := &page.Items[i]
			if obs.InstrumentID == nil {
				continue // entity-only rows carry no member value
			}
			member := *obs.InstrumentID
			data.rows++
			md := data.byMember[member]
			if md == nil {
				md = &memberData{}
				data.byMember[member] = md
			}
			md.anyRows = true
			value, has := obs.Values[input.Field]
			if !has {
				continue
			}
			data.fieldSeen = true
			if value.Kind != domain.ValueDecimal || value.MissingReason != "" {
				continue
			}
			parsed, err := domain.ParseDecimal(value.Encoded)
			if err != nil {
				return nil, domain.Wrap(err, domain.CodeInternalError,
					"factor: dataset %q field %q member %s carries a corrupt decimal %q",
					input.Dataset, input.Field, member, value.Encoded)
			}
			if input.PIT && (obs.Provenance.PublishedAt == nil || containsString(obs.Provenance.QualityFlags, pitUnverifiedFlag)) {
				md.pitRejected++
				continue
			}
			md.points = append(md.points, Point{Time: obs.EventTime, Value: parsed})
		}
		if page.NextCursor == "" {
			break
		}
		query.Cursor = page.NextCursor
	}
	// The store already orders by event time, but pagination reassembly
	// and future view implementations must not be trusted with ordering —
	// computes index points positionally.
	for _, md := range data.byMember {
		sort.Slice(md.points, func(i, j int) bool { return md.points[i].Time.Before(md.points[j].Time) })
	}
	return data, nil
}

// validateRequest checks the run contract that does not involve data:
// ref and range shape, pinned cache-key inputs, and the window ending
// exactly at the view's as_of (a window beyond the decision time would
// read rows the view already filters, silently changing semantics).
func (e *Engine) validateRequest(req RunRequest) error {
	if err := req.Ref.Validate(); err != nil {
		return domain.Wrap(err, codeRequestInvalid, "factor: request ref invalid")
	}
	if _, err := domain.NewInterval(req.Range.From, req.Range.To); err != nil {
		return domain.Wrap(err, codeRequestInvalid, "factor: request range invalid")
	}
	if req.UniverseID == "" || req.UniverseHash == "" || req.SnapshotHash == "" || req.AvailabilityPolicy == "" {
		return domain.NewError(codeRequestInvalid, "factor: request requires universe_id, universe_hash, snapshot_hash and availability_policy")
	}
	if !req.Range.To.Equal(e.view.AsOf()) {
		return domain.NewError(codeRequestInvalid,
			"factor: request range end %s must equal the view's as_of %s",
			req.Range.To.UTC().Format(time.RFC3339Nano), e.view.AsOf().UTC().Format(time.RFC3339Nano))
	}
	return nil
}

// Run executes one factor run end to end. Order: request validation,
// single-pass preflight (every problem at once, inputs read once), cache
// lookup, cross-sectional computation with dependency frames,
// completeness check, then stage and publish.
func (e *Engine) Run(ctx context.Context, req RunRequest) (*Frame, error) {
	spec, err := e.registry.Lookup(req.Ref)
	if err != nil {
		return nil, err
	}
	if err := e.validateRequest(req); err != nil {
		return nil, err
	}
	members := sortedUnique(req.Members)

	var problems []Problem
	problems = append(problems, e.registry.ValidateGraph(req.Ref)...)
	params, paramProblems := canonicalParams(spec, req.Params)
	if len(paramProblems) > 0 {
		for i := range paramProblems {
			paramProblems[i].Ref = req.Ref
		}
		return nil, preflightError(append(problems, paramProblems...))
	}
	if len(members) == 0 {
		problems = append(problems, Problem{Ref: req.Ref, Code: ProblemUniverseEmpty, Message: "universe is empty"})
		return nil, preflightError(problems)
	}

	// One pass loads data and collects problems together; the loaded
	// series are reused for computation so each input is read once.
	loaded := make(map[string]map[domain.ID]*memberData, len(spec.Inputs))
	for i := range spec.Inputs {
		data, inputProblems, err := e.checkInput(ctx, &spec.Inputs[i], req, params, members)
		if err != nil {
			return nil, err
		}
		problems = append(problems, inputProblems...)
		perMember := make(map[domain.ID]*memberData, len(data.byMember))
		for m, md := range data.byMember {
			perMember[m] = md
		}
		loaded[spec.Inputs[i].Name] = perMember
	}
	if len(problems) > 0 {
		return nil, preflightError(problems)
	}

	key, err := e.registry.CacheKey(CacheKeyRequest{
		SnapshotHash:       req.SnapshotHash,
		UniverseID:         req.UniverseID,
		UniverseHash:       req.UniverseHash,
		Ref:                req.Ref,
		Params:             params,
		Range:              req.Range,
		AvailabilityPolicy: req.AvailabilityPolicy,
	})
	if err != nil {
		return nil, err
	}
	if cached, ok := e.cache.Get(key); ok {
		return cached, nil
	}

	memo := make(map[string]*Frame)
	frame, err := e.frameFor(ctx, spec, req, params, members, memo, loaded)
	if err != nil {
		return nil, err
	}
	if err := frame.complete(members); err != nil {
		return nil, err
	}

	e.cache.Stage(key, frame)
	if err := e.cache.Publish(key); err != nil {
		e.cache.Abort(key)
		return nil, err
	}
	return frame, nil
}

// frameFor computes one spec's frame over the members, recursing into
// dependencies. preloaded carries the input series Run's preflight pass
// already loaded (input name -> member data); dependencies load their
// own by passing nil. memo de-duplicates shared subgraphs within one
// run. Only the top-level frame enters the cache; dependency frames are
// run-scoped.
func (e *Engine) frameFor(ctx context.Context, spec *Spec, req RunRequest, params map[string]any, members []domain.ID, memo map[string]*Frame, preloaded map[string]map[domain.ID]*memberData) (*Frame, error) {
	ref := FactorRef{ID: spec.ID, Version: spec.Version}
	if cached, ok := memo[ref.String()]; ok {
		return cached, nil
	}
	frame := &Frame{
		Ref:     ref,
		AsOf:    e.view.AsOf(),
		Values:  make(map[domain.ID]domain.Decimal, len(members)),
		Missing: make(map[domain.ID]string),
	}
	// Register before recursing: the graph is acyclic (validated), and
	// early registration keeps a hypothetical cycle from recursing
	// forever.
	memo[ref.String()] = frame

	var inputs map[string]map[domain.ID]*memberData
	if preloaded != nil {
		inputs = preloaded
	} else {
		inputs = make(map[string]map[domain.ID]*memberData, len(spec.Inputs))
		for i := range spec.Inputs {
			data, err := e.loadInput(ctx, &spec.Inputs[i], req, members)
			if err != nil {
				return nil, err
			}
			perMember := make(map[domain.ID]*memberData, len(data.byMember))
			for m, md := range data.byMember {
				perMember[m] = md
			}
			inputs[spec.Inputs[i].Name] = perMember
		}
	}

	depFrames := make(map[string]*Frame, len(spec.Deps))
	for _, dep := range spec.Deps {
		depSpec, err := e.registry.Lookup(dep)
		if err != nil {
			return nil, err
		}
		depFrame, err := e.frameFor(ctx, depSpec, req, intersectParams(depSpec, params), members, memo, nil)
		if err != nil {
			return nil, err
		}
		depFrames[dep.String()] = depFrame
	}

	for _, m := range members {
		value, reason, err := e.evalMember(spec, params, inputs, depFrames, m)
		if err != nil {
			return nil, err
		}
		if reason != "" {
			frame.Missing[m] = reason
		} else {
			frame.Values[m] = value
		}
	}
	return frame, nil
}

// intersectParams passes request parameters through to a dependency when
// it declares a parameter of the same name (reversal's n reaching
// momentum). The values were already canonicalized against the parent
// spec; the dependency re-validates them against its own schema, so a
// same-named but incompatible parameter simply falls back to its default.
func intersectParams(spec *Spec, params map[string]any) map[string]any {
	declared := make(map[string]bool, len(spec.Params))
	for _, p := range spec.Params {
		declared[p.Name] = true
	}
	out := make(map[string]any, len(params))
	for name, value := range params {
		if declared[name] {
			out[name] = value
		}
	}
	return out
}

// evalMember resolves one member's value: the first offending input
// short-circuits with its missing reason, otherwise the builtin compute
// or expression evaluator runs.
func (e *Engine) evalMember(spec *Spec, params map[string]any, inputs map[string]map[domain.ID]*memberData, depFrames map[string]*Frame, member domain.ID) (domain.Decimal, string, error) {
	asOf := e.view.AsOf()
	for i := range spec.Inputs {
		input := &spec.Inputs[i]
		md := inputs[input.Name][member]
		if md == nil || len(md.points) == 0 {
			if md != nil && md.pitRejected > 0 {
				return domain.Decimal(""), ReasonPITUnverified, nil
			}
			return domain.Decimal(""), ReasonMissingInput, nil
		}
		if input.Staleness > 0 && md.points[len(md.points)-1].Time.Before(asOf.Add(-input.Staleness)) {
			return domain.Decimal(""), ReasonStaleInput, nil
		}
	}
	computeCtx := &ComputeContext{
		AsOf:   asOf,
		Params: params,
		Inputs: make(map[string][]Point, len(spec.Inputs)),
	}
	for i := range spec.Inputs {
		computeCtx.Inputs[spec.Inputs[i].Name] = inputs[spec.Inputs[i].Name][member].points
	}
	if spec.Kind == KindBuiltin {
		return spec.Compute(computeCtx)
	}
	return evalExpression(&evalContext{inputs: computeCtx.Inputs, deps: depFrames}, spec.Expression, member)
}

// sortedUnique returns the members sorted ascending without duplicates
// or empty ids — the canonical universe order every frame is built in.
func sortedUnique(members []domain.ID) []domain.ID {
	seen := make(map[domain.ID]bool, len(members))
	out := make([]domain.ID, 0, len(members))
	for _, m := range members {
		if m == "" || seen[m] {
			continue
		}
		seen[m] = true
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}
