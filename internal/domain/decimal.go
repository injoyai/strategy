package domain

import (
	"bytes"
	"encoding/json"
	"regexp"
	"unicode"

	"github.com/shopspring/decimal"
)

// MaxDecimalLength bounds canonical decimal strings on the wire. It caps
// magnitude and precision together, which keeps every arithmetic result
// bounded as well (the overflow policy).
const MaxDecimalLength = 40

// maxRoundingPlaces bounds the Round argument so a hostile caller cannot
// trigger a rescale with billions of digits.
const maxRoundingPlaces = MaxDecimalLength

// decimalPattern accepts plain notation only: optional sign, digits, at most
// one decimal point, at least one digit. Exponents, NaN/Inf tokens and other
// shapes are rejected so JSON and HTTP inputs cannot smuggle unbounded
// magnitudes (1e999) or locale-dependent forms.
var decimalPattern = regexp.MustCompile(`^[+-]?(\d+(\.\d+)?|\.\d+)$`)

// Decimal is an exact, arbitrary-precision number in canonical plain
// notation: no exponent, no trailing fractional zeros, no positive sign and
// no negative zero (e.g. "1.5", "-0.25", "0"). Construct it through
// ParseDecimal or arithmetic helpers and treat it as immutable.
type Decimal string

// ParseDecimal validates raw wire input and returns its canonical form.
func ParseDecimal(s string) (Decimal, error) {
	switch {
	case s == "":
		return "", NewError(CodeValidationDecimal, "decimal: empty input")
	case len(s) > MaxDecimalLength:
		return "", NewError(CodeValidationDecimal, "decimal: input longer than %d characters", MaxDecimalLength)
	}
	for _, r := range s {
		if unicode.IsSpace(r) {
			return "", NewError(CodeValidationDecimal, "decimal: whitespace is not allowed")
		}
	}
	if !decimalPattern.MatchString(s) {
		return "", NewError(CodeValidationDecimal, "decimal: malformed input %q", truncate(s))
	}
	v, err := decimal.NewFromString(s)
	if err != nil {
		return "", Wrap(err, CodeValidationDecimal, "decimal: malformed input %q", truncate(s))
	}
	// String() trims trailing fractional zeros and never emits an exponent:
	// this is the canonical form.
	return Decimal(v.String()), nil
}

// IsZero reports whether the value equals zero. An invalid zero value
// (empty Decimal) reports false.
func (d Decimal) IsZero() bool {
	v, err := d.value()
	if err != nil {
		return false
	}
	return v.IsZero()
}

// IsNegative reports whether the value is strictly less than zero. An
// invalid Decimal reports false so quality checks fail open (no false
// positive on a malformed stored value).
func (d Decimal) IsNegative() bool {
	v, err := d.value()
	if err != nil {
		return false
	}
	return v.IsNegative()
}

// IsPositive reports whether the value is strictly greater than zero.
func (d Decimal) IsPositive() bool {
	v, err := d.value()
	if err != nil {
		return false
	}
	return v.IsPositive()
}

// value parses a stored decimal. Values built by this package always parse;
// the error path only guards zero values or hand-assembled strings.
func (d Decimal) value() (decimal.Decimal, error) {
	if d == "" {
		return decimal.Decimal{}, NewError(CodeValidationDecimal, "decimal: empty value")
	}
	v, err := decimal.NewFromString(string(d))
	if err != nil {
		return decimal.Decimal{}, Wrap(err, CodeValidationDecimal, "decimal: malformed value %q", truncate(string(d)))
	}
	return v, nil
}

// canonical converts an arithmetic result back into a bounded Decimal.
func canonical(v decimal.Decimal) (Decimal, error) {
	s := v.String()
	if len(s) > MaxDecimalLength {
		return "", NewError(CodeValidationDecimal, "decimal: result exceeds %d characters (overflow)", MaxDecimalLength)
	}
	return Decimal(s), nil
}

// Add returns d + o. Both operands must be valid; the result is bounded by
// MaxDecimalLength.
func (d Decimal) Add(o Decimal) (Decimal, error) {
	a, err := d.value()
	if err != nil {
		return "", err
	}
	b, err := o.value()
	if err != nil {
		return "", err
	}
	return canonical(a.Add(b))
}

