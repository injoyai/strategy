package screening

import "github.com/injoyai/strategy/internal/domain"

type Condition interface {
	conditionNode()
}

type All struct {
	NodeID   domain.ID
	Children []Condition
}

func (n All) conditionNode() {}

type Any struct {
	NodeID   domain.ID
	Children []Condition
}

func (n Any) conditionNode() {}

type Not struct {
	NodeID domain.ID
	Child  Condition
}

func (n Not) conditionNode() {}

type Compare struct {
	NodeID   domain.ID
	Input    Input
	Operator Operator
	Value    domain.Value
}

func (n Compare) conditionNode() {}

type Range struct {
	NodeID         domain.ID
	Input          Input
	Lower          *domain.Value
	Upper          *domain.Value
	LowerInclusive bool
	UpperInclusive bool
}

func (n Range) conditionNode() {}

type Set struct {
	NodeID domain.ID
	Input  Input
	Values []domain.Value
	NotIn  bool
}

func (n Set) conditionNode() {}

type Missing struct {
	NodeID    domain.ID
	Input     Input
	IsMissing bool
	IsPresent bool
}

func (n Missing) conditionNode() {}

func nodeKind(c Condition) string {
	switch c.(type) {
	case All:
		return "all"
	case Any:
		return "any"
	case Not:
		return "not"
	case Compare:
		return "compare"
	case Range:
		return "range"
	case Set:
		return "set"
	case Missing:
		return "missing"
	default:
		return ""
	}
}

type BindingKind string

const (
	BindingField  BindingKind = "field"
	BindingFactor BindingKind = "factor"
)

type Params map[string]any

type InputBinding struct {
	BindingID domain.ID
	Kind      BindingKind
	Dataset   string
	Field     string
	FactorRef domain.VersionRef
	Params    Params
}

type Input struct {
	BindingID domain.ID
}

type Operator string

const (
	OpEq  Operator = "eq"
	OpNe  Operator = "ne"
	OpGt  Operator = "gt"
	OpGte Operator = "gte"
	OpLt  Operator = "lt"
	OpLte Operator = "lte"
)

type Direction string

const (
	DirectionAsc  Direction = "asc"
	DirectionDesc Direction = "desc"
)

type ScoreDirection string

const (
	ScoreLargerBetter  ScoreDirection = "larger_is_better"
	ScoreSmallerBetter ScoreDirection = "smaller_is_better"
)

type RankField struct {
	Input     Input
	Direction Direction
}

type ScoreComponent struct {
	Input     Input
	Weight    domain.Decimal
	Direction ScoreDirection
}

type RankingMode string

const (
	RankingSort  RankingMode = "sort"
	RankingScore RankingMode = "score"
)

type Ranking struct {
	Mode       RankingMode
	Fields     []RankField
	Components []ScoreComponent
}

type SelectionMode string

const (
	SelectionAll  SelectionMode = "all"
	SelectionTopN SelectionMode = "top_n"
)

type Selection struct {
	Mode SelectionMode
	N    int64
}

type Definition struct {
	Name           string
	Description    string
	InputBindings  []InputBinding
	ConditionTree  Condition
	Ranking        Ranking
	Selection      Selection
	DisplayColumns []domain.ID
	ParentID       domain.ID
}

type InputTypes map[domain.ID]domain.ValueKind

type Limits struct {
	MaxDepth          int
	MaxNodes          int
	MaxSetLength      int
	MaxBindings       int
	MaxDisplayColumns int
}

func DefaultLimits() Limits {
	return Limits{
		MaxDepth:          16,
		MaxNodes:          256,
		MaxSetLength:      128,
		MaxBindings:       64,
		MaxDisplayColumns: 32,
	}
}
