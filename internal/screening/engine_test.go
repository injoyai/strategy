package screening

import (
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

func sortUniverseDef() Definition {
	return Definition{
		Name: "u",
		InputBindings: []InputBinding{
			{BindingID: idGate, Kind: BindingField, Dataset: "d", Field: "gate"},
			{BindingID: idSort, Kind: BindingField, Dataset: "d", Field: "sort"},
			{BindingID: idScore, Kind: BindingField, Dataset: "d", Field: "score"},
		},
		ConditionTree: Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "0")},
		Ranking:       Ranking{Mode: RankingSort, Fields: []RankField{{Input: Input{BindingID: idSort}, Direction: DirectionAsc}}},
		Selection:     Selection{Mode: SelectionTopN, N: 1},
	}
}

func fullGate(id string, present bool, gate string) InstrumentInputs {
	v := Inputs{}
	if present {
		v[idGate] = val(domain.ValueDecimal, gate)
	}
	return InstrumentInputs{InstrumentID: mustID(id), Values: v}
}

func withSort(inst InstrumentInputs, sortVal string) InstrumentInputs {
	inst.Values[idSort] = val(domain.ValueDecimal, sortVal)
	return inst
}

func TestEngineConservation(t *testing.T) {
	var universe []InstrumentInputs
	// 2 false
	universe = append(universe, fullGate("f0", true, "0"), fullGate("f1", true, "-1"))
	// 1 unknown
	universe = append(universe, fullGate("u", false, ""))
	// 1 true but insufficient (no sort)
	universe = append(universe, fullGate("t1", true, "5"))
	// 2 rankable
	universe = append(universe, withSort(fullGate("r1", true, "5"), "1"), withSort(fullGate("r2", true, "5"), "2"))

	res := runUniverse(t, sortUniverseDef(), universe)
	s := res.Summary
	if s.Population != 6 || s.ConditionFalse != 2 || s.ConditionUnknown != 1 || s.ConditionTrue != 3 ||
		s.RankInsufficient != 1 || s.Rankable != 2 || s.Selected != 1 || s.NotSelected != 1 {
		t.Errorf("conservation mismatch: %+v", s)
	}
	if s.Population != s.ConditionFalse+s.ConditionUnknown+s.ConditionTrue {
		t.Errorf("population not conserved")
	}
	if s.ConditionTrue != s.RankInsufficient+s.Rankable {
		t.Errorf("condition_true not conserved")
	}
	if s.Rankable != s.Selected+s.NotSelected {
		t.Errorf("rankable not conserved")
	}
	// failure paths are excluded rows sorted by instrument; rankable precede
	if len(res.Rows) != 6 {
		t.Fatalf("want 6 rows got %d", len(res.Rows))
	}
}

func TestEngineEmptyPopulation(t *testing.T) {
	res := runUniverse(t, sortUniverseDef(), nil)
	if res.Summary.EmptyReason != EmptyPopulation || res.Summary.Population != 0 {
		t.Errorf("empty population: %+v", res.Summary)
	}
	if len(res.Rows) != 0 {
		t.Errorf("want 0 rows got %d", len(res.Rows))
	}
}

func TestEngineConditionExcludedAll(t *testing.T) {
	universe := []InstrumentInputs{fullGate("f0", true, "0"), fullGate("f1", true, "-3")}
	res := runUniverse(t, sortUniverseDef(), universe)
	if res.Summary.EmptyReason != ConditionExcludedAll {
		t.Errorf("condition_excluded_all: %+v", res.Summary)
	}
}

func TestEngineNoRankable(t *testing.T) {
	universe := []InstrumentInputs{fullGate("t1", true, "5"), fullGate("t2", true, "6")}
	res := runUniverse(t, sortUniverseDef(), universe)
	if res.Summary.EmptyReason != NoRankableInstruments {
		t.Errorf("no_rankable: %+v", res.Summary)
	}
	if res.Summary.RankInsufficient != 2 || res.Summary.Rankable != 0 {
		t.Errorf("no_rankable counts: %+v", res.Summary)
	}
}

func TestEngineFailRunUnknown(t *testing.T) {
	universe := []InstrumentInputs{fullGate("u", false, "")}
	_, err := Run(sortUniverseDef(), baseCatalog(), Policy{RequiredValue: PolicyFailRun, Scoring: DefaultScoring()}, DefaultLimits(), universe)
	if err == nil || domain.ErrorCode(err) != codeRequiredValueMissing {
		t.Errorf("fail_run unknown: want %s got %v", codeRequiredValueMissing, err)
	}
}

func TestEngineFailRunInsufficient(t *testing.T) {
	universe := []InstrumentInputs{fullGate("t1", true, "5"), fullGate("t2", true, "6")}
	_, err := Run(sortUniverseDef(), baseCatalog(), Policy{RequiredValue: PolicyFailRun, Scoring: DefaultScoring()}, DefaultLimits(), universe)
	if err == nil || domain.ErrorCode(err) != codeRequiredValueMissing {
		t.Errorf("fail_run insufficient: want %s got %v", codeRequiredValueMissing, err)
	}
}

func TestEngineExcludeInstrument(t *testing.T) {
	universe := []InstrumentInputs{fullGate("u", false, ""), fullGate("t1", true, "5")}
	res := runUniverse(t, sortUniverseDef(), universe)
	// one unknown (missing gate) and one insufficient (missing sort) both excluded
	if res.Summary.Population != 2 || res.Summary.Rankable != 0 {
		t.Errorf("exclude semantics: %+v", res.Summary)
	}
}

