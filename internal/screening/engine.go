package screening

import (
	"sort"

	"github.com/injoyai/strategy/internal/domain"
)

type RequiredValuePolicy string

const (
	PolicyExcludeInstrument RequiredValuePolicy = "exclude_instrument"
	PolicyFailRun           RequiredValuePolicy = "fail_run"
)

type Policy struct {
	RequiredValue RequiredValuePolicy
	Scoring       Scoring
}

type InstrumentInputs struct {
	InstrumentID domain.ID
	Values       Inputs
}

type RowStage string

const (
	StageSelected         RowStage = "selected"
	StageConditionFalse   RowStage = "condition_false"
	StageConditionUnknown RowStage = "condition_unknown"
	StageRankInsufficient RowStage = "rank_insufficient"
	StageNotSelected      RowStage = "not_selected"
)

// Row is one frozen result row. The JSON encoding is the run's persisted form:
// a published result must be readable after the engine that produced it has
// changed, so the payload is explicit rather than implied by field order.
type Row struct {
	InstrumentID domain.ID        `json:"instrument_id"`
	Stage        RowStage         `json:"stage"`
	Rank         int64            `json:"rank"`
	Score        domain.Decimal   `json:"score,omitempty"`
	HasScore     bool             `json:"has_score"`
	Selected     bool             `json:"selected"`
	Values       Inputs           `json:"values"`
	Nodes        []NodeEvaluation `json:"nodes"`
	ScoreDetail  []ScoreEvidence  `json:"score_detail"`
}

// Summary is the mutually exclusive stage rollup of one run; the field names
// match the contract's ScreenSummary schema.
type Summary struct {
	Population       int    `json:"population"`
	ConditionFalse   int    `json:"condition_false"`
	ConditionUnknown int    `json:"condition_unknown"`
	ConditionTrue    int    `json:"condition_true"`
	RankInsufficient int    `json:"rank_insufficient"`
	Rankable         int    `json:"rankable"`
	Selected         int    `json:"selected"`
	NotSelected      int    `json:"not_selected"`
	EmptyReason      string `json:"empty_reason,omitempty"`
}

const (
	EmptyPopulation       = "empty_population"
	ConditionExcludedAll  = "condition_excluded_all"
	NoRankableInstruments = "no_rankable_instruments"
)

type Result struct {
	Summary Summary
	Rows    []Row
}

type rankable struct {
	rr     rankRow
	nodes  []NodeEvaluation
	values Inputs
}

type excluded struct {
	instrument domain.ID
	stage      RowStage
	nodes      []NodeEvaluation
	values     Inputs
}

// Run executes a frozen definition against one mother pool. Pure: no
// database, network or clock. Validation and policy checks are defensive
// re-checks; call sites normally validate before run. Sharding is only allowed
// across read/compute boundaries; sorting, percentile and selection always
// use the full pool so a single batch and sharded runs agree item-by-item.
func Run(def Definition, catalog InputTypes, policy Policy, limits Limits, universe []InstrumentInputs) (Result, error) {
	if issues := Validate(def, catalog, limits); len(issues) > 0 {
		return Result{}, domain.NewError(codeDefinitionInvalid, "screener definition is invalid: %s", issues[0].Message)
	}
	if err := validatePolicy(def, policy); err != nil {
		return Result{}, err
	}
	if err := ensureUniqueInstruments(universe); err != nil {
		return Result{}, err
	}

	var candidates []rankable
	var excludedRows []excluded
	failUnknown, failInsufficient := false, false

	for _, inst := range universe {
		truth, nodes := Evaluate(def.ConditionTree, inst.Values)
		switch truth {
		case TruthFalse:
			excludedRows = append(excludedRows, excluded{inst.InstrumentID, StageConditionFalse, nodes, inst.Values})
			continue
		case TruthUnknown:
			failUnknown = true
			excludedRows = append(excludedRows, excluded{inst.InstrumentID, StageConditionUnknown, nodes, inst.Values})
			continue
		}
		rr, ok := buildRankRow(inst, def)
		if !ok {
			failInsufficient = true
			excludedRows = append(excludedRows, excluded{inst.InstrumentID, StageRankInsufficient, nodes, inst.Values})
			continue
		}
		candidates = append(candidates, rankable{rr: rr, nodes: nodes, values: inst.Values})
	}

	if policy.RequiredValue == PolicyFailRun && (failUnknown || failInsufficient) {
		return Result{}, domain.NewError(codeRequiredValueMissing, "run requires values that are missing or unparseable")
	}

	rows := make([]rankRow, len(candidates))
	for i := range candidates {
		rows[i] = candidates[i].rr
	}
	if err := orderCandidates(rows, def, policy); err != nil {
		return Result{}, err
	}

	m := len(rows)
	limit := m
	if def.Selection.Mode == SelectionTopN && int(def.Selection.N) < m {
		limit = int(def.Selection.N)
	}

	byInst := make(map[domain.ID]int, len(candidates))
	for i := range candidates {
		byInst[candidates[i].rr.instrument] = i
	}

	res := Result{}
	res.Summary.Rankable = m
	res.Summary.Selected = limit
	res.Summary.NotSelected = m - limit

	for i := 0; i < m; i++ {
		ci := byInst[rows[i].instrument]
		stage := StageNotSelected
		selected := false
		if i < limit {
			stage = StageSelected
			selected = true
		}
		row := Row{
			InstrumentID: rows[i].instrument,
			Stage:        stage,
			Rank:         int64(i + 1),
			Selected:     selected,
			Values:       candidates[ci].values,
			Nodes:        candidates[ci].nodes,
		}
		if rows[i].hasScore {
			sc, err := rows[i].score.Round(policy.Scoring.Places, policy.Scoring.Rounding)
			if err == nil {
				row.Score = sc
				row.HasScore = true
			}
		}
		if def.Ranking.Mode == RankingScore {
			row.ScoreDetail = rows[i].evidence
		}
		res.Rows = append(res.Rows, row)
	}

	sort.SliceStable(excludedRows, func(i, j int) bool {
		return excludedRows[i].instrument.String() < excludedRows[j].instrument.String()
	})
	for _, e := range excludedRows {
		res.Rows = append(res.Rows, Row{
			InstrumentID: e.instrument,
			Stage:        e.stage,
			Nodes:        e.nodes,
			Values:       e.values,
		})
	}

	res.Summary.Population = len(universe)
	res.Summary.ConditionFalse = countStage(excludedRows, StageConditionFalse)
	res.Summary.ConditionUnknown = countStage(excludedRows, StageConditionUnknown)
	res.Summary.RankInsufficient = countStage(excludedRows, StageRankInsufficient)
	res.Summary.ConditionTrue = res.Summary.RankInsufficient + res.Summary.Rankable
	switch {
	case res.Summary.Selected > 0:
	case res.Summary.Population == 0:
		res.Summary.EmptyReason = EmptyPopulation
	case res.Summary.ConditionTrue == 0:
		res.Summary.EmptyReason = ConditionExcludedAll
	default:
		res.Summary.EmptyReason = NoRankableInstruments
	}
	return res, nil
}

