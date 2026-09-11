package domain

import (
	"regexp"
)

// unitPattern keeps units lower-case, URL-safe and bounded (e.g. "share",
// "kwh_co2e"). Units are semantic keys for quantity comparison.
var unitPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)

// Unit is a lowercase unit of measure.
type Unit string

// ParseUnit validates the wire form of a unit.
func ParseUnit(s string) (Unit, error) {
	if !unitPattern.MatchString(s) {
		return "", NewError(CodeValidationInvalid, "invalid unit %q", truncate(s))
	}
	return Unit(s), nil
}

// Quantity pairs an exact decimal amount with a unit. Arithmetic refuses to
// mix units so cross-unit mistakes surface at the boundary instead of
// producing silently wrong totals.
type Quantity struct {
	Amount Decimal
	Unit   Unit
}

// NewQuantity validates the amount and the unit.
func NewQuantity(amount Decimal, unit Unit) (Quantity, error) {
	if _, err := ParseUnit(string(unit)); err != nil {
		return Quantity{}, err
	}
	parsed, err := ParseDecimal(string(amount))
	if err != nil {
		return Quantity{}, err
	}
	return Quantity{Amount: parsed, Unit: unit}, nil
}

// Add returns q + o in q's unit.
func (q Quantity) Add(o Quantity) (Quantity, error) {
	if q.Unit != o.Unit {
		return Quantity{}, NewError(CodeValidationUnitMismatch, "quantity: cannot add %s and %s", q.Unit, o.Unit)
	}
	amount, err := q.Amount.Add(o.Amount)
	if err != nil {
		return Quantity{}, err
	}
	return Quantity{Amount: amount, Unit: q.Unit}, nil
}

// Sub returns q - o in q's unit.
func (q Quantity) Sub(o Quantity) (Quantity, error) {
	if q.Unit != o.Unit {
		return Quantity{}, NewError(CodeValidationUnitMismatch, "quantity: cannot subtract %s from %s", o.Unit, q.Unit)
	}
	amount, err := q.Amount.Sub(o.Amount)
	if err != nil {
		return Quantity{}, err
	}
	return Quantity{Amount: amount, Unit: q.Unit}, nil
}

// Price is money per unit of measure (e.g. 12.34 USD per share). Multiplying
// a price by a quantity of the same unit yields money in the price currency.
type Price struct {
	Money Money
	Unit  Unit
}

// NewPrice validates the amount, currency and unit.
func NewPrice(amount Decimal, currency string, unit Unit) (Price, error) {
	money, err := NewMoney(amount, currency)
	if err != nil {
		return Price{}, err
	}
	if _, err := ParseUnit(string(unit)); err != nil {
		return Price{}, err
	}
	return Price{Money: money, Unit: unit}, nil
}

// Mul scales the price by a quantity of the same unit: (amount/unit) * qty.
// The result keeps the price currency.
func (p Price) Mul(q Quantity) (Money, error) {
	if p.Unit != q.Unit {
		return Money{}, NewError(CodeValidationUnitMismatch, "price: unit %s does not match quantity unit %s", p.Unit, q.Unit)
	}
	amount, err := p.Money.Amount.Mul(q.Amount)
	if err != nil {
		return Money{}, err
	}
	return Money{Amount: amount, Currency: p.Money.Currency}, nil
}
