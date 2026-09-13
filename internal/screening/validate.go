package screening

import (
	"fmt"
	"strings"

	"github.com/injoyai/strategy/internal/domain"
)

type validator struct {
	limits    Limits
	declared  map[domain.ID]bool
	nodeIDs   map[domain.ID]bool
	nodeCount int
	issues    []domain.Issue
}

func (v *validator) issue(code, path, msg string) {
	v.issues = append(v.issues, domain.Issue{
		Code:     code,
		Path:     path,
		Message:  msg,
		Severity: domain.SeverityError,
	})
}

// Validate runs save-time validation of a screener definition against the
// resolved input catalog and complexity limits. Issues are returned in a
// deterministic order (bindings slice order, then tree pre-order, then
// ranking/selection/display/parent).
func Validate(def Definition, catalog InputTypes, limits Limits) []domain.Issue {
	v := &validator{
		limits:   limits,
		declared: map[domain.ID]bool{},
		nodeIDs:  map[domain.ID]bool{},
	}
	if strings.TrimSpace(def.Name) == "" {
		v.issue(codeNameEmpty, "name", "screener name is required")
	}
	v.validateBindings(def, catalog)
	v.validateTree(def, catalog)
	v.validateRanking(def, catalog)
	v.validateSelection(def)
	v.validateDisplay(def)
	v.validateParent(def)
	return v.issues
}

func (v *validator) validateBindings(def Definition, catalog InputTypes) {
	if len(def.InputBindings) < 1 {
		v.issue(codeBindingInvalid, "input_bindings", "at least one binding is required")
	}
	if v.limits.MaxBindings > 0 && len(def.InputBindings) > v.limits.MaxBindings {
		v.issue(codeBindingInvalid, "input_bindings", fmt.Sprintf("more than %d bindings are not allowed", v.limits.MaxBindings))
	}
	seen := map[domain.ID]bool{}
	for i, b := range def.InputBindings {
		path := "input_bindings[" + itoa(i) + "]"
		bindingID, err := domain.ParseID(b.BindingID.String())
		if err != nil {
			v.issue(codeBindingInvalid, path+".binding_id", err.Error())
		} else {
			v.declared[bindingID] = true
			if seen[bindingID] {
				v.issue(codeBindingDuplicate, path+".binding_id", "duplicate binding_id")
			} else {
				seen[bindingID] = true
			}
		}
		switch b.Kind {
		case BindingField:
			if strings.TrimSpace(b.Dataset) == "" {
				v.issue(codeBindingInvalid, path+".dataset", "dataset is required")
			}
			if strings.TrimSpace(b.Field) == "" {
				v.issue(codeBindingInvalid, path+".field", "field is required")
			}
		case BindingFactor:
			if err := b.FactorRef.Validate(); err != nil {
				v.issue(codeBindingInvalid, path+".factor_ref", err.Error())
			}
		default:
			v.issue(codeBindingInvalid, path+".kind", "unknown binding kind")
		}
	}
	for _, b := range def.InputBindings {
		if catalog == nil {
			continue
		}
		if _, ok := catalog[b.BindingID]; !ok {
			v.issue(codeCatalogMissing, "input_bindings", "binding "+b.BindingID.String()+" is not in the catalog")
		}
	}
}

func (v *validator) validateTree(def Definition, catalog InputTypes) {
	if def.ConditionTree == nil {
		v.issue(codeChildRequired, "condition_tree", "condition tree is required")
		return
	}
	v.walk(def.ConditionTree, catalog, 0, "condition_tree")
}

