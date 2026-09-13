package screening

import (
	"math/big"
	"reflect"
	"testing"

	"github.com/injoyai/strategy/internal/domain"
)

func big10(p int64) *big.Int {
	return new(big.Int).Exp(big.NewInt(10), big.NewInt(p), nil)
}

func scoreDef() Definition {
	return Definition{
		Name: "score",
		InputBindings: []InputBinding{
			{BindingID: idGate, Kind: BindingField, Dataset: "d", Field: "gate"},
			{BindingID: idScore, Kind: BindingField, Dataset: "d", Field: "score"},
			{BindingID: idVol, Kind: BindingField, Dataset: "d", Field: "vol"},
		},
		ConditionTree: Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "-1000000")},
		Ranking:       Ranking{Mode: RankingScore, Components: []ScoreComponent{{Input: Input{BindingID: idScore}, Weight: pdec("1"), Direction: ScoreLargerBetter}}},
		Selection:     Selection{Mode: SelectionAll},
	}
}

func runUniverse(t *testing.T, def Definition, universe []InstrumentInputs) Result {
	t.Helper()
	res, err := Run(def, baseCatalog(), Policy{RequiredValue: PolicyExcludeInstrument, Scoring: DefaultScoring()}, DefaultLimits(), universe)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	return res
}

func inputOf(t *testing.T, id string, encoded string) InstrumentInputs {
	mid, err := domain.ParseID(id)
	if err != nil {
		t.Fatalf("id %q: %v", id, err)
	}
	return InstrumentInputs{
		InstrumentID: mid,
		Values:       Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, encoded)},
	}
}

func TestScoreSingleCandidateHalf(t *testing.T) {
	res := runUniverse(t, scoreDef(), []InstrumentInputs{
		{InstrumentID: mustID("inst1"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "7")}},
	})
	if len(res.Rows) != 1 {
		t.Fatalf("want 1 row got %d", len(res.Rows))
	}
	if res.Rows[0].Score != mustDec(t, "0.5") {
		t.Errorf("m=1 percentile = %s, want 0.5", res.Rows[0].Score)
	}
}

func TestScoreAllEqualHalf(t *testing.T) {
	universe := []InstrumentInputs{
		{InstrumentID: mustID("i1"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "4")}},
		{InstrumentID: mustID("i2"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "4")}},
		{InstrumentID: mustID("i3"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "4")}},
	}
	res := runUniverse(t, scoreDef(), universe)
	if len(res.Rows) != 3 {
		t.Fatalf("want 3 rows got %d", len(res.Rows))
	}
	for _, r := range res.Rows {
		if r.Score != mustDec(t, "0.5") {
			t.Errorf("all-equal score = %s, want 0.5", r.Score)
		}
	}
}

// Values 1,2,2,4 over ids A,B,C,D. Average-rank tie: B and C share 0.5.
// larger_is_better descending order: D(1), B/C(0.5), A(0) → order D,B,C,A.
func TestScoreTieAverageRankOrder(t *testing.T) {
	fixtures := []struct {
		id  string
		val int
	}{
		{"a", 1}, {"b", 2}, {"c", 2}, {"d", 4},
	}
	var universe []InstrumentInputs
	for _, f := range fixtures {
		universe = append(universe, inputOf(t, f.id, itoa(f.val)))
	}
	res := runUniverse(t, scoreDef(), universe)
	wantOrder := []string{"d", "b", "c", "a"}
	wantScores := []string{"1", "0.5", "0.5", "0"}
	for i, r := range res.Rows {
		if r.InstrumentID.String() != wantOrder[i] {
			t.Errorf("rank %d instrument = %s, want %s", i, r.InstrumentID, wantOrder[i])
		}
		if got := r.Score; got != mustDec(t, wantScores[i]) {
			t.Errorf("rank %d score = %s, want %s", i, got, wantScores[i])
		}
		if r.Rank != int64(i+1) {
			t.Errorf("rank %d declared rank %d", i, r.Rank)
		}
	}
}

func TestScoreSmallerBetter(t *testing.T) {
	def := scoreDef()
	def.Ranking = Ranking{Mode: RankingScore, Components: []ScoreComponent{{Input: Input{BindingID: idScore}, Weight: mustDec(t, "1"), Direction: ScoreSmallerBetter}}}
	universe := []InstrumentInputs{
		{InstrumentID: mustID("x1"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "1")}},
		{InstrumentID: mustID("x2"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "9")}},
	}
	res := runUniverse(t, def, universe)
	// smaller value 1 → percentile 0 → directed = 1-0 = 1 → ranks first
	if res.Rows[0].InstrumentID.String() != "x1" {
		t.Errorf("smaller_is_better: want x1 first, got %s", res.Rows[0].InstrumentID)
	}
	if res.Rows[0].Score != mustDec(t, "1") || res.Rows[1].Score != mustDec(t, "0") {
		t.Errorf("smaller_is_better scores = %s,%s want 1,0", res.Rows[0].Score, res.Rows[1].Score)
	}
}

