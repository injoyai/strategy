package domain

import (
	"regexp"
)

// currencyPattern enforces the ISO 4217 alphabetic currency code shape
// (three upper-case letters, e.g. "USD", "CNY").
var currencyPattern = regexp.MustCompile(`^[A-Z]{3}$`)

// Money pairs an exact decimal amount with a currency. Both fields are
// immutable once built; arithmetic refuses to mix currencies so a silently
// wrong unit can never flow downstream.
type Money struct {
	Amount   Decimal
	Currency string
}

// NewMoney validates the amount and the ISO 4217 currency code.
func NewMoney(amount Decimal, currency string) (Money, error) {
	if !currencyPattern.MatchString(currency) {
		return Money{}, NewError(CodeValidationInvalid, "money: invalid currency %q", truncate(currency))
	}
	parsed, err := ParseDecimal(string(amount))
	if err != nil {
		return Money{}, err
	}
	return Money{Amount: parsed, Currency: currency}, nil
}

// Add returns m + o in m's currency.
func (m Money) Add(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, NewError(CodeValidationCurrencyMismatch, "money: cannot add %s and %s", m.Currency, o.Currency)
	}
	amount, err := m.Amount.Add(o.Amount)
	if err != nil {
		return Money{}, err
	}
	return Money{Amount: amount, Currency: m.Currency}, nil
}

// Sub returns m - o in m's currency.
func (m Money) Sub(o Money) (Money, error) {
	if m.Currency != o.Currency {
		return Money{}, NewError(CodeValidationCurrencyMismatch, "money: cannot subtract %s from %s", o.Currency, m.Currency)
	}
	amount, err := m.Amount.Sub(o.Amount)
	if err != nil {
		return Money{}, err
	}
	return Money{Amount: amount, Currency: m.Currency}, nil
}
