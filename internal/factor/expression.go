package factor

import (
	"math"
	"strconv"

	"github.com/injoyai/strategy/internal/domain"
)

// ExprKind enumerates the restricted expression AST node kinds. Only
// these shapes exist; anything else fails registration.
type ExprKind string

const (
	ExprLiteral ExprKind = "literal"
	ExprInput   ExprKind = "input"
	ExprFactor  ExprKind = "factor"
	ExprBinary  ExprKind = "binary"
	ExprUnary   ExprKind = "unary"
	ExprCall    ExprKind = "call"
)

// maxExpressionDepth bounds AST depth at registration so a hand-assembled
// deep tree cannot exhaust evaluation.
const maxExpressionDepth = 32

// binaryOps is the operator whitelist.
var binaryOps = map[string]bool{"+": true, "-": true, "*": true, "/": true}

// callArity is the function whitelist with arities.
var callArity = map[string]int{"abs": 1, "log": 1, "sqrt": 1, "min": 2, "max": 2}

// Expression is one node of the restricted expression language. Number is
// the literal value (nil is invalid); Name addresses an input (ExprInput)
// or a function (ExprCall); Ref addresses a declared dependency
// (ExprFactor, which must also appear in Spec.Deps); Op/Left/Right/Args
// carry children.
type Expression struct {
	Kind   ExprKind
	Number *float64
	Name   string
	Ref    *FactorRef
	Op     string
	Left   *Expression
	Right  *Expression
	Args   []*Expression
}

// expressionDepth returns the depth of the tree in node levels.
func expressionDepth(e *Expression) int {
	if e == nil {
		return 0
	}
	depth := 0
	for _, child := range expressionChildren(e) {
		if d := expressionDepth(child); d > depth {
			depth = d
		}
	}
	return depth + 1
}

// expressionChildren lists a node's children for depth and walk logic.
func expressionChildren(e *Expression) []*Expression {
	switch e.Kind {
	case ExprUnary:
		return []*Expression{e.Left}
	case ExprBinary:
		return []*Expression{e.Left, e.Right}
	case ExprCall:
		return e.Args
	}
	return nil
}

