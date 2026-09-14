package screening

import (
	"encoding/json"

	"github.com/injoyai/strategy/internal/domain"
)

// RuleSchemaVersion identifies the screener rule schema an immutable version
// was saved under. It rides on every version row so a future schema change
// can never silently reinterpret an older definition.
const RuleSchemaVersion = "screener-rule/1"

// The wire model mirrors the OpenAPI Screen* schemas (docs/api/openapi.json).
// Conditions and input bindings are discriminated unions there, so the Go
// structs below stay lenient when decoding (they accept every branch field)
// and emit exactly one branch when encoding — a response therefore satisfies
// the contract's oneOf rather than a widened superset.
//
// Name, description and parent_id are deliberately outside the hashed
// definition: like universe versions, the hash identifies the rule contract
// itself, not the label it was saved under or the version it descends from.

// WireInputBinding is one declared input binding. FactorRef stays decodable
// (the custom marshaler emits it inside the factor branch only); a zero value
// means the payload did not carry one, which bindingOfWire rejects — an empty
// reference is never a valid binding.
type WireInputBinding struct {
	BindingID domain.ID         `json:"binding_id"`
	Kind      BindingKind       `json:"kind"`
	Dataset   string            `json:"dataset,omitempty"`
	Field     string            `json:"field,omitempty"`
	FactorRef domain.VersionRef `json:"factor_ref,omitempty"`
	Params    Params            `json:"params,omitempty"`
}

// fieldBindingWire and factorBindingWire are the exact contract branches.
type fieldBindingWire struct {
	BindingID domain.ID   `json:"binding_id"`
	Kind      BindingKind `json:"kind"`
	Dataset   string      `json:"dataset"`
	Field     string      `json:"field"`
}

type factorBindingWire struct {
	BindingID domain.ID         `json:"binding_id"`
	Kind      BindingKind       `json:"kind"`
	FactorRef domain.VersionRef `json:"factor_ref"`
	Params    Params            `json:"params"`
}

// MarshalJSON emits the branch selected by Kind; unknown kinds are an error
// rather than a silently widened payload.
func (b WireInputBinding) MarshalJSON() ([]byte, error) {
	switch b.Kind {
	case BindingField:
		return json.Marshal(fieldBindingWire{BindingID: b.BindingID, Kind: b.Kind, Dataset: b.Dataset, Field: b.Field})
	case BindingFactor:
		params := b.Params
		if params == nil {
			params = Params{}
		}
		return json.Marshal(factorBindingWire{BindingID: b.BindingID, Kind: b.Kind, FactorRef: b.FactorRef, Params: params})
	default:
		return nil, domain.NewError(codeBindingInvalid, "screening: unknown binding kind %q", b.Kind)
	}
}

// WireInput references one declared binding.
type WireInput struct {
	BindingID domain.ID `json:"binding_id"`
}

// WireCondition is one condition-tree node.
type WireCondition struct {
	NodeID string `json:"node_id"`
	Kind   string `json:"kind"`

	Children []WireCondition `json:"children,omitempty"`
	Child    *WireCondition  `json:"child,omitempty"`

	Input    *WireInput     `json:"input,omitempty"`
	Operator Operator       `json:"operator,omitempty"`
	Value    *domain.Value  `json:"value,omitempty"`
	Lower    *domain.Value  `json:"lower,omitempty"`
	Upper    *domain.Value  `json:"upper,omitempty"`
	In       []domain.Value `json:"in,omitempty"`
	NotIn    []domain.Value `json:"not_in,omitempty"`

	// Pointers keep the contract's explicit booleans distinguishable from
	// "absent", which matters for range bounds and missing checks.
	LowerInclusive *bool `json:"lower_inclusive,omitempty"`
	UpperInclusive *bool `json:"upper_inclusive,omitempty"`
	IsMissing      *bool `json:"is_missing,omitempty"`
	IsPresent      *bool `json:"is_present,omitempty"`
}

type compareConditionWire struct {
	NodeID   string        `json:"node_id"`
	Kind     string        `json:"kind"`
	Input    *WireInput    `json:"input"`
	Operator Operator      `json:"operator"`
	Value    *domain.Value `json:"value"`
}

type rangeConditionWire struct {
	NodeID         string        `json:"node_id"`
	Kind           string        `json:"kind"`
	Input          *WireInput    `json:"input"`
	Lower          *domain.Value `json:"lower"`
	Upper          *domain.Value `json:"upper"`
	LowerInclusive bool          `json:"lower_inclusive"`
	UpperInclusive bool          `json:"upper_inclusive"`
}

