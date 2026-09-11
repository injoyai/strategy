package domain

import (
	"strings"
	"testing"
)

func TestParseUnit(t *testing.T) {
	for _, s := range []string{"share", "kwh_co2e", "a"} {
		if _, err := ParseUnit(s); err != nil {
			t.Fatalf("ParseUnit(%q) error: %v", s, err)
		}
	}
	for _, s := range []string{"Share", "", "1share", "share-share", "a b", strings.Repeat("a", 33)} {
		if _, err := ParseUnit(s); err == nil {
			t.Fatalf("ParseUnit(%q) should fail", s)
		} else if code := ErrorCode(err); code != CodeValidationInvalid {
			t.Fatalf("ParseUnit(%q) code = %s, want %s", s, code, CodeValidationInvalid)
		}
	}
}

func TestQuantityArithmetic(t *testing.T) {
	a, err := NewQuantity(Decimal("1.5"), "share")
	if err != nil {
		t.Fatalf("NewQuantity error: %v", err)
	}
	b, _ := NewQuantity(Decimal("2.5"), "share")
	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("Add error: %v", err)
	}
	if sum.Amount != Decimal("4") || sum.Unit != "share" {
		t.Fatalf("Add = %+v, want 4 share", sum)
	}

	other, _ := NewQuantity(Decimal("1"), "kwh_co2e")
	if _, err := a.Add(other); ErrorCode(err) != CodeValidationUnitMismatch {
		t.Fatalf("Add cross-unit code = %v, want %s", err, CodeValidationUnitMismatch)
	}
	if _, err := a.Sub(other); ErrorCode(err) != CodeValidationUnitMismatch {
		t.Fatalf("Sub cross-unit code = %v, want %s", err, CodeValidationUnitMismatch)
	}
}

func TestPriceMul(t *testing.T) {
	p, err := NewPrice(Decimal("12.34"), "USD", "share")
	if err != nil {
		t.Fatalf("NewPrice error: %v", err)
	}
	q, _ := NewQuantity(Decimal("3"), "share")
	money, err := p.Mul(q)
	if err != nil {
		t.Fatalf("Mul error: %v", err)
	}
	if money.Amount != Decimal("37.02") || money.Currency != "USD" {
		t.Fatalf("Mul = %+v, want 37.02 USD", money)
	}

	q2, _ := NewQuantity(Decimal("3"), "kwh_co2e")
	if _, err := p.Mul(q2); ErrorCode(err) != CodeValidationUnitMismatch {
		t.Fatalf("Mul cross-unit code = %v, want %s", err, CodeValidationUnitMismatch)
	}
}
