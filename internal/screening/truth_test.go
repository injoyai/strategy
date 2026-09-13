package screening

import (
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

var truths = []Truth{TruthFalse, TruthTrue, TruthUnknown}

func TestKleeneNot(t *testing.T) {
	want := map[Truth]Truth{TruthTrue: TruthFalse, TruthFalse: TruthTrue, TruthUnknown: TruthUnknown}
	for _, in := range truths {
		n := Not{NodeID: idNode1, Child: truthLeaf(in)}
		got, _ := Evaluate(n, gateInputs())
		if got != want[in] {
			t.Errorf("NOT %s = %s, want %s", in, got, want[in])
		}
	}
}

func TestKleeneAnd(t *testing.T) {
	for _, a := range truths {
		for _, b := range truths {
			node := All{NodeID: idNode1, Children: []Condition{truthLeaf(a), truthLeaf(b)}}
			got, _ := Evaluate(node, gateInputs())
			want := kleeneAnd(a, b)
			if got != want {
				t.Errorf("AND(%s,%s) = %s, want %s", a, b, got, want)
			}
		}
	}
}

func TestKleeneOr(t *testing.T) {
	for _, a := range truths {
		for _, b := range truths {
			node := Any{NodeID: idNode1, Children: []Condition{truthLeaf(a), truthLeaf(b)}}
			got, _ := Evaluate(node, gateInputs())
			want := kleeneOr(a, b)
			if got != want {
				t.Errorf("OR(%s,%s) = %s, want %s", a, b, got, want)
			}
		}
	}
}

func kleeneAnd(a, b Truth) Truth {
	if a == TruthFalse || b == TruthFalse {
		return TruthFalse
	}
	if a == TruthUnknown || b == TruthUnknown {
		return TruthUnknown
	}
	return TruthTrue
}

func kleeneOr(a, b Truth) Truth {
	if a == TruthTrue || b == TruthTrue {
		return TruthTrue
	}
	if a == TruthUnknown || b == TruthUnknown {
		return TruthUnknown
	}
	return TruthFalse
}

func evalOne(root Condition, inputs Inputs) Truth {
	tr, _ := Evaluate(root, inputs)
	return tr
}

func TestCompareOperatorsDecimal(t *testing.T) {
	cases := []struct {
		op   Operator
		want Truth
	}{
		{OpEq, TruthFalse},
		{OpNe, TruthTrue},
		{OpGt, TruthFalse}, // input -5 vs literal 3 (lexicographic trap)
		{OpGte, TruthFalse},
		{OpLt, TruthTrue},
		{OpLte, TruthTrue},
	}
	inputs := Inputs{idGate: val(domain.ValueDecimal, "-5")}
	for _, c := range cases {
		node := Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: c.op, Value: val(domain.ValueDecimal, "3")}
		if got := evalOne(node, inputs); got != c.want {
			t.Errorf("(input=-5) %s 3 = %s, want %s", c.op, got, c.want)
		}
	}
}

func TestCompareStringEQ(t *testing.T) {
	inputs := Inputs{idSec: val(domain.ValueString, "tech")}
	node := Compare{NodeID: idNode1, Input: Input{BindingID: idSec}, Operator: OpEq, Value: val(domain.ValueString, "tech")}
	if got := evalOne(node, inputs); got != TruthTrue {
		t.Errorf("string eq = %s, want true", got)
	}
	nodeGt := Compare{NodeID: idNode1, Input: Input{BindingID: idSec}, Operator: OpGt, Value: val(domain.ValueString, "tech")}
	if got := evalOne(nodeGt, inputs); got != TruthUnknown {
		t.Errorf("string gt = %s, want unknown (operator_kind_mismatch)", got)
	}
}

func TestBooleanGtUnknownAtEval(t *testing.T) {
	inputs := Inputs{idGate: val(domain.ValueBoolean, "true")}
	node := Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueBoolean, "false")}
	if got := evalOne(node, inputs); got != TruthUnknown {
		t.Errorf("boolean gt = %s, want unknown", got)
	}
}