func TestEngineDisplayMissingNoFailRun(t *testing.T) {
	def := sortUniverseDef()
	def.DisplayColumns = []domain.ID{idScore, idSort}
	universe := []InstrumentInputs{withSort(fullGate("t1", true, "5"), "1")}
	// display column values absent; fail_run must NOT trigger
	res, err := Run(def, baseCatalog(), Policy{RequiredValue: PolicyFailRun, Scoring: DefaultScoring()}, DefaultLimits(), universe)
	if err != nil {
		t.Fatalf("display-only missing must not fail_run: %v", err)
	}
	_ = res
}

func TestEngineInvalidDefinition(t *testing.T) {
	def := sortUniverseDef()
	def.Name = ""
	_, err := Run(def, baseCatalog(), Policy{RequiredValue: PolicyExcludeInstrument, Scoring: DefaultScoring()}, DefaultLimits(), nil)
	if err == nil || domain.ErrorCode(err) != codeDefinitionInvalid {
		t.Errorf("invalid definition: want %s got %v", codeDefinitionInvalid, err)
	}
}

func TestEngineInvalidPolicy(t *testing.T) {
	_, err := Run(sortUniverseDef(), baseCatalog(), Policy{RequiredValue: RequiredValuePolicy("bogus"), Scoring: DefaultScoring()}, DefaultLimits(), nil)
	if err == nil || domain.ErrorCode(err) != codeInvalidPolicy {
		t.Errorf("invalid policy: want %s got %v", codeInvalidPolicy, err)
	}
}

func TestEngineInvalidScoring(t *testing.T) {
	def := scoreDef()
	_, err := Run(def, baseCatalog(), Policy{RequiredValue: PolicyExcludeInstrument, Scoring: Scoring{Places: 0, Rounding: domain.RoundHalfAwayFromZero}}, DefaultLimits(), nil)
	if err == nil || domain.ErrorCode(err) != codeInvalidPolicy {
		t.Errorf("invalid scoring: want %s got %v", codeInvalidPolicy, err)
	}
}

func TestEngineDuplicateInstrument(t *testing.T) {
	universe := []InstrumentInputs{
		{InstrumentID: mustID("dup"), Values: Inputs{idGate: val(domain.ValueDecimal, "5")}},
		{InstrumentID: mustID("dup"), Values: Inputs{idGate: val(domain.ValueDecimal, "5")}},
	}
	_, err := Run(sortUniverseDef(), baseCatalog(), Policy{RequiredValue: PolicyExcludeInstrument, Scoring: DefaultScoring()}, DefaultLimits(), universe)
	if err == nil || domain.ErrorCode(err) != codeDuplicateInstrument {
		t.Errorf("duplicate instrument: want %s got %v", codeDuplicateInstrument, err)
	}
}

func TestEngineTopNNoPadding(t *testing.T) {
	universe := []InstrumentInputs{withSort(fullGate("r1", true, "5"), "1"), withSort(fullGate("r2", true, "5"), "2")}
	def := sortUniverseDef()
	def.Selection = Selection{Mode: SelectionTopN, N: 5} // more than rankable
	res := runUniverse(t, def, universe)
	if res.Summary.Selected != 2 || res.Summary.NotSelected != 0 {
		t.Errorf("n>m must return all, no padding: %+v", res.Summary)
	}
	def.Selection = Selection{Mode: SelectionTopN, N: 1}
	res = runUniverse(t, def, universe)
	if res.Summary.Selected != 1 || res.Summary.NotSelected != 1 {
		t.Errorf("n<m: %+v", res.Summary)
	}
}

func TestEngineSelectionAll(t *testing.T) {
	universe := []InstrumentInputs{withSort(fullGate("r1", true, "5"), "2"), withSort(fullGate("r2", true, "5"), "1")}
	def := sortUniverseDef()
	def.Selection = Selection{Mode: SelectionAll}
	res := runUniverse(t, def, universe)
	if res.Summary.Selected != 2 || res.Summary.NotSelected != 0 {
		t.Errorf("selection all: %+v", res.Summary)
	}
	for _, r := range res.Rows {
		if !r.Selected || r.Stage != StageSelected {
			t.Errorf("row %s not selected: stage=%s", r.InstrumentID, r.Stage)
		}
	}
}

func TestEngineMissingSortIsInsufficient(t *testing.T) {
	universe := []InstrumentInputs{fullGate("t1", true, "5")}
	res := runUniverse(t, sortUniverseDef(), universe)
	if len(res.Rows) != 1 || res.Rows[0].Stage != StageRankInsufficient {
		t.Fatalf("want rank_insufficient, got %+v", res.Rows)
	}
	if res.Rows[0].Rank != 0 {
		t.Errorf("unranked row must have rank 0")
	}
}

func TestEngineExcludedRowsSortedAfter(t *testing.T) {
	// rankables first, excluded after sorted by instrument asc
	universe := []InstrumentInputs{
		withSort(fullGate("a", true, "5"), "2"),
		withSort(fullGate("b", true, "5"), "1"),
		fullGate("z", true, "0"), // false (gate 0) → excluded
		fullGate("t1", true, "5"),
	}
	res := runUniverse(t, sortUniverseDef(), universe)
	if len(res.Rows) != 4 {
		t.Fatalf("want 4 rows got %d", len(res.Rows))
	}
	// first two are rankable and selected in sort asc order 1,2
	if res.Rows[0].Stage != StageSelected || res.Rows[1].Stage != StageNotSelected {
		t.Errorf("rankable must precede excluded: %s,%s", res.Rows[0].Stage, res.Rows[1].Stage)
	}
	// last two excluded; the false(0) row must appear before the insufficient
	// one even though it is "not selected"
	for i := 2; i < len(res.Rows); i++ {
		if res.Rows[i].Stage == StageSelected || res.Rows[i].Stage == StageNotSelected {
			t.Errorf("row %d must be excluded, got %s", i, res.Rows[i].Stage)
		}
	}
}