type setConditionWire struct {
	NodeID string         `json:"node_id"`
	Kind   string         `json:"kind"`
	Input  *WireInput     `json:"input"`
	In     []domain.Value `json:"in,omitempty"`
	NotIn  []domain.Value `json:"not_in,omitempty"`
}

type missingConditionWire struct {
	NodeID    string     `json:"node_id"`
	Kind      string     `json:"kind"`
	Input     *WireInput `json:"input"`
	IsMissing *bool      `json:"is_missing,omitempty"`
	IsPresent *bool      `json:"is_present,omitempty"`
}

// MarshalJSON emits the branch selected by Kind. Range bounds are always
// emitted, including null for an open end, because the contract lists them as
// required nullable fields.
func (c WireCondition) MarshalJSON() ([]byte, error) {
	switch c.Kind {
	case "all", "any":
		return json.Marshal(struct {
			NodeID   string          `json:"node_id"`
			Kind     string          `json:"kind"`
			Children []WireCondition `json:"children"`
		}{c.NodeID, c.Kind, nonNilConditions(c.Children)})
	case "not":
		return json.Marshal(struct {
			NodeID string         `json:"node_id"`
			Kind   string         `json:"kind"`
			Child  *WireCondition `json:"child"`
		}{c.NodeID, c.Kind, c.Child})
	case "compare":
		return json.Marshal(compareConditionWire{c.NodeID, c.Kind, c.Input, c.Operator, c.Value})
	case "range":
		return json.Marshal(rangeConditionWire{
			NodeID:         c.NodeID,
			Kind:           c.Kind,
			Input:          c.Input,
			Lower:          c.Lower,
			Upper:          c.Upper,
			LowerInclusive: c.LowerInclusive != nil && *c.LowerInclusive,
			UpperInclusive: c.UpperInclusive != nil && *c.UpperInclusive,
		})
	case "set":
		return json.Marshal(setConditionWire{c.NodeID, c.Kind, c.Input, c.In, c.NotIn})
	case "missing":
		return json.Marshal(missingConditionWire{c.NodeID, c.Kind, c.Input, c.IsMissing, c.IsPresent})
	default:
		return nil, domain.NewError(codeNodeIDInvalid, "screening: unknown condition kind %q", c.Kind)
	}
}

func nonNilConditions(in []WireCondition) []WireCondition {
	if in == nil {
		return []WireCondition{}
	}
	return in
}

// WireRankField and WireScoreComponent are the two ranking branches.
type WireRankField struct {
	Input     WireInput `json:"input"`
	Direction Direction `json:"direction"`
}

type WireScoreComponent struct {
	Input     WireInput      `json:"input"`
	Weight    domain.Decimal `json:"weight"`
	Direction ScoreDirection `json:"direction"`
}

// WireRanking is a discriminated union on mode.
type WireRanking struct {
	Mode       RankingMode          `json:"mode"`
	Fields     []WireRankField      `json:"fields,omitempty"`
	Components []WireScoreComponent `json:"components,omitempty"`
}

// WireSelection is a discriminated union on mode.
type WireSelection struct {
	Mode SelectionMode `json:"mode"`
	N    int64         `json:"n,omitempty"`
}

// WireDefinition is the canonical, hashable definition payload: exactly the
// fields an immutable screener version freezes, in declaration order. The
// struct layout is fixed so marshaling is byte-stable for one definition.
type WireDefinition struct {
	InputBindings  []WireInputBinding `json:"input_bindings"`
	ConditionTree  WireCondition      `json:"condition_tree"`
	Ranking        WireRanking        `json:"ranking"`
	Selection      WireSelection      `json:"selection"`
	DisplayColumns []domain.ID        `json:"display_columns"`
}

// WireScreener is the wire form of one immutable screener version.
type WireScreener struct {
	ID                domain.ID          `json:"id"`
	Version           domain.ID          `json:"version"`
	Name              string             `json:"name"`
	Description       string             `json:"description,omitempty"`
	InputBindings     []WireInputBinding `json:"input_bindings"`
	ConditionTree     WireCondition      `json:"condition_tree"`
	Ranking           WireRanking        `json:"ranking"`
	Selection         WireSelection      `json:"selection"`
	DisplayColumns    []domain.ID        `json:"display_columns"`
	ParentID          domain.ID          `json:"parent_id,omitempty"`
	RuleSchemaVersion string             `json:"rule_schema_version"`
	CreatedAt         string             `json:"created_at"`
}