func countStage(rows []excluded, stage RowStage) int {
	n := 0
	for _, r := range rows {
		if r.stage == stage {
			n++
		}
	}
	return n
}

func validatePolicy(def Definition, policy Policy) error {
	switch policy.RequiredValue {
	case PolicyExcludeInstrument, PolicyFailRun:
	default:
		return domain.NewError(codeInvalidPolicy, "unknown required_value policy %q", policy.RequiredValue)
	}
	if def.Ranking.Mode == RankingScore {
		if policy.Scoring.Places < 1 || policy.Scoring.Places > 12 {
			return domain.NewError(codeInvalidPolicy, "scoring places must be within 1..12")
		}
		if !validRounding(policy.Scoring.Rounding) {
			return domain.NewError(codeInvalidPolicy, "unknown scoring rounding mode")
		}
	}
	return nil
}

func validRounding(m domain.RoundingMode) bool {
	switch m {
	case domain.RoundHalfAwayFromZero, domain.RoundHalfEven, domain.RoundDown,
		domain.RoundUp, domain.RoundCeil, domain.RoundFloor:
		return true
	default:
		return false
	}
}

func ensureUniqueInstruments(universe []InstrumentInputs) error {
	seen := map[domain.ID]bool{}
	for _, inst := range universe {
		if inst.InstrumentID == "" {
			return domain.NewError(codeDuplicateInstrument, "instrument id is required")
		}
		if seen[inst.InstrumentID] {
			return domain.NewError(codeDuplicateInstrument, "duplicate instrument %s", inst.InstrumentID)
		}
		seen[inst.InstrumentID] = true
	}
	return nil
}

func buildRankRow(inst InstrumentInputs, def Definition) (rankRow, bool) {
	rr := rankRow{instrument: inst.InstrumentID}
	switch def.Ranking.Mode {
	case RankingSort:
		for _, f := range def.Ranking.Fields {
			pv, _, ok := resolveInstrumentInput(inst.Values, f.Input)
			if !ok {
				return rankRow{}, false
			}
			rr.sortKeys = append(rr.sortKeys, pv)
		}
	case RankingScore:
		for _, c := range def.Ranking.Components {
			pv, raw, ok := resolveInstrumentInput(inst.Values, c.Input)
			if !ok {
				return rankRow{}, false
			}
			rr.compValues = append(rr.compValues, pv)
			rr.compRaws = append(rr.compRaws, raw)
		}
	}
	return rr, true
}

func resolveInstrumentInput(values Inputs, in Input) (parsedValue, domain.Value, bool) {
	raw, ok := values[in.BindingID]
	if !ok || raw.MissingReason != "" || raw.Kind == "" {
		return parsedValue{}, domain.Value{}, false
	}
	pv, err := parseValue(raw)
	if err != nil {
		return parsedValue{}, domain.Value{}, false
	}
	return pv, raw, true
}

func orderCandidates(rows []rankRow, def Definition, policy Policy) error {
	switch def.Ranking.Mode {
	case RankingSort:
		sort.SliceStable(rows, func(i, j int) bool {
			return compareRows(rows[i], rows[j], def.Ranking.Fields) < 0
		})
	case RankingScore:
		if err := scoreRows(rows, def.Ranking.Components, policy.Scoring); err != nil {
			return err
		}
		sort.SliceStable(rows, func(i, j int) bool {
			c := compareDec(rows[i].score, rows[j].score)
			if c != 0 {
				return c > 0
			}
			return rows[i].instrument.String() < rows[j].instrument.String()
		})
	}
	return nil
}
