package domain

import "testing"

func TestNewMoney(t *testing.T) {
	m, err := NewMoney(Decimal("10.50"), "USD")
	if err != nil {
		t.Fatalf("NewMoney error: %v", err)
	}
	if m.Amount != Decimal("10.5") || m.Currency != "USD" {
		t.Fatalf("NewMoney = %+v", m)
	}
	for _, c := range []struct{ amount, currency string }{
		{"10.50", "usd"},
		{"10.50", "US"},
		{"10.50", "USDT"},
		{"10.50", ""},
		{"abc", "USD"},
	} {
		if _, err := NewMoney(Decimal(c.amount), c.currency); err == nil {
			t.Fatalf("NewMoney(%q,%q) should fail", c.amount, c.currency)
		}
	}
}

func TestMoneyAddSub(t *testing.T) {
	a, _ := NewMoney(Decimal("10.50"), "USD")
	b, _ := NewMoney(Decimal("0.50"), "USD")

	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	if sum.Amount != Decimal("11") || sum.Currency != "USD" {
		t.Fatalf("Add = %+v, want 11 USD", sum)
	}

	diff, err := b.Sub(a)
	if err != nil {
		t.Fatalf("Sub error: %v", err)
	}
	if diff.Amount != Decimal("-10") || diff.Currency != "USD" {
		t.Fatalf("Sub = %+v, want -10 USD", diff)
	}

	eur, _ := NewMoney(Decimal("1"), "EUR")
	if _, err := a.Add(eur); ErrorCode(err) != CodeValidationCurrencyMismatch {
		t.Fatalf("Add cross-currency code = %v, want %s", err, CodeValidationCurrencyMismatch)
	}
	if _, err := a.Sub(eur); ErrorCode(err) != CodeValidationCurrencyMismatch {
		t.Fatalf("Sub cross-currency code = %v, want %s", err, CodeValidationCurrencyMismatch)
	}
}
