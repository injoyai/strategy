package factor

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/injoyai/strategy/internal/domain"
)

// ParamType enumerates the scalar parameter types a spec may declare.
type ParamType string

const (
	ParamInteger ParamType = "integer"
	ParamNumber  ParamType = "number"
	ParamString  ParamType = "string"
	ParamBoolean ParamType = "boolean"
)

// Param is one declared parameter. Optional params must carry a default —
// canonicalization fills it in, so a request only carries overrides.
type Param struct {
	Name     string
	Type     ParamType
	Required bool
	Default  any
	Min      *float64 // numeric types only
	Max      *float64 // numeric types only
	Enum     []string // string type only
}

// validate checks the declared schema itself, including that the default
// satisfies every constraint — a default violating the spec's own bounds
// is a registration bug, not a runtime surprise.
func (p Param) validate() error {
	if p.Name == "" {
		return domain.NewError(codeSpecInvalid, "factor: parameter name is required")
	}
	switch p.Type {
	case ParamInteger, ParamNumber, ParamString, ParamBoolean:
	default:
		return domain.NewError(codeSpecInvalid, "factor: parameter %q has unknown type %q", p.Name, p.Type)
	}
	numeric := p.Type == ParamInteger || p.Type == ParamNumber
	if (p.Min != nil || p.Max != nil) && !numeric {
		return domain.NewError(codeSpecInvalid, "factor: parameter %q: min/max only apply to numeric parameters", p.Name)
	}
	if p.Min != nil && p.Max != nil && *p.Min > *p.Max {
		return domain.NewError(codeSpecInvalid, "factor: parameter %q: min %v exceeds max %v", p.Name, *p.Min, *p.Max)
	}
	if len(p.Enum) > 0 && p.Type != ParamString {
		return domain.NewError(codeSpecInvalid, "factor: parameter %q: enum only applies to string parameters", p.Name)
	}
	if p.Required && p.Default != nil {
		return domain.NewError(codeSpecInvalid, "factor: parameter %q is required and must not carry a default", p.Name)
	}
	if !p.Required && p.Default == nil {
		return domain.NewError(codeSpecInvalid, "factor: parameter %q is optional and must declare a default", p.Name)
	}
	if p.Default == nil {
		return nil
	}
	value, ok := coerceParam(p, p.Default)
	if !ok {
		return domain.NewError(codeSpecInvalid, "factor: parameter %q default does not match type %s", p.Name, p.Type)
	}
	if p.Min != nil && numericValue(value) < *p.Min {
		return domain.NewError(codeSpecInvalid, "factor: parameter %q default %v is below minimum %v", p.Name, numericValue(value), *p.Min)
	}
	if p.Max != nil && numericValue(value) > *p.Max {
		return domain.NewError(codeSpecInvalid, "factor: parameter %q default %v is above maximum %v", p.Name, numericValue(value), *p.Max)
	}
	if len(p.Enum) > 0 {
		s, _ := value.(string)
		if !containsString(p.Enum, s) {
			return domain.NewError(codeSpecInvalid, "factor: parameter %q default %q is not one of %s", p.Name, s, strings.Join(p.Enum, ", "))
		}
	}
	return nil
}

// canonicalParams merges supplied values over declared defaults into the
// canonical typed map: integer -> int64, number -> float64, string ->
// string, boolean -> bool. JSON-decoded numbers arrive as float64, so the
// integer path accepts integral float64. Every violation is reported —
// preflight surfaces all problems in one round, not just the first.
func canonicalParams(spec *Spec, supplied map[string]any) (map[string]any, []Problem) {
	declared := make(map[string]bool, len(spec.Params))
	for _, p := range spec.Params {
		declared[p.Name] = true
	}
	keys := make([]string, 0, len(supplied))
	for k := range supplied {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var problems []Problem
	for _, k := range keys {
		if !declared[k] {
			problems = append(problems, Problem{Code: ProblemParamInvalid, Message: fmt.Sprintf("unknown parameter %q", k)})
		}
	}

	out := make(map[string]any, len(spec.Params))
	for _, p := range spec.Params {
		raw, supplied := supplied[p.Name]
		if !supplied {
			if p.Default == nil {
				problems = append(problems, Problem{Code: ProblemParamInvalid, Message: fmt.Sprintf("parameter %q is required", p.Name)})
				continue
			}
			raw = p.Default
		}
		value, ok := coerceParam(p, raw)
		if !ok {
			problems = append(problems, Problem{Code: ProblemParamInvalid, Message: fmt.Sprintf("parameter %q must be a %s", p.Name, p.Type)})
			continue
		}
		if p.Min != nil && numericValue(value) < *p.Min {
			problems = append(problems, Problem{Code: ProblemParamInvalid, Message: fmt.Sprintf("parameter %q value %v is below minimum %v", p.Name, numericValue(value), *p.Min)})
			continue
		}
		if p.Max != nil && numericValue(value) > *p.Max {
			problems = append(problems, Problem{Code: ProblemParamInvalid, Message: fmt.Sprintf("parameter %q value %v is above maximum %v", p.Name, numericValue(value), *p.Max)})
			continue
		}
		if len(p.Enum) > 0 {
			s, _ := value.(string)
			if !containsString(p.Enum, s) {
				problems = append(problems, Problem{Code: ProblemParamInvalid, Message: fmt.Sprintf("parameter %q value %q is not one of %s", p.Name, s, strings.Join(p.Enum, ", "))})
				continue
			}
		}
		out[p.Name] = value
	}
	return out, problems
}

// coerceParam converts one raw value (Go-native or JSON-decoded) into the
// canonical Go type for the declared parameter type.
func coerceParam(p Param, raw any) (any, bool) {
	switch p.Type {
	case ParamInteger:
		switch v := raw.(type) {
		case float64:
			if v != math.Trunc(v) {
				return nil, false
			}
			return int64(v), true
		case int:
			return int64(v), true
		case int64:
			return v, true
		}
		return nil, false
	case ParamNumber:
		switch v := raw.(type) {
		case float64:
			return v, true
		case int:
			return float64(v), true
		case int64:
			return float64(v), true
		}
		return nil, false
	case ParamString:
		if s, ok := raw.(string); ok {
			return s, true
		}
		return nil, false
	case ParamBoolean:
		if b, ok := raw.(bool); ok {
			return b, true
		}
		return nil, false
	}
	return nil, false
}

// numericValue widens a canonical param value for range checks.
func numericValue(v any) float64 {
	switch t := v.(type) {
	case int64:
		return float64(t)
	case float64:
		return t
	}
	return 0
}

func containsString(values []string, want string) bool {
	for _, v := range values {
		if v == want {
			return true
		}
	}
	return false
}