func (v *validator) walk(node Condition, catalog InputTypes, depth int, prefix string) {
	if v.limits.MaxDepth > 0 && depth > v.limits.MaxDepth {
		if !v.seenIssue(codeDepthLimit, prefix) {
			v.issue(codeDepthLimit, prefix, "condition tree exceeds maximum depth")
		}
		return
	}
	v.nodeCount++
	if v.limits.MaxNodes > 0 && v.nodeCount > v.limits.MaxNodes {
		if !v.seenIssue(codeNodeLimit, prefix) {
			v.issue(codeNodeLimit, prefix, "condition tree exceeds maximum node count")
		}
		return
	}
	if node := nodeKind(node); node != "" {
		prefix = prefix + "." + node
	}
	v.registerNodeID(node, prefix)
	switch n := node.(type) {
	case All:
		if len(n.Children) < 1 {
			v.issue(codeChildrenEmpty, prefix, "all must have at least one child")
		}
		for i, c := range n.Children {
			v.walk(c, catalog, depth+1, prefix+".children["+itoa(i)+"]")
		}
	case Any:
		if len(n.Children) < 1 {
			v.issue(codeChildrenEmpty, prefix, "any must have at least one child")
		}
		for i, c := range n.Children {
			v.walk(c, catalog, depth+1, prefix+".children["+itoa(i)+"]")
		}
	case Not:
		if n.Child == nil {
			v.issue(codeChildRequired, prefix+".child", "not requires exactly one child")
		} else {
			v.walk(n.Child, catalog, depth+1, prefix+".child")
		}
	case Compare:
		v.checkInput(n.Input, prefix+".input")
		kind := v.bindingKind(n.Input.BindingID, catalog)
		if !validOperator(n.Operator) {
			v.issue(codeOperatorInvalid, prefix+".operator", "unknown operator")
		} else if kind != "" && !operatorAllowed(kind, n.Operator) {
			v.issue(codeOperatorMismatch, prefix+".operator", "operator not allowed for kind "+string(kind))
		}
		v.checkLiteral(n.Value, kind, prefix+".value")
	case Range:
		v.checkInput(n.Input, prefix+".input")
		kind := v.bindingKind(n.Input.BindingID, catalog)
		if kind != "" {
			if !rangeAllowed(kind) {
				v.issue(codeOperatorMismatch, prefix, "range not allowed for kind "+string(kind))
			} else {
				v.checkRangeBounds(n, kind, prefix)
			}
		}
	case Set:
		v.checkInput(n.Input, prefix+".input")
		kind := v.bindingKind(n.Input.BindingID, catalog)
		if kind != "" && !setAllowed(kind) {
			v.issue(codeOperatorMismatch, prefix, "set not allowed for kind "+string(kind))
		}
		if len(n.Values) < 1 {
			v.issue(codeSetInvalid, prefix, "set must have at least one element")
		}
		if v.limits.MaxSetLength > 0 && len(n.Values) > v.limits.MaxSetLength {
			v.issue(codeSetInvalid, prefix, "set has too many elements")
		}
		var elemKind domain.ValueKind
		for i := range n.Values {
			p := prefix + ".values[" + itoa(i) + "]"
			val := n.Values[i]
			v.checkLiteral(val, kind, p)
			if i == 0 {
				elemKind = val.Kind
			} else if elemKind != "" && val.Kind != elemKind {
				v.issue(codeSetInvalid, p, "set elements must share the same kind")
			}
		}
	case Missing:
		v.checkInput(n.Input, prefix+".input")
		if n.IsMissing == n.IsPresent {
			v.issue(codeMissingAmbiguous, prefix, "missing requires exactly one of is_missing/is_present")
		}
	}
}

func (v *validator) seenIssue(code, path string) bool {
	for _, it := range v.issues {
		if it.Code == code && it.Path == path {
			return true
		}
	}
	return false
}

func (v *validator) registerNodeID(node Condition, path string) {
	idStr := nodeIDOf(node)
	if idStr == "" {
		return
	}
	id, err := domain.ParseID(idStr)
	if err != nil {
		v.issue(codeNodeIDInvalid, path+".node_id", err.Error())
		return
	}
	if v.nodeIDs[id] {
		v.issue(codeNodeIDDuplicate, path+".node_id", "duplicate node_id")
	}
	v.nodeIDs[id] = true
}

func nodeIDOf(node Condition) string {
	switch n := node.(type) {
	case All:
		return n.NodeID.String()
	case Any:
		return n.NodeID.String()
	case Not:
		return n.NodeID.String()
	case Compare:
		return n.NodeID.String()
	case Range:
		return n.NodeID.String()
	case Set:
		return n.NodeID.String()
	case Missing:
		return n.NodeID.String()
	default:
		return ""
	}
}