// Sub returns d - o.
func (d Decimal) Sub(o Decimal) (Decimal, error) {
	a, err := d.value()
	if err != nil {
		return "", err
	}
	b, err := o.value()
	if err != nil {
		return "", err
	}
	return canonical(a.Sub(b))
}

// Mul returns d * o.
func (d Decimal) Mul(o Decimal) (Decimal, error) {
	a, err := d.value()
	if err != nil {
		return "", err
	}
	b, err := o.value()
	if err != nil {
		return "", err
	}
	return canonical(a.Mul(b))
}

// Div returns d / o, rounded to the division precision (16 fractional
// digits) shopspring applies by default — well inside MaxDecimalLength.
// A zero divisor is rejected explicitly: shopspring's Div panics on zero,
// and callers (e.g. ratio factors) must map the failure onto a missing
// reason such as invalid_denominator instead of an arithmetic panic.
func (d Decimal) Div(o Decimal) (Decimal, error) {
	a, err := d.value()
	if err != nil {
		return "", err
	}
	b, err := o.value()
	if err != nil {
		return "", err
	}
	if b.IsZero() {
		return "", NewError(CodeValidationInvalid, "decimal: division by zero")
	}
	return canonical(a.Div(b))
}

// RoundingMode selects an explicit rounding strategy. No default is fixed:
// call sites must name the mode so fee, mark and metric policies stay
// explicit decisions.
type RoundingMode uint8

const (
	RoundHalfAwayFromZero RoundingMode = iota //  2.5 ->  3, -2.5 -> -3
	RoundHalfEven                             //  2.5 ->  2,  3.5 ->  4 (banker's)
	RoundDown                                 // toward zero:  1.9 ->  1, -1.9 -> -1
	RoundUp                                   // away from zero: 1.1 -> 2, -1.1 -> -2
	RoundCeil                                 // toward +inf: -1.1 -> -1
	RoundFloor                                // toward -inf:  1.9 ->  1
)

// Round rounds d to places fractional digits (negative places round whole
// tens, hundreds, ...) using the given mode and returns the canonical form.
func (d Decimal) Round(places int32, mode RoundingMode) (Decimal, error) {
	if places > maxRoundingPlaces || places < -maxRoundingPlaces {
		return "", NewError(CodeValidationDecimal, "decimal: rounding places %d out of range", places)
	}
	v, err := d.value()
	if err != nil {
		return "", err
	}
	switch mode {
	case RoundHalfAwayFromZero:
		v = v.Round(places)
	case RoundHalfEven:
		v = v.RoundBank(places)
	case RoundDown:
		v = v.RoundDown(places)
	case RoundUp:
		v = v.RoundUp(places)
	case RoundCeil:
		v = v.RoundCeil(places)
	case RoundFloor:
		v = v.RoundFloor(places)
	default:
		return "", NewError(CodeValidationInvalid, "decimal: unknown rounding mode %d", mode)
	}
	return canonical(v)
}

// UnmarshalJSON accepts only JSON strings. Bare numbers (including 1e999)
// and null are rejected at the token level, before any float parsing could
// lose precision or overflow.
func (d *Decimal) UnmarshalJSON(data []byte) error {
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return NewError(CodeValidationDecimal, "decimal: JSON string required, got %s", jsonTokenKind(data))
	}
	parsed, err := ParseDecimal(s)
	if err != nil {
		return err
	}
	*d = parsed
	return nil
}

// jsonTokenKind names the JSON token type of raw data for error messages.
func jsonTokenKind(data []byte) string {
	trimmed := bytes.TrimLeft(data, " \t\r\n")
	switch {
	case len(trimmed) == 0:
		return "empty input"
	case bytes.Equal(trimmed, []byte("null")):
		return "null"
	case trimmed[0] == '"':
		return "string"
	case trimmed[0] == '{':
		return "object"
	case trimmed[0] == '[':
		return "array"
	case trimmed[0] == 't' || trimmed[0] == 'f':
		return "boolean"
	default:
		return "number"
	}
}
