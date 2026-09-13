package screening

import (
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

var (
	idNode1 = mustID("node1")
	idNode2 = mustID("node2")
	idNode3 = mustID("node3")
	idNode4 = mustID("node4")
	idGate  = mustID("b_gate")
	idScore = mustID("b_score")
	idSort  = mustID("b_sort")
	idSec   = mustID("b_sector")
	idVol   = mustID("b_vol")
)

func mustID(s string) domain.ID {
	id, err := domain.ParseID(s)
	if err != nil {
		panic(err)
	}
	return id
}

func val(kind domain.ValueKind, encoded string) domain.Value {
	return domain.Value{Kind: kind, Encoded: encoded}
}

func miss(reason string) domain.Value {
	return domain.Value{MissingReason: reason}
}

func mustDec(t *testing.T, s string) domain.Decimal {
	t.Helper()
	d, err := domain.ParseDecimal(s)
	if err != nil {
		t.Fatalf("ParseDecimal(%q): %v", s, err)
	}
	return d
}

func pdec(s string) domain.Decimal {
	d, err := domain.ParseDecimal(s)
	if err != nil {
		panic(err)
	}
	return d
}

func hasIssue(t *testing.T, issues []domain.Issue, code string) {
	t.Helper()
	for _, it := range issues {
		if it.Code == code {
			return
		}
	}
	t.Fatalf("expected issue code %q, got %v", code, codesOf(issues))
}

func noIssues(t *testing.T, issues []domain.Issue) {
	t.Helper()
	if len(issues) != 0 {
		t.Fatalf("expected no issues, got %v", codesOf(issues))
	}
}

func codesOf(issues []domain.Issue) []string {
	out := make([]string, len(issues))
	for i, it := range issues {
		out[i] = it.Code
	}
	return out
}

// truthLeaf builds a leaf condition whose root truth is t when evaluated
// against inputs carrying b_gate=5 (and no b_absent binding).
func truthLeaf(t Truth) Condition {
	switch t {
	case TruthTrue:
		return Missing{NodeID: idNode1, Input: Input{BindingID: idGate}, IsPresent: true}
	case TruthFalse:
		return Missing{NodeID: idNode2, Input: Input{BindingID: idGate}, IsMissing: true}
	default:
		return Compare{NodeID: idNode3, Input: Input{BindingID: mustID("b_absent")}, Operator: OpEq, Value: val(domain.ValueDecimal, "1")}
	}
}

func gateInputs() Inputs {
	return Inputs{idGate: val(domain.ValueDecimal, "5")}
}

// baseDef returns a valid sort-mode definition over decimal bindings.
func baseDef() Definition {
	return Definition{
		Name: "test",
		InputBindings: []InputBinding{
			{BindingID: idGate, Kind: BindingField, Dataset: "d", Field: "gate"},
			{BindingID: idSort, Kind: BindingField, Dataset: "d", Field: "sort"},
		},
		ConditionTree: Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "0")},
		Ranking: Ranking{
			Mode:   RankingSort,
			Fields: []RankField{{Input: Input{BindingID: idSort}, Direction: DirectionAsc}},
		},
		Selection: Selection{Mode: SelectionAll},
	}
}

func baseCatalog() InputTypes {
	return InputTypes{
		idGate:  domain.ValueDecimal,
		idScore: domain.ValueDecimal,
		idSort:  domain.ValueDecimal,
		idSec:   domain.ValueString,
		idVol:   domain.ValueDecimal,
	}
}

func TestValidateAcceptsFullDefinition(t *testing.T) {
	def := Definition{
		Name: "full",
		InputBindings: []InputBinding{
			{BindingID: idGate, Kind: BindingField, Dataset: "d", Field: "gate"},
			{BindingID: idScore, Kind: BindingFactor, FactorRef: domain.VersionRef{ID: mustID("f_pe"), Version: "v1"}, Params: map[string]any{"k": float64(1)}},
		},
		ConditionTree: All{NodeID: idNode1, Children: []Condition{
			Any{NodeID: idNode2, Children: []Condition{
				Compare{NodeID: idNode3, Input: Input{BindingID: idGate}, Operator: OpEq, Value: val(domain.ValueDecimal, "1")},
				Range{NodeID: idNode4, Input: Input{BindingID: idGate}, Lower: &domain.Value{Kind: domain.ValueDecimal, Encoded: "0"}, Upper: &domain.Value{Kind: domain.ValueDecimal, Encoded: "10"}, LowerInclusive: true, UpperInclusive: false},
			}},
			Set{NodeID: mustID("node5"), Input: Input{BindingID: idGate}, Values: []domain.Value{val(domain.ValueDecimal, "1"), val(domain.ValueDecimal, "2")}},
			Missing{NodeID: mustID("node6"), Input: Input{BindingID: idGate}, IsMissing: true},
		}},
		Ranking:        Ranking{Mode: RankingScore, Components: []ScoreComponent{{Input: Input{BindingID: idScore}, Weight: mustDec(t, "1"), Direction: ScoreLargerBetter}}},
		Selection:      Selection{Mode: SelectionAll},
		DisplayColumns: []domain.ID{idGate},
	}
	noIssues(t, Validate(def, baseCatalog(), DefaultLimits()))
}