func (v *validator) checkInput(in Input, path string) {
	if in.BindingID == "" {
		v.issue(codeInputUnknown, path, "input binding_id is required")
		return
	}
	if _, err := domain.ParseID(in.BindingID.String()); err != nil {
		v.issue(codeInputUnknown, path, err.Error())
		return
	}
	if !v.declared[in.BindingID] {
		v.issue(codeInputUnknown, path, "input references undeclared binding "+in.BindingID.String())
	}
}

func (v *validator) bindingKind(bindingID domain.ID, catalog InputTypes) domain.ValueKind {
	if catalog == nil {
		return ""
	}
	return catalog[bindingID]
}

func (v *validator) checkLiteral(val domain.Value, kind domain.ValueKind, path string) {
	if val.MissingReason != "" {
		v.issue(codeLiteralInvalid, path, "threshold literal cannot be a missing value")
		return
	}
	if val.Kind == "" {
		v.issue(codeLiteralInvalid, path, "threshold literal kind is required")
		return
	}
	if kind != "" && val.Kind != kind {
		v.issue(codeLiteralKindMatch, path+"|"+string(val.Kind)+"|"+string(kind), "threshold literal kind "+string(val.Kind)+" does not match binding kind "+string(kind))
		return
	}
	switch val.Kind {
	case domain.ValueDecimal, domain.ValueNumber:
		if _, err := domain.ParseDecimal(val.Encoded); err != nil {
			v.issue(codeLiteralInvalid, path, "invalid decimal literal")
		}
	case domain.ValueTimestamp:
		if _, err := parseTimestamp(val.Encoded); err != nil {
			v.issue(codeLiteralInvalid, path, "invalid timestamp literal")
		}
	case domain.ValueBoolean:
		if val.Encoded != "true" && val.Encoded != "false" {
			v.issue(codeLiteralInvalid, path, "invalid boolean literal")
		}
	case domain.ValueString:
		if strings.TrimSpace(val.Encoded) == "" {
			v.issue(codeLiteralInvalid, path, "string literal cannot be empty")
		}
	default:
		v.issue(codeLiteralInvalid, path, "unknown literal kind")
	}
}

func (v *validator) checkRangeBounds(n Range, kind domain.ValueKind, path string) {
	if n.Lower == nil && n.Upper == nil {
		v.issue(codeRangeBounds, path, "range requires at least one bound")
		return
	}
	if n.Lower != nil {
		v.checkLiteral(*n.Lower, kind, path+".lower")
	}
	if n.Upper != nil {
		v.checkLiteral(*n.Upper, kind, path+".upper")
	}
	if n.Lower != nil && n.Upper != nil {
		if c, err := compareLiterals(*n.Lower, *n.Upper); err == nil && c > 0 {
			v.issue(codeRangeBounds, path, "lower must not exceed upper")
		}
	}
}

func (v *validator) validateRanking(def Definition, catalog InputTypes) {
	switch def.Ranking.Mode {
	case RankingSort:
		if len(def.Ranking.Fields) < 1 {
			v.issue(codeRankingEmpty, "ranking", "sort mode requires at least one field")
		}
		if len(def.Ranking.Components) > 0 {
			v.issue(codeRankingMode, "ranking", "sort mode cannot declare score components")
		}
		for i, f := range def.Ranking.Fields {
			path := "ranking.fields[" + itoa(i) + "]"
			v.checkInput(f.Input, path+".input")
			if f.Direction != DirectionAsc && f.Direction != DirectionDesc {
				v.issue(codeDirectionInvalid, path+".direction", "unknown sort direction")
			} else {
				kind := v.bindingKind(f.Input.BindingID, catalog)
				if kind != "" && !sortableKind(kind) {
					v.issue(codeRankingKind, path+".input", "kind "+string(kind)+" cannot be sorted")
				}
			}
		}
	case RankingScore:
		if len(def.Ranking.Components) < 1 {
			v.issue(codeRankingEmpty, "ranking", "score mode requires at least one component")
		}
		if len(def.Ranking.Fields) > 0 {
			v.issue(codeRankingMode, "ranking", "score mode cannot declare sort fields")
		}
		sum := domain.Decimal("0")
		weightErr := false
		for i, c := range def.Ranking.Components {
			path := "ranking.components[" + itoa(i) + "]"
			v.checkInput(c.Input, path+".input")
			if c.Direction != ScoreLargerBetter && c.Direction != ScoreSmallerBetter {
				v.issue(codeDirectionInvalid, path+".direction", "unknown score direction")
			}
			valid := c.Weight.IsZero() || c.Weight.IsNegative() || c.Weight.IsPositive()
			if !valid {
				v.issue(codeWeightInvalid, path+".weight", "weight is not a valid decimal")
				weightErr = true
				continue
			}
			if c.Weight.IsNegative() {
				v.issue(codeWeightInvalid, path+".weight", "weight must be non-negative")
			}
			kind := v.bindingKind(c.Input.BindingID, catalog)
			if kind != "" && !scoreableKind(kind) {
				v.issue(codeRankingKind, path+".input", "kind "+string(kind)+" cannot be scored")
			}
			next, err := sum.Add(c.Weight)
			if err != nil {
				v.issue(codeWeightInvalid, path+".weight", err.Error())
				weightErr = true
				break
			}
			sum = next
		}
		if !weightErr {
			diff, err := sum.Sub(domain.Decimal("1"))
			if err != nil || !diff.IsZero() {
				v.issue(codeWeightSum, "ranking", "weights must sum to 1")
			}
		}
	default:
		v.issue(codeRankingMode, "ranking", "unknown ranking mode")
	}
}

