package screening

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

// fullDefinition exercises every condition branch plus both binding kinds, so
// a round-trip covers the whole wire surface a saved version can contain.
func fullDefinition() Definition {
	return Definition{
		Name:        "质量动量",
		Description: "含全部条件分支的测试定义",
		InputBindings: []InputBinding{
			{BindingID: "px", Kind: BindingField, Dataset: "bar", Field: "close"},
			{BindingID: "mom", Kind: BindingFactor, FactorRef: domain.VersionRef{ID: "momentum", Version: "1"}, Params: Params{"n": 20}},
		},
		ConditionTree: All{NodeID: "root", Children: []Condition{
			Compare{NodeID: "gt", Input: Input{BindingID: "px"}, Operator: OpGt, Value: domain.Value{Kind: domain.ValueDecimal, Encoded: "10"}},
			Range{
				NodeID:         "rng",
				Input:          Input{BindingID: "px"},
				Lower:          &domain.Value{Kind: domain.ValueDecimal, Encoded: "1"},
				Upper:          &domain.Value{Kind: domain.ValueDecimal, Encoded: "9"},
				LowerInclusive: true,
			},
			Set{NodeID: "set", Input: Input{BindingID: "px"}, Values: []domain.Value{{Kind: domain.ValueDecimal, Encoded: "1"}}},
			Missing{NodeID: "mis", Input: Input{BindingID: "mom"}, IsMissing: true},
			Not{NodeID: "not", Child: Compare{NodeID: "ne", Input: Input{BindingID: "px"}, Operator: OpNe, Value: domain.Value{Kind: domain.ValueDecimal, Encoded: "0"}}},
		}},
		Ranking:        Ranking{Mode: RankingSort, Fields: []RankField{{Input: Input{BindingID: "px"}, Direction: DirectionDesc}}},
		Selection:      Selection{Mode: SelectionTopN, N: 10},
		DisplayColumns: []domain.ID{"px"},
		ParentID:       "scr_parent",
	}
}

func marshal(t *testing.T, v any) []byte {
	t.Helper()
	encoded, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return encoded
}

// TestWireDefinitionRoundTrip proves the stored payload is lossless: decoding
// a marshaled definition and re-encoding it yields identical bytes, so the
// hash taken at save time still identifies the definition read back later.
func TestWireDefinitionRoundTrip(t *testing.T) {
	def := fullDefinition()
	encoded := marshal(t, WireDefinitionOf(def))

	var decoded WireDefinition
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	back, err := DefinitionOfWire(decoded, def.Name, def.Description, def.ParentID)
	if err != nil {
		t.Fatalf("DefinitionOfWire: %v", err)
	}
	again := marshal(t, WireDefinitionOf(back))
	if !bytes.Equal(encoded, again) {
		t.Fatalf("round-trip changed the payload:\n first: %s\nsecond: %s", encoded, again)
	}
	if issues := Validate(back, nil, DefaultLimits()); len(issues) > 0 {
		t.Fatalf("round-tripped definition is invalid: %+v", issues)
	}
}

// TestWireDefinitionExcludesLabels pins the documented hash scope: name,
// description and parent lineage are not part of the canonical payload, so the
// hash identifies the rule contract rather than the label it was saved under.
func TestWireDefinitionExcludesLabels(t *testing.T) {
	renamed := fullDefinition()
	renamed.Name = "另一个名字"
	renamed.Description = "另一段说明"
	renamed.ParentID = "scr_other"

	if !bytes.Equal(marshal(t, WireDefinitionOf(fullDefinition())), marshal(t, WireDefinitionOf(renamed))) {
		t.Fatal("name/description/parent_id leaked into the canonical payload")
	}
}

