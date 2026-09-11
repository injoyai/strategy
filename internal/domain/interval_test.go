package domain

import (
	"testing"
	"time"
)

// ts builds a UTC instant n hours after the Unix epoch.
func ts(hours int64) time.Time { return time.Unix(hours*3600, 0).UTC() }

func TestNewIntervalUTCNormalization(t *testing.T) {
	loc, err := LoadBusinessTimezone("Asia/Shanghai")
	if err != nil {
		t.Fatalf("embedded tzdata missing or zone unknown: %v", err)
	}
	from := time.Date(2024, 1, 2, 0, 0, 0, 0, loc)
	to := time.Date(2024, 1, 3, 0, 0, 0, 0, loc)
	iv, err := NewInterval(from, to)
	if err != nil {
		t.Fatalf("NewInterval error: %v", err)
	}
	wantFrom := time.Date(2024, 1, 1, 16, 0, 0, 0, time.UTC)
	if !iv.From.Equal(wantFrom) || iv.From.Location() != time.UTC {
		t.Fatalf("From = %s %v, want %s UTC", iv.From, iv.From.Location(), wantFrom)
	}
	if !iv.To.Equal(to.UTC()) || iv.To.Location() != time.UTC {
		t.Fatalf("To = %s %v, want %s UTC", iv.To, iv.To.Location(), to.UTC())
	}
}

func TestIntervalContains(t *testing.T) {
	iv, _ := NewInterval(ts(1), ts(3))
	cases := []struct {
		at   time.Time
		want bool
	}{
		{ts(1), true},  // left bound included
		{ts(2), true},  // inside
		{ts(3), false}, // right bound excluded
		{ts(0), false}, // before
		{ts(4), false}, // after
	}
	for _, c := range cases {
		if got := iv.Contains(c.at); got != c.want {
			t.Fatalf("Contains(%s) = %v, want %v", c.at, got, c.want)
		}
	}
}

func TestIntervalOverlaps(t *testing.T) {
	iv, _ := NewInterval(ts(1), ts(3))
	cases := []struct {
		from, to time.Time
		want     bool
	}{
		{ts(3), ts(5), false}, // adjacent, no overlap
		{ts(2), ts(4), true},  // overlaps right edge
		{ts(0), ts(1), false}, // adjacent on the left
		{ts(1), ts(2), true},  // inside
		{ts(0), ts(10), true}, // contains iv
		{ts(5), ts(6), false}, // disjoint
	}
	for _, c := range cases {
		o := Interval{From: c.from, To: c.to}
		if got := iv.Overlaps(o); got != c.want {
			t.Fatalf("Overlaps([%s,%s)) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestNewIntervalRejects(t *testing.T) {
	for _, c := range []struct{ from, to time.Time }{
		{ts(3), ts(3)}, // empty range
		{ts(3), ts(1)}, // inverted
	} {
		if _, err := NewInterval(c.from, c.to); err == nil {
			t.Fatalf("NewInterval(%s,%s) should fail", c.from, c.to)
		} else if code := ErrorCode(err); code != CodeValidationInterval {
			t.Fatalf("NewInterval code = %s, want %s", code, CodeValidationInterval)
		}
	}
}

func TestLoadBusinessTimezoneRejects(t *testing.T) {
	for _, name := range []string{"", "Local", "local", "Mars/Phobos"} {
		if _, err := LoadBusinessTimezone(name); err == nil {
			t.Fatalf("LoadBusinessTimezone(%q) should fail", name)
		} else if code := ErrorCode(err); code != CodeValidationInvalid {
			t.Fatalf("LoadBusinessTimezone(%q) code = %s, want %s", name, code, CodeValidationInvalid)
		}
	}
}