func TestMissingReasonPropagation(t *testing.T) {
	inputs := Inputs{idGate: miss("unavailable_me")}
	node := Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "1")}
	truth, nodes := Evaluate(node, inputs)
	if truth != TruthUnknown {
		t.Fatalf("want unknown, got %s", truth)
	}
	if nodes[0].MissingReason != "unavailable_me" {
		t.Errorf("want propagated reason, got %q", nodes[0].MissingReason)
	}
	if nodes[0].Input == nil || nodes[0].Threshold == nil {
		t.Errorf("explanation should carry input and threshold")
	}
}

func TestRangeInclusivity(t *testing.T) {
	cases := []struct {
		lower, upper       *domain.Value
		lowerInc, upperInc bool
		input              string
		want               Truth
	}{
		{ptrVal("1"), ptrVal("3"), true, true, "2", TruthTrue},
		{ptrVal("1"), ptrVal("3"), true, true, "3", TruthTrue},
		{ptrVal("1"), ptrVal("3"), true, true, "4", TruthFalse},
		{ptrVal("1"), ptrVal("3"), false, true, "1", TruthFalse},
		{ptrVal("1"), nil, true, false, "0", TruthFalse},
		{nil, ptrVal("3"), false, false, "3", TruthFalse},
		{nil, ptrVal("3"), false, false, "2.5", TruthTrue},
	}
	for i, c := range cases {
		node := Range{NodeID: idNode1, Input: Input{BindingID: idGate}, Lower: c.lower, Upper: c.upper, LowerInclusive: c.lowerInc, UpperInclusive: c.upperInc}
		inputs := Inputs{idGate: val(domain.ValueDecimal, c.input)}
		got := evalOne(node, inputs)
		if got != c.want {
			t.Errorf("case %d input=%s: got %s want %s", i, c.input, got, c.want)
		}
	}
}

func ptrVal(s string) *domain.Value {
	v := val(domain.ValueDecimal, s)
	return &v
}

func TestSetMembership(t *testing.T) {
	node := Set{NodeID: idNode1, Input: Input{BindingID: idGate}, Values: []domain.Value{val(domain.ValueDecimal, "1"), val(domain.ValueDecimal, "2"), val(domain.ValueDecimal, "5")}}
	if got := evalOne(node, Inputs{idGate: val(domain.ValueDecimal, "5")}); got != TruthTrue {
		t.Errorf("in-set got %s want true", got)
	}
	if got := evalOne(node, Inputs{idGate: val(domain.ValueDecimal, "9")}); got != TruthFalse {
		t.Errorf("not-in-set got %s want false", got)
	}
	notIn := Set{NodeID: idNode1, Input: Input{BindingID: idGate}, Values: node.Values, NotIn: true}
	if got := evalOne(notIn, Inputs{idGate: val(domain.ValueDecimal, "5")}); got != TruthFalse {
		t.Errorf("not_in got %s want false", got)
	}
	unparseable := Set{NodeID: idNode1, Input: Input{BindingID: idGate}, Values: []domain.Value{val(domain.ValueDecimal, "1"), miss("some_reason")}}
	if got := evalOne(unparseable, Inputs{idGate: val(domain.ValueDecimal, "1")}); got != TruthUnknown {
		t.Errorf("unparseable element got %s want unknown", got)
	}
}

func TestExplainNested(t *testing.T) {
	tr, nodes := Evaluate(All{NodeID: idNode1, Children: []Condition{
		truthLeaf(TruthTrue),
		Any{NodeID: idNode2, Children: []Condition{truthLeaf(TruthFalse), truthLeaf(TruthTrue)}},
	}}, gateInputs())
	if tr != TruthTrue {
		t.Fatalf("nested got %s want true", tr)
	}
	if len(nodes) != 1 || len(nodes[0].Children) != 2 {
		t.Fatalf("unexpected explanation tree shape")
	}
}