// validateExpression checks the whole tree against the whitelist: node
// shapes, declared inputs, registered and declared dependencies, depth,
// and the root unit against the spec's output unit.
func validateExpression(root *Expression, spec *Spec, lookup func(FactorRef) *Spec) error {
	if root == nil {
		return domain.NewError(codeSpecInvalid, "factor: expression is required")
	}
	if expressionDepth(root) > maxExpressionDepth {
		return domain.NewError(codeSpecInvalid, "factor: expression depth exceeds %d", maxExpressionDepth)
	}
	var walk func(e *Expression) error
	walk = func(e *Expression) error {
		switch e.Kind {
		case ExprLiteral:
			if e.Number == nil {
				return domain.NewError(codeSpecInvalid, "factor: literal node requires a number")
			}
		case ExprInput:
			if e.Name == "" {
				return domain.NewError(codeSpecInvalid, "factor: input node requires a name")
			}
			if !declaredInput(spec, e.Name) {
				return domain.NewError(codeSpecInvalid, "factor: expression references undeclared input %q", e.Name)
			}
		case ExprFactor:
			if e.Ref == nil {
				return domain.NewError(codeSpecInvalid, "factor: factor node requires a ref")
			}
			if err := e.Ref.Validate(); err != nil {
				return domain.Wrap(err, codeSpecInvalid, "factor: factor node ref invalid")
			}
			if lookup(*e.Ref) == nil {
				return domain.NewError(codeSpecInvalid, "factor: expression references unregistered factor %s", e.Ref)
			}
			if !declaredDep(spec, *e.Ref) {
				return domain.NewError(codeSpecInvalid, "factor: expression references %s which is not declared in deps", e.Ref)
			}
		case ExprBinary:
			if !binaryOps[e.Op] {
				return domain.NewError(codeSpecInvalid, "factor: unknown binary operator %q", e.Op)
			}
			if e.Left == nil || e.Right == nil {
				return domain.NewError(codeSpecInvalid, "factor: binary node %q requires both operands", e.Op)
			}
		case ExprUnary:
			if e.Op != "neg" {
				return domain.NewError(codeSpecInvalid, "factor: unknown unary operator %q", e.Op)
			}
			if e.Left == nil {
				return domain.NewError(codeSpecInvalid, "factor: unary node requires an operand")
			}
		case ExprCall:
			arity, ok := callArity[e.Name]
			if !ok {
				return domain.NewError(codeSpecInvalid, "factor: unknown function %q", e.Name)
			}
			if len(e.Args) != arity {
				return domain.NewError(codeSpecInvalid, "factor: function %q takes %d argument(s), got %d", e.Name, arity, len(e.Args))
			}
		default:
			return domain.NewError(codeSpecInvalid, "factor: unknown expression kind %q", e.Kind)
		}
		for _, child := range expressionChildren(e) {
			if err := walk(child); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return err
	}
	unit, err := unitOf(root, spec, lookup)
	if err != nil {
		return err
	}
	if unit != spec.OutputUnit {
		return domain.NewError(codeSpecInvalid, "factor: expression unit %q does not match output unit %q", unit, spec.OutputUnit)
	}
	return nil
}

// unitOf derives a node's unit. Literals are dimensionless; inputs and
// dependencies carry their declared units; neg inherits; + and - require
// the operands to agree (a dimensionless side adopts the other's unit);
// * and / produce a dimensionless result — the whitelist exists for
// ratio-style composition, so full dimension algebra is deliberately out
// of scope and mismatches fail registration instead. abs/min/max inherit
// their first argument's unit; log/sqrt are dimensionless.
func unitOf(e *Expression, spec *Spec, lookup func(FactorRef) *Spec) (string, error) {
	switch e.Kind {
	case ExprLiteral:
		return "", nil
	case ExprInput:
		for _, in := range spec.Inputs {
			if in.Name == e.Name {
				return in.Unit, nil
			}
		}
		return "", domain.NewError(codeSpecInvalid, "factor: expression references undeclared input %q", e.Name)
	case ExprFactor:
		dep := lookup(*e.Ref)
		if dep == nil {
			return "", domain.NewError(codeSpecInvalid, "factor: expression references unregistered factor %s", e.Ref)
		}
		return dep.OutputUnit, nil
	case ExprUnary:
		return unitOf(e.Left, spec, lookup)
	case ExprBinary:
		left, err := unitOf(e.Left, spec, lookup)
		if err != nil {
			return "", err
		}
		right, err := unitOf(e.Right, spec, lookup)
		if err != nil {
			return "", err
		}
		switch e.Op {
		case "+", "-":
			switch {
			case left == "":
				return right, nil
			case right == "":
				return left, nil
			case left != right:
				return "", domain.NewError(codeSpecInvalid, "factor: operator %q mixes units %q and %q", e.Op, left, right)
			}
			return left, nil
		default: // "*" and "/"
			return "", nil
		}
	case ExprCall:
		for _, arg := range e.Args {
			if _, err := unitOf(arg, spec, lookup); err != nil {
				return "", err
			}
		}
		switch e.Name {
		case "abs", "min", "max":
			return unitOf(e.Args[0], spec, lookup)
		default: // log, sqrt
			return "", nil
		}
	}
	return "", domain.NewError(codeSpecInvalid, "factor: unknown expression kind %q", e.Kind)
}

// evalContext is everything the evaluator sees for one member.
type evalContext struct {
	inputs map[string][]Point
	deps   map[string]*Frame
}

// wrapArith adapts a decimal arithmetic result to the evaluator's
// (value, reason, error) shape — arithmetic failures abort the run, they
// are never silently coerced into a missing member.
func wrapArith(v domain.Decimal, err error) (domain.Decimal, string, error) {
	if err != nil {
		return "", "", err
	}
	return v, "", nil
}

// evalExpression evaluates one node for one member. A non-empty reason
// marks the member missing (no input points, dependency missing, zero
// divisor, out-of-domain math); an error aborts the run (arithmetic
// overflow, unresolved state).
func evalExpression(ctx *evalContext, e *Expression, member domain.ID) (domain.Decimal, string, error) {
	switch e.Kind {
	case ExprLiteral:
		// 'f' with -1 precision never uses exponent notation, so the
		// float literal stays inside ParseDecimal's plain grammar.
		v, err := domain.ParseDecimal(strconv.FormatFloat(*e.Number, 'f', -1, 64))
		if err != nil {
			return "", "", err
		}
		return v, "", nil
	case ExprInput:
		points := ctx.inputs[e.Name]
		if len(points) == 0 {
			return "", ReasonMissingInput, nil
		}
		return points[len(points)-1].Value, "", nil
	case ExprFactor:
		frame := ctx.deps[e.Ref.String()]
		if frame == nil {
			return "", "", domain.NewError(codeComputeFailed, "factor: dependency %s was not resolved", e.Ref)
		}
		if reason, ok := frame.Missing[member]; ok {
			return "", reason, nil
		}
		v, ok := frame.Values[member]
		if !ok {
			return "", ReasonMissingInput, nil
		}
		return v, "", nil
	case ExprUnary:
		v, reason, err := evalExpression(ctx, e.Left, member)
		if err != nil || reason != "" {
			return "", reason, err
		}
		zero, err := domain.ParseDecimal("0")
		if err != nil {
			return "", "", err
		}
		return wrapArith(zero.Sub(v))
	case ExprBinary:
		a, reason, err := evalExpression(ctx, e.Left, member)
		if err != nil || reason != "" {
			return "", reason, err
		}
		b, reason, err := evalExpression(ctx, e.Right, member)
		if err != nil || reason != "" {
			return "", reason, err
		}
		switch e.Op {
		case "+":
			return wrapArith(a.Add(b))
		case "-":
			return wrapArith(a.Sub(b))
		case "*":
			return wrapArith(a.Mul(b))
		case "/":
			if b.IsZero() {
				return "", ReasonInvalidDenominator, nil
			}
			return wrapArith(a.Div(b))
		}
		return "", "", domain.NewError(codeComputeFailed, "factor: unknown binary operator %q", e.Op)
	case ExprCall:
		return evalCall(ctx, e, member)
	}
	return "", "", domain.NewError(codeComputeFailed, "factor: unknown expression kind %q", e.Kind)
}

// evalCall evaluates one whitelisted function call.
func evalCall(ctx *evalContext, e *Expression, member domain.ID) (domain.Decimal, string, error) {
	v, reason, err := evalExpression(ctx, e.Args[0], member)
	if err != nil || reason != "" {
		return "", reason, err
	}
	switch e.Name {
	case "abs":
		if v.IsNegative() {
			zero, err := domain.ParseDecimal("0")
			if err != nil {
				return "", "", err
			}
			return wrapArith(zero.Sub(v))
		}
		return v, "", nil
	case "log", "sqrt":
		// Domain errors are missing members, not aborted runs: a factor
		// like log(PE) simply does not apply to that instrument.
		if v.IsNegative() || v.IsZero() {
			return "", ReasonNotApplicable, nil
		}
		f, err := strconv.ParseFloat(string(v), 64)
		if err != nil {
			return "", "", domain.Wrap(err, codeComputeFailed, "factor: %s cannot convert %q", e.Name, v)
		}
		out := math.Log(f)
		if e.Name == "sqrt" {
			out = math.Sqrt(f)
		}
		s := strconv.FormatFloat(out, 'f', -1, 64)
		d, err := domain.ParseDecimal(s)
		if err != nil {
			return "", "", domain.Wrap(err, codeComputeFailed, "factor: %s result %q is not representable", e.Name, s)
		}
		return d, "", nil
	case "min", "max":
		w, reason, err := evalExpression(ctx, e.Args[1], member)
		if err != nil || reason != "" {
			return "", reason, err
		}
		diff, err := v.Sub(w)
		if err != nil {
			return "", "", err
		}
		vSmaller := diff.IsNegative()
		if (e.Name == "min") == vSmaller {
			return v, "", nil
		}
		return w, "", nil
	}
	return "", "", domain.NewError(codeComputeFailed, "factor: unknown function %q", e.Name)
}

func declaredInput(spec *Spec, name string) bool {
	for _, in := range spec.Inputs {
		if in.Name == name {
			return true
		}
	}
	return false
}

func declaredDep(spec *Spec, ref FactorRef) bool {
	for _, dep := range spec.Deps {
		if dep == ref {
			return true
		}
	}
	return false
}
