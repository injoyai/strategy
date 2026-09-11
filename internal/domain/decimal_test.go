package domain

import (
	"encoding/json"
	"math/rand"
	"strings"
	"testing"
)

func TestParseDecimalCanonical(t *testing.T) {
	cases := []struct{ in, want string }{
		{"1", "1"},
		{"+1.50", "1.5"},
		{"-0.0", "0"},
		{"0.100", "0.1"},
		{"00012.3400", "12.34"},
		{".5", "0.5"},
	}
	for _, c := range cases {
		got, err := ParseDecimal(c.in)
		if err != nil {
			t.Fatalf("ParseDecimal(%q) error: %v", c.in, err)
		}
		if got != Decimal(c.want) {
			t.Fatalf("ParseDecimal(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("9", MaxDecimalLength)
	if _, err := ParseDecimal(long); err != nil {
		t.Fatalf("ParseDecimal(%d chars) error: %v", MaxDecimalLength, err)
	}
	if _, err := ParseDecimal(long + "0"); err == nil {
		t.Fatalf("ParseDecimal(%d chars) should fail", MaxDecimalLength+1)
	}
}

func TestParseDecimalRejects(t *testing.T) {
	cases := []string{
		"", " 1", "1 ", "1e3", "1E3", "1e999", "NaN", "Infinity", "-Inf",
		"1.2.3", "--1", ".", "abc", "1,5", "1_000", "0x10", "1.",
	}
	for _, in := range cases {
		got, err := ParseDecimal(in)
		if err == nil {
			t.Fatalf("ParseDecimal(%q) = %q, want error", in, got)
		}
		if code := ErrorCode(err); code != CodeValidationDecimal {
			t.Fatalf("ParseDecimal(%q) code = %s, want %s", in, code, CodeValidationDecimal)
		}
	}
}

func TestDecimalArithmetic(t *testing.T) {
	a, _ := ParseDecimal("0.1")
	b, _ := ParseDecimal("0.2")
	sum, err := a.Add(b)
	if err != nil {
		t.Fatalf("0.1+0.2 error: %v", err)
	}
	if sum != Decimal("0.3") {
		t.Fatalf("0.1+0.2 = %q, want 0.3", sum)
	}

	zero, _ := ParseDecimal("0")
	if diff, err := a.Sub(a); err != nil || diff != Decimal("0") {
		t.Fatalf("a-a = %q err %v, want 0", diff, err)
	}
	if s, err := a.Add(zero); err != nil || s != a {
		t.Fatalf("a+0 = %q err %v, want a", s, err)
	}

	forty := Decimal(strings.Repeat("9", MaxDecimalLength))
	one, _ := ParseDecimal("1")
	if got, err := forty.Add(one); err == nil {
		t.Fatalf("40 nines + 1 = %q, want overflow error", got)
	} else if code := ErrorCode(err); code != CodeValidationDecimal {
		t.Fatalf("overflow code = %s, want %s", code, CodeValidationDecimal)
	}

	twenty := Decimal(strings.Repeat("9", 20))
	product, err := twenty.Mul(twenty)
	if err != nil {
		t.Fatalf("20 nines squared error: %v", err)
	}
	if len(product) != MaxDecimalLength {
		t.Fatalf("20 nines squared = %d chars, want %d", len(product), MaxDecimalLength)
	}
}

func TestDecimalRound(t *testing.T) {
	cases := []struct {
		in     string
		places int32
		mode   RoundingMode
		want   string
	}{
		{"1.005", 2, RoundHalfAwayFromZero, "1.01"},
		{"1.005", 2, RoundHalfEven, "1"},
		{"1.005", 2, RoundDown, "1"},
		{"1.005", 2, RoundUp, "1.01"},
		{"1.005", 2, RoundCeil, "1.01"},
		{"1.005", 2, RoundFloor, "1"},
		{"-1.005", 2, RoundHalfAwayFromZero, "-1.01"},
		{"-1.005", 2, RoundHalfEven, "-1"},
		{"-1.005", 2, RoundDown, "-1"},
		{"-1.005", 2, RoundUp, "-1.01"},
		{"-1.005", 2, RoundCeil, "-1"},
		{"-1.005", 2, RoundFloor, "-1.01"},
		{"2.5", 0, RoundHalfAwayFromZero, "3"},
		{"2.5", 0, RoundHalfEven, "2"},
		{"-2.5", 0, RoundHalfAwayFromZero, "-3"},
		{"-2.5", 0, RoundHalfEven, "-2"},
	}
	for _, c := range cases {
		d, _ := ParseDecimal(c.in)
		got, err := d.Round(c.places, c.mode)
		if err != nil {
			t.Fatalf("Round(%q,%d,%d) error: %v", c.in, c.places, c.mode, err)
		}
		if got != Decimal(c.want) {
			t.Fatalf("Round(%q,%d,%d) = %q, want %q", c.in, c.places, c.mode, got, c.want)
		}
	}

	d, _ := ParseDecimal("1.5")
	if _, err := d.Round(2, RoundingMode(99)); err == nil {
		t.Fatal("unknown rounding mode should fail")
	} else if code := ErrorCode(err); code != CodeValidationInvalid {
		t.Fatalf("unknown mode code = %s, want %s", code, CodeValidationInvalid)
	}
	if _, err := d.Round(maxRoundingPlaces+1, RoundHalfEven); err == nil {
		t.Fatal("places beyond range should fail")
	} else if code := ErrorCode(err); code != CodeValidationDecimal {
		t.Fatalf("places code = %s, want %s", code, CodeValidationDecimal)
	}
}

func TestDecimalUnmarshalJSON(t *testing.T) {
	var d Decimal
	if err := json.Unmarshal([]byte(`"1.50"`), &d); err != nil {
		t.Fatalf("unmarshal string error: %v", err)
	}
	if d != Decimal("1.5") {
		t.Fatalf("unmarshal = %q, want 1.5", d)
	}
	for _, in := range []string{"1.5", "1e999", "null", "true", `"1e3"`} {
		var d Decimal
		if err := json.Unmarshal([]byte(in), &d); err == nil {
			t.Fatalf("json %s should fail", in)
		} else if code := ErrorCode(err); code != CodeValidationDecimal {
			t.Fatalf("json %s code = %s, want %s", in, code, CodeValidationDecimal)
		}
	}
}

func TestDecimalProperties(t *testing.T) {
	rng := rand.New(rand.NewSource(1)) //nolint:gosec // deterministic seeded generator for property tests
	zero, _ := ParseDecimal("0")
	for i := 0; i < 200; i++ {
		a, err := ParseDecimal(randDecimalString(rng))
		if err != nil {
			t.Fatalf("seed %d: parse %q error: %v", i, a, err)
		}
		again, err := ParseDecimal(string(a))
		if err != nil || again != a {
			t.Fatalf("seed %d: parse not idempotent: %q -> %q (err %v)", i, a, again, err)
		}
		if s, err := a.Add(zero); err != nil || s != a {
			t.Fatalf("seed %d: a+0 = %q (err %v), want %q", i, s, err, a)
		}
		if diff, err := a.Sub(a); err != nil || diff != Decimal("0") {
			t.Fatalf("seed %d: a-a = %q (err %v), want 0", i, diff, err)
		}
		b, err := ParseDecimal(randDecimalString(rng))
		if err != nil {
			t.Fatalf("seed %d: parse %q error: %v", i, b, err)
		}
		sum, err := a.Add(b)
		if err != nil {
			t.Fatalf("seed %d: a+b overflow: %v", i, err)
		}
		back, err := sum.Sub(b)
		if err != nil || back != a {
			t.Fatalf("seed %d: (a+b)-b = %q (err %v), want %q", i, back, err, a)
		}
	}
}

// randDecimalString emits a random plain-notation decimal: optional sign,
// 1-10 integer digits and 0-9 fractional digits.
func randDecimalString(rng *rand.Rand) string {
	var b strings.Builder
	if rng.Intn(4) == 0 {
		b.WriteByte('-')
	}
	for n := rng.Intn(10) + 1; n > 0; n-- {
		b.WriteByte(byte('0' + rng.Intn(10)))
	}
	if frac := rng.Intn(10); frac > 0 {
		b.WriteByte('.')
		for ; frac > 0; frac-- {
			b.WriteByte(byte('0' + rng.Intn(10)))
		}
	}
	return b.String()
}