func TestWireBranchesMatchContract(t *testing.T) {
	cases := []struct {
		name string
		got  any
		want string
	}{
		{
			name: "field binding carries dataset and field only",
			got:  WireInputBinding{BindingID: "px", Kind: BindingField, Dataset: "bar", Field: "close"},
			want: `{"binding_id":"px","kind":"field","dataset":"bar","field":"close"}`,
		},
		{
			name: "factor binding always emits params",
			got:  WireInputBinding{BindingID: "mom", Kind: BindingFactor, FactorRef: domain.VersionRef{ID: "momentum", Version: "1"}},
			want: `{"binding_id":"mom","kind":"factor","factor_ref":{"id":"momentum","version":"1"},"params":{}}`,
		},
		{
			name: "range emits nullable bounds and both flags",
			got:  WireCondition{NodeID: "rng", Kind: "range", Input: &WireInput{BindingID: "px"}},
			want: `{"node_id":"rng","kind":"range","input":{"binding_id":"px"},"lower":null,"upper":null,"lower_inclusive":false,"upper_inclusive":false}`,
		},
		{
			name: "set emits the selected membership branch only",
			got:  WireCondition{NodeID: "set", Kind: "set", Input: &WireInput{BindingID: "px"}, In: []domain.Value{{Kind: domain.ValueDecimal, Encoded: "1"}}},
			want: `{"node_id":"set","kind":"set","input":{"binding_id":"px"},"in":[{"kind":"decimal","value":"1"}]}`,
		},
		{
			name: "missing emits exactly one predicate",
			got:  WireCondition{NodeID: "mis", Kind: "missing", Input: &WireInput{BindingID: "px"}, IsMissing: boolPtr(true)},
			want: `{"node_id":"mis","kind":"missing","input":{"binding_id":"px"},"is_missing":true}`,
		},
		{
			name: "all emits its children array",
			got:  WireCondition{NodeID: "root", Kind: "all", Children: []WireCondition{}},
			want: `{"node_id":"root","kind":"all","children":[]}`,
		},
		{
			name: "score ranking emits components only",
			got:  WireRanking{Mode: RankingScore, Components: []WireScoreComponent{{Input: WireInput{BindingID: "px"}, Weight: domain.Decimal("1"), Direction: ScoreLargerBetter}}},
			want: `{"mode":"score","components":[{"input":{"binding_id":"px"},"weight":"1","direction":"larger_is_better"}]}`,
		},
		{
			name: "sort ranking emits fields only",
			got:  WireRanking{Mode: RankingSort, Fields: []WireRankField{{Input: WireInput{BindingID: "px"}, Direction: DirectionAsc}}},
			want: `{"mode":"sort","fields":[{"input":{"binding_id":"px"},"direction":"asc"}]}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := string(marshal(t, tc.got)); got != tc.want {
				t.Fatalf("wire payload = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestWireUnknownKindFailsClosed(t *testing.T) {
	if _, err := json.Marshal(WireInputBinding{BindingID: "x", Kind: "unknown"}); err == nil {
		t.Fatal("unknown binding kind must not be marshaled")
	}
	if _, err := json.Marshal(WireCondition{NodeID: "x", Kind: "unknown"}); err == nil {
		t.Fatal("unknown condition kind must not be marshaled")
	}
}

func TestDefinitionOfWireFailsClosed(t *testing.T) {
	validTree := WireCondition{NodeID: "gt", Kind: "compare", Input: &WireInput{BindingID: "px"}, Operator: OpGt, Value: &domain.Value{Kind: domain.ValueDecimal, Encoded: "1"}}

	// A factor binding without factor_ref is a malformed payload: the branch
	// discriminator alone does not supply the reference.
	if _, err := DefinitionOfWire(WireDefinition{
		ConditionTree: validTree,
		InputBindings: []WireInputBinding{{BindingID: "mom", Kind: BindingFactor}},
	}, "n", "", ""); err == nil {
		t.Fatal("factor binding without factor_ref must be rejected")
	}
	if _, err := DefinitionOfWire(WireDefinition{}, "n", "", ""); err == nil {
		t.Fatal("missing condition tree must be rejected")
	}
	if _, err := DefinitionOfWire(WireDefinition{
		ConditionTree: validTree,
		InputBindings: []WireInputBinding{{BindingID: "mom", Kind: BindingFactor, FactorRef: domain.VersionRef{ID: "momentum", Version: "1"}}},
	}, "n", "", ""); err != nil {
		t.Fatalf("factor binding with factor_ref must decode: %v", err)
	}
}

// TestVersionRequestValidateCollapsesToDefinitionCode pins the save-time
// contract: structural validation needs no snapshot and reports one stable
// code, so the HTTP layer maps it to a single status.
func TestVersionRequestValidateCollapsesToDefinitionCode(t *testing.T) {
	def := fullDefinition()
	def.Ranking = Ranking{Mode: RankingScore, Components: []ScoreComponent{
		{Input: Input{BindingID: "px"}, Weight: domain.Decimal("0.6"), Direction: ScoreLargerBetter},
	}}
	err := VersionRequest{Name: def.Name, Definition: def}.Validate()
	if err == nil {
		t.Fatal("weights summing to 0.6 must be rejected")
	}
	if got := domain.ErrorCode(err); got != codeDefinitionInvalid {
		t.Fatalf("error code = %q, want %q", got, codeDefinitionInvalid)
	}

	unnamed := fullDefinition()
	unnamed.Name = " "
	if err := (VersionRequest{Name: unnamed.Name, Definition: unnamed}).Validate(); err == nil {
		t.Fatal("empty name must be rejected")
	}
}
