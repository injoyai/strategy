package factor

import (
	"context"
	"fmt"
	"strings"

	"github.com/injoyai/strategy/internal/domain"
)

// Problem is one preflight or graph finding: a code, a human message and
// the factor ref it belongs to. Problems are findings, not errors —
// preflight returns all of them in one round so a caller can fix the
// whole request at once.
type Problem struct {
	Ref     FactorRef
	Code    string
	Message string
}

// String renders "code: message" — the form embedded into error messages,
// because domain.Error surfaces only its message text on the wire.
func (p Problem) String() string {
	return fmt.Sprintf("%s: %s", p.Code, p.Message)
}

// preflightMaxMembers bounds the member list inside one problem message.
const preflightMaxMembers = 5

// RunRequest is one factor run: which factor, with which parameters, over
// which universe and window, against which snapshot. SnapshotHash,
// UniverseID, UniverseHash and AvailabilityPolicy feed the cache key, so
// the request must pin everything the output depends on.
type RunRequest struct {
	Ref                FactorRef
	Params             map[string]any
	Members            []domain.ID
	Range              domain.Interval
	UniverseID         string
	UniverseHash       string
	SnapshotHash       string
	AvailabilityPolicy string
	// TolerateMemberGaps accepts members that cannot be computed at this decision
	// time: instead of failing the run, they carry a missing reason in the frame.
	// Screening uses it because the members it cannot rank are a stage it already
	// reports; a factor run or an analysis keeps the strict default, where a
	// member the engine cannot compute is a problem the caller must fix.
	TolerateMemberGaps bool
}

// Tolerates reports whether this request accepts one preflight finding instead of
// failing on it. Callers that classify findings themselves must ask the request,
// not a fixed list, so what a tolerant run actually ignores has exactly one
// definition.
func (r RunRequest) Tolerates(code string) bool {
	return r.TolerateMemberGaps && code == ProblemInsufficientHistory
}

// Preflight checks a request without computing: graph closure, parameter
// schema, universe membership and per-input data availability. All
// findings are returned together; an error return signals infrastructure
// failure only, never a finding.
func (e *Engine) Preflight(ctx context.Context, req RunRequest) ([]Problem, error) {
	spec, err := e.registry.Lookup(req.Ref)
	if err != nil {
		return nil, err
	}
	var problems []Problem
	problems = append(problems, e.registry.ValidateGraph(req.Ref)...)

	params, paramProblems := canonicalParams(spec, req.Params)
	for i := range paramProblems {
		paramProblems[i].Ref = req.Ref
	}
	problems = append(problems, paramProblems...)
	if len(paramProblems) > 0 {
		// Lookbacks can depend on parameters, so data checks would run
		// with unresolved windows — stop after reporting the schema
		// problems.
		return problems, nil
	}

	members := sortedUnique(req.Members)
	if len(members) == 0 {
		return append(problems, Problem{
			Ref:     req.Ref,
			Code:    ProblemUniverseEmpty,
			Message: "universe is empty",
		}), nil
	}

	for i := range spec.Inputs {
		_, inputProblems, err := e.checkInput(ctx, &spec.Inputs[i], req, params, members)
		if err != nil {
			return problems, err
		}
		problems = append(problems, inputProblems...)
	}
	return problems, nil
}

// checkInput loads one input's data and derives its availability
// problems. The loaded data is returned alongside so Run can reuse it and
// read each input exactly once.
func (e *Engine) checkInput(ctx context.Context, input *Input, req RunRequest, params map[string]any, members []domain.ID) (*inputData, []Problem, error) {
	var problems []Problem
	if !supportedFrequencies[input.Frequency] {
		problems = append(problems, Problem{
			Ref:     req.Ref,
			Code:    ProblemFrequencyUnsupported,
			Message: fmt.Sprintf("input %q frequency %q is not supported", input.Name, input.Frequency),
		})
		// No query can succeed for an unsupported frequency; return the
		// empty shape so callers can keep iterating.
		return newInputData(), problems, nil
	}
	data, err := e.loadInput(ctx, input, req, members)
	if err != nil {
		return nil, problems, err
	}
	lookback := input.EffectiveLookback(params)
	if data.rows == 0 {
		problems = append(problems, Problem{
			Ref:     req.Ref,
			Code:    ProblemDatasetMissing,
			Message: fmt.Sprintf("input %q: dataset %q has no visible rows for any member in the window", input.Name, input.Dataset),
		})
		return data, problems, nil
	}
	if !data.fieldSeen {
		problems = append(problems, Problem{
			Ref:     req.Ref,
			Code:    ProblemFieldMissing,
			Message: fmt.Sprintf("input %q: field %q never appears in dataset %q", input.Name, input.Field, input.Dataset),
		})
		return data, problems, nil
	}
	var short []string
	for _, m := range members {
		if data.usable(m) < lookback {
			short = append(short, m.String())
		}
	}
	if len(short) > 0 {
		problems = append(problems, Problem{
			Ref:     req.Ref,
			Code:    ProblemInsufficientHistory,
			Message: insufficientHistoryMessage(input, lookback, short),
		})
	}
	if input.PIT {
		unverified := 0
		for _, md := range data.byMember {
			unverified += md.pitRejected
		}
		if unverified > 0 {
			problems = append(problems, Problem{
				Ref:  req.Ref,
				Code: ProblemPITUnverified,
				Message: fmt.Sprintf("input %q: %d row(s) lack point-in-time verification (no published_at or flagged %s)",
					input.Name, unverified, pitUnverifiedFlag),
			})
		}
	}
	return data, problems, nil
}

// insufficientHistoryMessage lists at most preflightMaxMembers offending
// members, then summarizes the rest.
func insufficientHistoryMessage(input *Input, lookback int, short []string) string {
	listed := short
	if len(listed) > preflightMaxMembers {
		listed = listed[:preflightMaxMembers]
	}
	msg := fmt.Sprintf("input %q: %d member(s) have fewer than %d usable points (%s",
		input.Name, len(short), lookback, strings.Join(listed, ", "))
	if len(short) > preflightMaxMembers {
		msg += fmt.Sprintf(", and %d more", len(short)-preflightMaxMembers)
	}
	return msg + ")"
}

// summarizeProblems renders problems as "code: message; ...".
func summarizeProblems(problems []Problem) string {
	parts := make([]string, len(problems))
	for i := range problems {
		parts[i] = problems[i].String()
	}
	return strings.Join(parts, "; ")
}

// preflightError builds the single error Run returns when preflight found
// problems; every problem is inlined into the message because
// domain.Error carries only its message text on the wire.
func preflightError(problems []Problem) error {
	return domain.NewError(codePreflightFailed, "factor: preflight failed: %s", summarizeProblems(problems))
}