func TestValidateNameEmpty(t *testing.T) {
	def := baseDef()
	def.Name = "  "
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeNameEmpty)
}

func TestValidateUnknownInput(t *testing.T) {
	def := baseDef()
	def.ConditionTree = Compare{NodeID: idNode1, Input: Input{BindingID: mustID("b_no")}, Operator: OpGt, Value: val(domain.ValueDecimal, "0")}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeInputUnknown)
}

func TestValidateBooleanGtMismatch(t *testing.T) {
	def := baseDef()
	def.ConditionTree = Compare{NodeID: idNode1, Input: Input{BindingID: idSec}, Operator: OpGt, Value: val(domain.ValueString, "x")}
	// idSec is string; gt not allowed
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeOperatorMismatch)
}

func TestValidateDecimalWithStringLiteral(t *testing.T) {
	def := baseDef()
	def.ConditionTree = Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueString, "x")}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeLiteralKindMatch)
}

func TestValidateRangeBounds(t *testing.T) {
	def := baseDef()
	def.ConditionTree = Range{NodeID: idNode1, Input: Input{BindingID: idGate}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeRangeBounds)

	lo := val(domain.ValueDecimal, "10")
	hi := val(domain.ValueDecimal, "1")
	def.ConditionTree = Range{NodeID: idNode1, Input: Input{BindingID: idGate}, Lower: &lo, Upper: &hi, LowerInclusive: true, UpperInclusive: true}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeRangeBounds)
}

func TestValidateSetInvalid(t *testing.T) {
	def := baseDef()
	def.ConditionTree = Set{NodeID: idNode1, Input: Input{BindingID: idGate}, Values: []domain.Value{val(domain.ValueDecimal, "1"), val(domain.ValueString, "x")}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeSetInvalid)

	def.ConditionTree = Set{NodeID: idNode1, Input: Input{BindingID: idSec}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeSetInvalid)
}

func TestValidateSetBooleanKind(t *testing.T) {
	// boolean binding not set-able → operator mismatch
	cat := InputTypes{idGate: domain.ValueBoolean}
	def := baseDef()
	def.ConditionTree = Set{NodeID: idNode1, Input: Input{BindingID: idGate}, Values: []domain.Value{val(domain.ValueBoolean, "true")}}
	hasIssue(t, Validate(def, cat, DefaultLimits()), codeOperatorMismatch)
}

func TestValidateDuplicateNodeID(t *testing.T) {
	def := baseDef()
	def.ConditionTree = All{NodeID: idNode1, Children: []Condition{
		Compare{NodeID: idNode2, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "0")},
		Compare{NodeID: idNode2, Input: Input{BindingID: idGate}, Operator: OpLt, Value: val(domain.ValueDecimal, "9")},
	}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeNodeIDDuplicate)
}

func TestValidateChildrenEmpty(t *testing.T) {
	def := baseDef()
	def.ConditionTree = All{NodeID: idNode1}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeChildrenEmpty)
}

func TestValidateWeightSum(t *testing.T) {
	def := baseDef()
	def.Ranking = Ranking{Mode: RankingScore, Components: []ScoreComponent{
		{Input: Input{BindingID: idScore}, Weight: mustDec(t, "0.6"), Direction: ScoreLargerBetter},
		{Input: Input{BindingID: idVol}, Weight: mustDec(t, "0.3"), Direction: ScoreLargerBetter},
	}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeWeightSum)
}