// WireDefinitionOf projects a definition onto its canonical payload.
func WireDefinitionOf(def Definition) WireDefinition {
	bindings := make([]WireInputBinding, 0, len(def.InputBindings))
	for _, b := range def.InputBindings {
		bindings = append(bindings, wireBindingOf(b))
	}
	columns := def.DisplayColumns
	if columns == nil {
		columns = []domain.ID{}
	}
	return WireDefinition{
		InputBindings:  bindings,
		ConditionTree:  wireConditionOf(def.ConditionTree),
		Ranking:        wireRankingOf(def.Ranking),
		Selection:      WireSelection{Mode: def.Selection.Mode, N: def.Selection.N},
		DisplayColumns: columns,
	}
}

// DefinitionOfWire rebuilds the engine definition from its stored payload.
// It is the inverse of WireDefinitionOf; validation stays the caller's job so
// a stored definition is re-checked on every load.
func DefinitionOfWire(payload WireDefinition, name, description string, parentID domain.ID) (Definition, error) {
	conditions, err := conditionOfWire(payload.ConditionTree)
	if err != nil {
		return Definition{}, err
	}
	bindings := make([]InputBinding, 0, len(payload.InputBindings))
	for _, b := range payload.InputBindings {
		binding, err := bindingOfWire(b)
		if err != nil {
			return Definition{}, err
		}
		bindings = append(bindings, binding)
	}
	ranking := Ranking{Mode: payload.Ranking.Mode}
	for _, f := range payload.Ranking.Fields {
		ranking.Fields = append(ranking.Fields, RankField{Input: Input{BindingID: f.Input.BindingID}, Direction: f.Direction})
	}
	for _, c := range payload.Ranking.Components {
		ranking.Components = append(ranking.Components, ScoreComponent{
			Input:     Input{BindingID: c.Input.BindingID},
			Weight:    c.Weight,
			Direction: c.Direction,
		})
	}
	columns := payload.DisplayColumns
	if columns == nil {
		columns = []domain.ID{}
	}
	return Definition{
		Name:           name,
		Description:    description,
		InputBindings:  bindings,
		ConditionTree:  conditions,
		Ranking:        ranking,
		Selection:      Selection{Mode: payload.Selection.Mode, N: payload.Selection.N},
		DisplayColumns: columns,
		ParentID:       parentID,
	}, nil
}

func wireBindingOf(b InputBinding) WireInputBinding {
	out := WireInputBinding{BindingID: b.BindingID, Kind: b.Kind}
	switch b.Kind {
	case BindingField:
		out.Dataset = b.Dataset
		out.Field = b.Field
	case BindingFactor:
		out.FactorRef = b.FactorRef
		out.Params = b.Params
		if out.Params == nil {
			out.Params = Params{}
		}
	}
	return out
}

func bindingOfWire(b WireInputBinding) (InputBinding, error) {
	out := InputBinding{BindingID: b.BindingID, Kind: b.Kind}
	switch b.Kind {
	case BindingField:
		out.Dataset = b.Dataset
		out.Field = b.Field
	case BindingFactor:
		if b.FactorRef.ID == "" || b.FactorRef.Version == "" {
			return InputBinding{}, domain.NewError(codeBindingInvalid, "screening: factor binding %s requires factor_ref", b.BindingID)
		}
		out.FactorRef = b.FactorRef
		out.Params = b.Params
	default:
		return InputBinding{}, domain.NewError(codeBindingInvalid, "screening: unknown binding kind %q", b.Kind)
	}
	return out, nil
}

func wireConditionOf(node Condition) WireCondition {
	switch n := node.(type) {
	case All:
		children := make([]WireCondition, 0, len(n.Children))
		for _, c := range n.Children {
			children = append(children, wireConditionOf(c))
		}
		return WireCondition{NodeID: n.NodeID.String(), Kind: "all", Children: children}
	case Any:
		children := make([]WireCondition, 0, len(n.Children))
		for _, c := range n.Children {
			children = append(children, wireConditionOf(c))
		}
		return WireCondition{NodeID: n.NodeID.String(), Kind: "any", Children: children}
	case Not:
		child := wireConditionOf(n.Child)
		return WireCondition{NodeID: n.NodeID.String(), Kind: "not", Child: &child}
	case Compare:
		return WireCondition{
			NodeID:   n.NodeID.String(),
			Kind:     "compare",
			Input:    &WireInput{BindingID: n.Input.BindingID},
			Operator: n.Operator,
			Value:    valuePtr(n.Value),
		}
	case Range:
		return WireCondition{
			NodeID:         n.NodeID.String(),
			Kind:           "range",
			Input:          &WireInput{BindingID: n.Input.BindingID},
			Lower:          n.Lower,
			Upper:          n.Upper,
			LowerInclusive: boolPtr(n.LowerInclusive),
			UpperInclusive: boolPtr(n.UpperInclusive),
		}
	case Set:
		values := make([]domain.Value, 0, len(n.Values))
		values = append(values, n.Values...)
		out := WireCondition{NodeID: n.NodeID.String(), Kind: "set", Input: &WireInput{BindingID: n.Input.BindingID}}
		if n.NotIn {
			out.NotIn = values
		} else {
			out.In = values
		}
		return out
	case Missing:
		out := WireCondition{NodeID: n.NodeID.String(), Kind: "missing", Input: &WireInput{BindingID: n.Input.BindingID}}
		if n.IsPresent {
			out.IsPresent = boolPtr(true)
		} else {
			out.IsMissing = boolPtr(true)
		}
		return out
	default:
		return WireCondition{}
	}
}