func sortDefDesc() Definition {
	return Definition{
		Name: "sort",
		InputBindings: []InputBinding{
			{BindingID: idGate, Kind: BindingField, Dataset: "d", Field: "gate"},
			{BindingID: idSort, Kind: BindingField, Dataset: "d", Field: "sort"},
		},
		ConditionTree: Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "-1000000")},
		Ranking:       Ranking{Mode: RankingSort, Fields: []RankField{{Input: Input{BindingID: idSort}, Direction: DirectionDesc}}},
		Selection:     Selection{Mode: SelectionAll},
	}
}

func TestSortStableTieBreak(t *testing.T) {
	universe := []InstrumentInputs{
		{InstrumentID: mustID("b"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idSort: val(domain.ValueDecimal, "3")}},
		{InstrumentID: mustID("a"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idSort: val(domain.ValueDecimal, "3")}},
		{InstrumentID: mustID("c"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idSort: val(domain.ValueDecimal, "1")}},
	}
	res := runUniverse(t, sortDefDesc(), universe)
	// desc: values 3,3,1 → ties a,b resolve by instrument asc → order a,b,c
	wantOrder := []string{"a", "b", "c"}
	for i, r := range res.Rows {
		if r.InstrumentID.String() != wantOrder[i] {
			t.Errorf("sort tie rank %d = %s, want %s", i, r.InstrumentID, wantOrder[i])
		}
	}
}

func TestScoreZeroWeightEvidence(t *testing.T) {
	def := Definition{
		Name: "score2",
		InputBindings: []InputBinding{
			{BindingID: idGate, Kind: BindingField, Dataset: "d", Field: "gate"},
			{BindingID: idScore, Kind: BindingField, Dataset: "d", Field: "score"},
			{BindingID: idVol, Kind: BindingField, Dataset: "d", Field: "vol"},
		},
		ConditionTree: Compare{NodeID: idNode1, Input: Input{BindingID: idGate}, Operator: OpGt, Value: val(domain.ValueDecimal, "-1000000")},
		Ranking: Ranking{Mode: RankingScore, Components: []ScoreComponent{
			{Input: Input{BindingID: idScore}, Weight: mustDec(t, "1"), Direction: ScoreLargerBetter},
			{Input: Input{BindingID: idVol}, Weight: mustDec(t, "0"), Direction: ScoreLargerBetter},
		}},
		Selection: Selection{Mode: SelectionAll},
	}
	universe := []InstrumentInputs{
		{InstrumentID: mustID("i1"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "6"), idVol: val(domain.ValueDecimal, "9")}},
	}
	res := runUniverse(t, def, universe)
	if len(res.Rows) != 1 {
		t.Fatalf("want 1 row got %d", len(res.Rows))
	}
	r := res.Rows[0]
	if r.Score != mustDec(t, "0.5") {
		t.Errorf("zero-weight should not change score, got %s", r.Score)
	}
	if len(r.ScoreDetail) != 2 {
		t.Fatalf("want 2 evidence entries (zero weight kept), got %d", len(r.ScoreDetail))
	}
	if r.ScoreDetail[0].Contribution != mustDec(t, "0.5") || r.ScoreDetail[1].Contribution != mustDec(t, "0") {
		t.Errorf("contributions = %s,%s want 0.5,0", r.ScoreDetail[0].Contribution, r.ScoreDetail[1].Contribution)
	}
}

func TestPercentileScaledValues(t *testing.T) {
	places := big10(6)
	cases := []struct {
		first, last, m int64
		want           string
	}{
		{1, 1, 4, "0"},
		{2, 3, 4, "0.5"},
		{4, 4, 4, "1"},
		{1, 1, 1, "0.5"},
		{1, 4, 4, "0.5"},
	}
	for _, c := range cases {
		sc := percentileScaled(c.first, c.last, c.m, places)
		dec, err := scaledToDecimal(sc, 6)
		if err != nil {
			t.Fatalf("scaledToDecimal: %v", err)
		}
		if dec != mustDec(t, c.want) {
			t.Errorf("percentile(%d,%d,m=%d) = %s, want %s", c.first, c.last, c.m, dec, c.want)
		}
	}
}

func TestShuffleOracle(t *testing.T) {
	def := scoreDef()
	fixtures := []InstrumentInputs{
		{InstrumentID: mustID("a"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "1")}},
		{InstrumentID: mustID("b"), Values: Inputs{idGate: val(domain.ValueDecimal, "5"), idScore: val(domain.ValueDecimal, "2")}},
		{InstrumentID: mustID("c"), Values: Inputs{idGate: val(domain.ValueDecimal, "0"), idScore: val(domain.ValueDecimal, "3")}},
		{InstrumentID: mustID("d"), Values: Inputs{idGate: val(domain.ValueDecimal, "4")}},
	}
	order1 := []InstrumentInputs{fixtures[0], fixtures[1], fixtures[2], fixtures[3]}
	res1 := runUniverse(t, def, order1)
	order2 := []InstrumentInputs{fixtures[3], fixtures[0], fixtures[2], fixtures[1]}
	res2 := runUniverse(t, def, order2)
	if !reflect.DeepEqual(res1.Rows, res2.Rows) {
		t.Errorf("shuffle oracle mismatch:\n%+v\n%+v", res1.Rows, res2.Rows)
	}
}