func TestValidateNegativeWeight(t *testing.T) {
	def := baseDef()
	def.Ranking = Ranking{Mode: RankingScore, Components: []ScoreComponent{
		{Input: Input{BindingID: idScore}, Weight: mustDec(t, "-1"), Direction: ScoreLargerBetter},
	}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeWeightInvalid)
}

func TestValidateZeroWeightAllowed(t *testing.T) {
	def := baseDef()
	def.Ranking = Ranking{Mode: RankingScore, Components: []ScoreComponent{
		{Input: Input{BindingID: idGate}, Weight: mustDec(t, "0"), Direction: ScoreLargerBetter},
		{Input: Input{BindingID: idSort}, Weight: mustDec(t, "1"), Direction: ScoreLargerBetter},
	}}
	noIssues(t, Validate(def, baseCatalog(), DefaultLimits()))
}

func TestValidateTopNZero(t *testing.T) {
	def := baseDef()
	def.Selection = Selection{Mode: SelectionTopN, N: 0}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeSelectionInvalid)
}

func TestValidateNodeLimits(t *testing.T) {
	def := baseDef()
	def.ConditionTree = All{NodeID: idNode1, Children: []Condition{
		Compare{NodeID: idNode2, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "0")},
		Compare{NodeID: idNode3, Input: Input{BindingID: idSort}, Operator: OpLt, Value: val(domain.ValueDecimal, "9")},
	}}
	limits := DefaultLimits()
	limits.MaxNodes = 2 // 3 nodes in tree > 2
	hasIssue(t, Validate(def, baseCatalog(), limits), codeNodeLimit)

	limits = DefaultLimits()
	def = baseDef()
	def.ConditionTree = buildChain(DefaultLimits().MaxDepth + 1)
	hasIssue(t, Validate(def, baseCatalog(), limits), codeDepthLimit)
}

func buildChain(n int) Condition {
	leaf := Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "0")}
	var c Condition = leaf
	for i := 1; i <= n; i++ {
		c = Not{NodeID: mustID("n" + itoa(i)), Child: c}
	}
	return c
}

func TestValidateMissingAmbiguous(t *testing.T) {
	def := baseDef()
	def.ConditionTree = Missing{NodeID: idNode1, Input: Input{BindingID: idGate}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeMissingAmbiguous)
}

func TestValidateRankingModeConflict(t *testing.T) {
	def := baseDef()
	def.Ranking = Ranking{Mode: RankingSort, Fields: []RankField{{Input: Input{BindingID: idSort}, Direction: DirectionAsc}}, Components: []ScoreComponent{{Input: Input{BindingID: idScore}, Weight: mustDec(t, "1"), Direction: ScoreLargerBetter}}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeRankingMode)

	def.Ranking = Ranking{Mode: RankingScore, Components: []ScoreComponent{{Input: Input{BindingID: idScore}, Weight: mustDec(t, "1"), Direction: ScoreLargerBetter}}, Fields: []RankField{{Input: Input{BindingID: idSort}, Direction: DirectionAsc}}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeRankingMode)
}

func TestValidateRankingKind(t *testing.T) {
	def := baseDef()
	def.Ranking = Ranking{Mode: RankingScore, Components: []ScoreComponent{{Input: Input{BindingID: idSec}, Weight: mustDec(t, "1"), Direction: ScoreLargerBetter}}}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeRankingKind)

	cat := InputTypes{idGate: domain.ValueDecimal, idSort: domain.ValueBoolean}
	def.Ranking = Ranking{Mode: RankingSort, Fields: []RankField{{Input: Input{BindingID: idSort}, Direction: DirectionAsc}}}
	hasIssue(t, Validate(def, cat, DefaultLimits()), codeRankingKind)
}

func TestValidateCatalogMissing(t *testing.T) {
	def := baseDef()
	def.InputBindings = append(def.InputBindings, InputBinding{BindingID: mustID("b_x"), Kind: BindingField, Dataset: "d", Field: "x"})
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeCatalogMissing)
}

func TestValidateDisplayInvalid(t *testing.T) {
	def := baseDef()
	def.DisplayColumns = []domain.ID{mustID("b_not_declared")}
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeDisplayInvalid)
}

func TestValidateParentInvalid(t *testing.T) {
	def := baseDef()
	def.ParentID = "bad id!"
	hasIssue(t, Validate(def, baseCatalog(), DefaultLimits()), codeParentInvalid)
}