func conditionOfWire(c WireCondition) (Condition, error) {
	nodeID := domain.ID(c.NodeID)
	switch c.Kind {
	case "all":
		children, err := conditionsOfWire(c.Children)
		if err != nil {
			return nil, err
		}
		return All{NodeID: nodeID, Children: children}, nil
	case "any":
		children, err := conditionsOfWire(c.Children)
		if err != nil {
			return nil, err
		}
		return Any{NodeID: nodeID, Children: children}, nil
	case "not":
		if c.Child == nil {
			return nil, domain.NewError(codeChildRequired, "screening: not node %s requires a child", c.NodeID)
		}
		child, err := conditionOfWire(*c.Child)
		if err != nil {
			return nil, err
		}
		return Not{NodeID: nodeID, Child: child}, nil
	case "compare":
		if c.Input == nil || c.Value == nil {
			return nil, domain.NewError(codeInputUnknown, "screening: compare node %s requires input and value", c.NodeID)
		}
		return Compare{NodeID: nodeID, Input: Input{BindingID: c.Input.BindingID}, Operator: c.Operator, Value: *c.Value}, nil
	case "range":
		if c.Input == nil {
			return nil, domain.NewError(codeInputUnknown, "screening: range node %s requires input", c.NodeID)
		}
		return Range{
			NodeID:         nodeID,
			Input:          Input{BindingID: c.Input.BindingID},
			Lower:          c.Lower,
			Upper:          c.Upper,
			LowerInclusive: c.LowerInclusive != nil && *c.LowerInclusive,
			UpperInclusive: c.UpperInclusive != nil && *c.UpperInclusive,
		}, nil
	case "set":
		if c.Input == nil {
			return nil, domain.NewError(codeInputUnknown, "screening: set node %s requires input", c.NodeID)
		}
		values := c.In
		notIn := false
		if len(c.NotIn) > 0 {
			values = c.NotIn
			notIn = true
		}
		return Set{NodeID: nodeID, Input: Input{BindingID: c.Input.BindingID}, Values: values, NotIn: notIn}, nil
	case "missing":
		if c.Input == nil {
			return nil, domain.NewError(codeInputUnknown, "screening: missing node %s requires input", c.NodeID)
		}
		isPresent := c.IsPresent != nil && *c.IsPresent
		return Missing{
			NodeID:    nodeID,
			Input:     Input{BindingID: c.Input.BindingID},
			IsMissing: !isPresent,
			IsPresent: isPresent,
		}, nil
	default:
		return nil, domain.NewError(codeChildRequired, "screening: unknown condition kind %q", c.Kind)
	}
}

func conditionsOfWire(in []WireCondition) ([]Condition, error) {
	out := make([]Condition, 0, len(in))
	for _, c := range in {
		node, err := conditionOfWire(c)
		if err != nil {
			return nil, err
		}
		out = append(out, node)
	}
	return out, nil
}

func wireRankingOf(r Ranking) WireRanking {
	switch r.Mode {
	case RankingScore:
		components := make([]WireScoreComponent, 0, len(r.Components))
		for _, c := range r.Components {
			components = append(components, WireScoreComponent{
				Input:     WireInput{BindingID: c.Input.BindingID},
				Weight:    c.Weight,
				Direction: c.Direction,
			})
		}
		return WireRanking{Mode: r.Mode, Components: components}
	default:
		fields := make([]WireRankField, 0, len(r.Fields))
		for _, f := range r.Fields {
			fields = append(fields, WireRankField{Input: WireInput{BindingID: f.Input.BindingID}, Direction: f.Direction})
		}
		return WireRanking{Mode: r.Mode, Fields: fields}
	}
}

func valuePtr(v domain.Value) *domain.Value { return &v }

func boolPtr(b bool) *bool { return &b }
