package screening

import (
	"errors"
	"strings"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

type Truth string

const (
	TruthTrue    Truth = "true"
	TruthFalse   Truth = "false"
	TruthUnknown Truth = "unknown"
)

type Inputs map[domain.ID]domain.Value

type NodeEvaluation struct {
	NodeID        domain.ID        `json:"node_id"`
	Kind          string           `json:"kind"`
	Truth         Truth            `json:"truth"`
	Input         *Input           `json:"input,omitempty"`
	Threshold     *domain.Value    `json:"threshold,omitempty"`
	MissingReason string           `json:"missing_reason,omitempty"`
	Children      []NodeEvaluation `json:"children"`
}

func (e NodeEvaluation) truthNode() {}

// Evaluate walks the condition tree with three-valued logic against one
// instrument's resolved inputs and returns the root truth plus per-node
// evidence. A nil root (or empty tree) evaluates to unknown.
func Evaluate(root Condition, inputs Inputs) (Truth, []NodeEvaluation) {
	ev := &evaluator{inputs: inputs}
	if root == nil {
		return TruthUnknown, nil
	}
	n := ev.eval(root)
	return n.Truth, []NodeEvaluation{n}
}

type evaluator struct {
	inputs Inputs
}

func (e *evaluator) eval(node Condition) NodeEvaluation {
	switch n := node.(type) {
	case All:
		return e.evalAll(n)
	case Any:
		return e.evalAny(n)
	case Not:
		return e.evalNot(n)
	case Compare:
		return e.evalCompare(n)
	case Range:
		return e.evalRange(n)
	case Set:
		return e.evalSet(n)
	case Missing:
		return e.evalMissing(n)
	default:
		return NodeEvaluation{Kind: "unknown_node", Truth: TruthUnknown}
	}
}

func (e *evaluator) evalAll(n All) NodeEvaluation {
	ne := NodeEvaluation{NodeID: n.NodeID, Kind: "all", Truth: TruthTrue}
	hasFalse, hasUnknown := false, false
	for _, child := range n.Children {
		if child == nil {
			continue
		}
		c := e.eval(child)
		ne.Children = append(ne.Children, c)
		switch c.Truth {
		case TruthFalse:
			hasFalse = true
		case TruthUnknown:
			hasUnknown = true
		}
	}
	switch {
	case hasFalse:
		ne.Truth = TruthFalse
	case hasUnknown:
		ne.Truth = TruthUnknown
	default:
		ne.Truth = TruthTrue
	}
	return ne
}

func (e *evaluator) evalAny(n Any) NodeEvaluation {
	ne := NodeEvaluation{NodeID: n.NodeID, Kind: "any", Truth: TruthFalse}
	hasTrue, hasUnknown := false, false
	for _, child := range n.Children {
		if child == nil {
			continue
		}
		c := e.eval(child)
		ne.Children = append(ne.Children, c)
		switch c.Truth {
		case TruthTrue:
			hasTrue = true
		case TruthUnknown:
			hasUnknown = true
		}
	}
	switch {
	case hasTrue:
		ne.Truth = TruthTrue
	case hasUnknown:
		ne.Truth = TruthUnknown
	default:
		ne.Truth = TruthFalse
	}
	return ne
}

func (e *evaluator) evalNot(n Not) NodeEvaluation {
	ne := NodeEvaluation{NodeID: n.NodeID, Kind: "not", Truth: TruthUnknown}
	if n.Child == nil {
		ne.MissingReason = "child_required"
		return ne
	}
	c := e.eval(n.Child)
	ne.Children = []NodeEvaluation{c}
	switch c.Truth {
	case TruthTrue:
		ne.Truth = TruthFalse
	case TruthFalse:
		ne.Truth = TruthTrue
	default:
		ne.Truth = TruthUnknown
	}
	return ne
}

// lookupInput resolves an input value and its availability reason. A missing
// inputs entry, an explicit missing_reason, or an empty Kind cell all make
// the value unavailable; otherwise a parse failure is reported later.
func (e *evaluator) lookupInput(in Input) (parsedValue, domain.Value, string, ErrorReason) {
	raw, ok := e.inputs[in.BindingID]
	if !ok {
		return parsedValue{}, domain.Value{}, "", ReasonInputUnavailable
	}
	if raw.MissingReason != "" || raw.Kind == "" {
		return parsedValue{}, raw, raw.MissingReason, ReasonNotAvailable
	}
	pv, err := parseValue(raw)
	if err != nil {
		return parsedValue{}, raw, "", ReasonInputUnparseable
	}
	return pv, raw, "", ""
}

type ErrorReason string

const (
	ReasonNoError            ErrorReason = ""
	ReasonInputUnavailable   ErrorReason = "input_unavailable"
	ReasonInputUnparseable   ErrorReason = "input_unparseable"
	ReasonLiteralUnparseable ErrorReason = "literal_unparseable"
	ReasonKindMismatch       ErrorReason = "kind_mismatch"
	ReasonOperatorMismatch   ErrorReason = "operator_kind_mismatch"
	ReasonNotAvailable       ErrorReason = "not_available"
	ReasonChildRequired      ErrorReason = "child_required"
)

func (e *evaluator) evalCompare(n Compare) NodeEvaluation {
	ne := NodeEvaluation{NodeID: n.NodeID, Kind: "compare", Input: inputPtr(n.Input), Threshold: &n.Value}
	in := &n.Input
	pv, raw, missReason, reason := e.lookupInput(*in)
	_ = raw
	if reason != ReasonNoError {
		ne.Truth, ne.MissingReason = TruthUnknown, missString(missReason, reason)
		return ne
	}
	tv, err := parseValue(n.Value)
	if err != nil {
		ne.Truth, ne.MissingReason = TruthUnknown, string(ReasonLiteralUnparseable)
		return ne
	}
	if pv.kind != tv.kind {
		ne.Truth, ne.MissingReason = TruthUnknown, string(ReasonKindMismatch)
		return ne
	}
	if !operatorAllowed(pv.kind, n.Operator) {
		ne.Truth, ne.MissingReason = TruthUnknown, string(ReasonOperatorMismatch)
		return ne
	}
	cmp, err := compareParsed(pv, tv)
	if err != nil {
		ne.Truth, ne.MissingReason = TruthUnknown, string(ReasonKindMismatch)
		return ne
	}
	switch n.Operator {
	case OpEq:
		ne.Truth = boolTruth(cmp == 0)
	case OpNe:
		ne.Truth = boolTruth(cmp != 0)
	case OpGt:
		ne.Truth = boolTruth(cmp > 0)
	case OpGte:
		ne.Truth = boolTruth(cmp >= 0)
	case OpLt:
		ne.Truth = boolTruth(cmp < 0)
	case OpLte:
		ne.Truth = boolTruth(cmp <= 0)
	default:
		ne.Truth, ne.MissingReason = TruthUnknown, string(ReasonOperatorMismatch)
	}
	return ne
}

func (e *evaluator) lookup(n Input) (parsedValue, string, ErrorReason) {
	pv, _, canc, reason := e.lookupInput(n)
	return pv, canc, reason
}

func (e *evaluator) evalRange(n Range) NodeEvaluation {
	ne := NodeEvaluation{NodeID: n.NodeID, Kind: "range", Input: inputPtr(n.Input)}
	pv, _, missReason, reason := e.lookupInput(n.Input)
	if reason != ReasonNoError {
		ne.Truth, ne.MissingReason = TruthUnknown, missString(missReason, reason)
		return ne
	}
	if ok, msg := withinRange(pv, n); !ok {
		ne.Truth, ne.MissingReason = TruthFalse, msg
		return ne
	} else {
		ne.Truth = TruthTrue
	}
	return ne
}

func withinRange(pv parsedValue, n Range) (bool, string) {
	if n.LowerInclusive || n.UpperInclusive {
		// handled below per bound
	}
	if n.Lower != nil {
		if n.Lower.Kind != pv.kind {
			return false, string(ReasonKindMismatch)
		}
		lv, err := parseValue(*n.Lower)
		if err != nil {
			return false, string(ReasonLiteralUnparseable)
		}
		cmp, err := compareParsed(pv, lv)
		if err != nil {
			return false, string(ReasonKindMismatch)
		}
		if cmp < 0 || (cmp == 0 && !n.LowerInclusive) {
			return false, string(ReasonNoError)
		}
	}
	if n.Upper != nil {
		if n.Upper.Kind != pv.kind {
			return false, string(ReasonKindMismatch)
		}
		uv, err := parseValue(*n.Upper)
		if err != nil {
			return false, string(ReasonLiteralUnparseable)
		}
		cmp, err := compareParsed(pv, uv)
		if err != nil {
			return false, string(ReasonKindMismatch)
		}
		if cmp > 0 || (cmp == 0 && !n.UpperInclusive) {
			return false, string(ReasonNoError)
		}
	}
	return true, string(ReasonNoError)
}

func (e *evaluator) evalSet(n Set) NodeEvaluation {
	ne := NodeEvaluation{NodeID: n.NodeID, Kind: "set", Input: inputPtr(n.Input)}
	pv, _, missReason, reason := e.lookupInput(n.Input)
	if reason != ReasonNoError {
		ne.Truth, ne.MissingReason = TruthUnknown, missString(missReason, reason)
		return ne
	}
	truth := TruthFalse
	for _, v := range n.Values {
		ev, err := parseValue(v)
		if err != nil {
			ne.Truth, ne.MissingReason = TruthUnknown, string(ReasonLiteralUnparseable)
			return ne
		}
		if ev.kind != pv.kind {
			ne.Truth, ne.MissingReason = TruthUnknown, string(ReasonKindMismatch)
			return ne
		}
		if cmp, _ := compareParsed(pv, ev); cmp == 0 {
			truth = TruthTrue
		}
	}
	if n.NotIn {
		if truth == TruthTrue {
			truth = TruthFalse
		} else {
			truth = TruthTrue
		}
	}
	ne.Truth = truth
	return ne
}

func (e *evaluator) evalMissing(n Missing) NodeEvaluation {
	ne := NodeEvaluation{NodeID: n.NodeID, Kind: "missing", Input: inputPtr(n.Input)}
	raw, ok := e.inputs[n.Input.BindingID]
	if !ok {
		isMissing := n.IsMissing
		ne.Truth = boolTruth(isMissing)
		if isMissing {
			ne.MissingReason = string(ReasonInputUnavailable)
		}
		return ne
	}
	available := raw.Kind != "" && raw.MissingReason == ""
	if n.IsMissing {
		ne.Truth = boolTruth(!available)
		if !available {
			ne.MissingReason = raw.MissingReason
		}
		return ne
	}
	ne.Truth = boolTruth(available)
	return ne
}

func inputPtr(in Input) *Input {
	c := in
	return &c
}

func boolTruth(b bool) Truth {
	if b {
		return TruthTrue
	}
	return TruthFalse
}

func missString(missReason string, reason ErrorReason) string {
	if missReason != "" {
		return missReason
	}
	return string(reason)
}

type parsedValue struct {
	kind domain.ValueKind
	dec  domain.Decimal
	ts   time.Time
	str  string
	flag bool
}

func parseValue(v domain.Value) (parsedValue, error) {
	switch v.Kind {
	case domain.ValueDecimal, domain.ValueNumber:
		dec, err := domain.ParseDecimal(v.Encoded)
		if err != nil {
			return parsedValue{}, err
		}
		return parsedValue{kind: v.Kind, dec: dec}, nil
	case domain.ValueTimestamp:
		ts, err := parseTimestamp(v.Encoded)
		if err != nil {
			return parsedValue{}, err
		}
		return parsedValue{kind: v.Kind, ts: ts}, nil
	case domain.ValueString:
		if strings.TrimSpace(v.Encoded) == "" {
			return parsedValue{}, errors.New("empty string")
		}
		return parsedValue{kind: v.Kind, str: v.Encoded}, nil
	case domain.ValueBoolean:
		switch v.Encoded {
		case "true":
			return parsedValue{kind: v.Kind, flag: true}, nil
		case "false":
			return parsedValue{kind: v.Kind}, nil
		default:
			return parsedValue{}, errors.New("invalid boolean")
		}
	default:
		return parsedValue{}, errors.New("unknown kind")
	}
}

func parseTimestamp(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

func compareParsed(a, b parsedValue) (int, error) {
	if a.kind != b.kind {
		return 0, errors.New("kind mismatch")
	}
	switch a.kind {
	case domain.ValueDecimal, domain.ValueNumber:
		return compareDec(a.dec, b.dec), nil
	case domain.ValueTimestamp:
		switch {
		case a.ts.Before(b.ts):
			return -1, nil
		case a.ts.After(b.ts):
			return 1, nil
		default:
			return 0, nil
		}
	case domain.ValueString:
		return strings.Compare(a.str, b.str), nil
	case domain.ValueBoolean:
		ai, bi := 0, 0
		if a.flag {
			ai = 1
		}
		if b.flag {
			bi = 1
		}
		return ai - bi, nil
	default:
		return 0, errors.New("unknown kind")
	}
}

func compareDec(a, b domain.Decimal) int {
	diff, err := a.Sub(b)
	if err != nil {
		return 0
	}
	switch {
	case diff.IsNegative():
		return -1
	case diff.IsPositive():
		return 1
	default:
		return 0
	}
}

// compareLiterals compares two threshold literals of the same kind for range
// bound cross-checks during save-time validation.
func compareLiterals(a, b domain.Value) (int, error) {
	av, err := parseValue(a)
	if err != nil {
		return 0, err
	}
	bv, err := parseValue(b)
	if err != nil {
		return 0, err
	}
	return compareParsed(av, bv)
}