func (v *validator) validateSelection(def Definition) {
	switch def.Selection.Mode {
	case SelectionAll:
	case SelectionTopN:
		if def.Selection.N < 1 {
			v.issue(codeSelectionInvalid, "selection", "top_n n must be at least 1")
		}
	default:
		v.issue(codeSelectionInvalid, "selection", "unknown selection mode")
	}
}

func (v *validator) validateDisplay(def Definition) {
	if v.limits.MaxDisplayColumns > 0 && len(def.DisplayColumns) > v.limits.MaxDisplayColumns {
		v.issue(codeDisplayInvalid, "display_columns", "too many display columns")
	}
	seen := map[domain.ID]bool{}
	for i, c := range def.DisplayColumns {
		path := "display_columns[" + itoa(i) + "]"
		id, err := domain.ParseID(c.String())
		if err != nil {
			v.issue(codeDisplayInvalid, path, err.Error())
		} else if seen[id] {
			v.issue(codeDisplayInvalid, path, "duplicate display column")
		} else {
			seen[id] = true
			if !v.declared[id] {
				v.issue(codeDisplayInvalid, path, "display column must reference a declared binding")
			}
		}
	}
}

func (v *validator) validateParent(def Definition) {
	if def.ParentID != "" {
		if _, err := domain.ParseID(def.ParentID.String()); err != nil {
			v.issue(codeParentInvalid, "parent_id", err.Error())
		}
	}
}

func validOperator(op Operator) bool {
	switch op {
	case OpEq, OpNe, OpGt, OpGte, OpLt, OpLte:
		return true
	default:
		return false
	}
}

func operatorAllowed(kind domain.ValueKind, op Operator) bool {
	switch kind {
	case domain.ValueDecimal, domain.ValueNumber, domain.ValueTimestamp:
		return validOperator(op)
	case domain.ValueString, domain.ValueBoolean:
		return op == OpEq || op == OpNe
	default:
		return false
	}
}

func rangeAllowed(kind domain.ValueKind) bool {
	switch kind {
	case domain.ValueDecimal, domain.ValueNumber, domain.ValueTimestamp:
		return true
	default:
		return false
	}
}

func setAllowed(kind domain.ValueKind) bool {
	switch kind {
	case domain.ValueDecimal, domain.ValueNumber, domain.ValueString:
		return true
	default:
		return false
	}
}

func sortableKind(kind domain.ValueKind) bool {
	switch kind {
	case domain.ValueDecimal, domain.ValueNumber, domain.ValueTimestamp, domain.ValueString:
		return true
	default:
		return false
	}
}

func scoreableKind(kind domain.ValueKind) bool {
	switch kind {
	case domain.ValueDecimal, domain.ValueNumber:
		return true
	default:
		return false
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	neg := i < 0
	u := uint64(i)
	if neg {
		u = uint64(-i)
	}
	var buf [20]byte
	pos := len(buf)
	for u > 0 {
		pos--
		buf[pos] = byte('0' + u%10)
		u /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}
