package factor

import (
	"github.com/injoyai/strategy/internal/domain"
)

// RegisterDefaults registers the M1-07 builtin factor family: momentum
// (a price-only factor that runs standalone — AC-05's price factor) and
// reversal (an expression factor composing momentum's negation).
func RegisterDefaults(r *Registry) error {
	for _, spec := range []*Spec{MomentumSpec(), ReversalSpec()} {
		if err := r.Register(spec); err != nil {
			return domain.Wrap(err, codeSpecInvalid, "factor: register defaults")
		}
	}
	return nil
}

// MomentumSpec builds the n-period momentum factor: P(t)/P(t-n) - 1 over
// daily closes. It reads only the bar dataset, so it computes wherever
// prices exist — the property AC-05 contrasts with valuation-dependent
// factors.
func MomentumSpec() *Spec {
	return &Spec{
		ID:      "momentum",
		Version: "1.0.0",
		Title:   "N-period close momentum (P(t)/P(t-n) - 1)",
		Kind:    KindBuiltin,
		Params: []Param{
			{Name: "n", Type: ParamInteger, Default: int64(20), Min: ptrFloat(1), Max: ptrFloat(250)},
		},
		Inputs: []Input{
			{
				Name:      "close",
				Dataset:   "bar",
				Field:     "close",
				Frequency: "daily",
				// Momentum needs n+1 points to form the n-period ratio.
				// The window is parameterized, so it resolves per request.
				LookbackFor: func(params map[string]any) int { return paramInt(params, "n") + 1 },
				Unit:        "price",
				PIT:         true,
			},
		},
		OutputUnit:   "ratio",
		AssetClasses: []string{"equity"},
		Compute:      computeMomentum,
	}
}

// ReversalSpec builds the reversal factor: negated momentum, expressed as
// a restricted expression over a declared dependency. It mirrors
// momentum's n parameter so a request tunes the window once and the
// engine passes it through to the dependency by name.
func ReversalSpec() *Spec {
	momentum := FactorRef{ID: "momentum", Version: "1.0.0"}
	return &Spec{
		ID:      "reversal",
		Version: "1.0.0",
		Title:   "Reversal (negated momentum)",
		Kind:    KindExpression,
		Params: []Param{
			{Name: "n", Type: ParamInteger, Default: int64(20), Min: ptrFloat(1), Max: ptrFloat(250)},
		},
		Deps:         []FactorRef{momentum},
		Expression:   negated(momentum),
		OutputUnit:   "ratio",
		AssetClasses: []string{"equity"},
	}
}

// negated builds the neg(unary) AST over a factor reference.
func negated(ref FactorRef) *Expression {
	return &Expression{
		Kind: ExprUnary,
		Op:   "neg",
		Left: &Expression{Kind: ExprFactor, Ref: &ref},
	}
}

// computeMomentum derives P(t)/P(t-n) - 1. A zero base price is a missing
// member (invalid_denominator), never an aborted run; too few points is
// a defensive re-check — preflight already guarantees the window.
func computeMomentum(ctx *ComputeContext) (domain.Decimal, string, error) {
	n := paramInt(ctx.Params, "n")
	points := ctx.Inputs["close"]
	if len(points) < n+1 {
		return domain.Decimal(""), ReasonInsufficientHistory, nil
	}
	last := points[len(points)-1].Value
	base := points[len(points)-1-n].Value
	if base.IsZero() {
		return domain.Decimal(""), ReasonInvalidDenominator, nil
	}
	ratio, err := last.Div(base)
	if err != nil {
		return domain.Decimal(""), "", err
	}
	one, err := domain.ParseDecimal("1")
	if err != nil {
		return domain.Decimal(""), "", err
	}
	value, err := ratio.Sub(one)
	if err != nil {
		return domain.Decimal(""), "", err
	}
	return value, "", nil
}

// paramInt reads an integer parameter canonicalized to int64.
func paramInt(params map[string]any, name string) int {
	if v, ok := params[name].(int64); ok {
		return int(v)
	}
	return 0
}

// ptrFloat boxes a bound for Param.Min/Max.
func ptrFloat(v float64) *float64 { return &v }
