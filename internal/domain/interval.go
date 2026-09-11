package domain

import (
	"strings"
	"time"

	// Embedded timezone database: Windows hosts often lack a system
	// zoneinfo, and embedding keeps business timezones consistent across
	// machines. Importing here means every binary that links domain gets it.
	_ "time/tzdata"
)

// Interval is a half-open time range [From, To) with From < To. Both bounds
// are normalized to UTC on construction; internal comparisons never deal with
// local time.
type Interval struct {
	From time.Time
	To   time.Time
}

// NewInterval builds a half-open [from, to) interval, normalizing both ends
// to UTC. A zero-length or inverted range is rejected.
func NewInterval(from, to time.Time) (Interval, error) {
	f, t := from.UTC(), to.UTC()
	if !f.Before(t) {
		return Interval{}, NewError(CodeValidationInterval, "interval: from must be before to (%s >= %s)", f.Format(time.RFC3339Nano), t.Format(time.RFC3339Nano))
	}
	return Interval{From: f, To: t}, nil
}

// Contains reports whether ts falls inside [From, To): the left bound is
// included, the right bound is excluded.
func (iv Interval) Contains(ts time.Time) bool {
	u := ts.UTC()
	return !u.Before(iv.From) && u.Before(iv.To)
}

// Overlaps reports whether iv and o share any instant. Adjacent intervals
// ([1,3) and [3,5)) do not overlap.
func (iv Interval) Overlaps(o Interval) bool {
	return iv.From.Before(o.To.UTC()) && o.From.UTC().Before(iv.To)
}

// LoadBusinessTimezone resolves an IANA zone name for business-day logic.
// "local" is rejected by name: business time must never depend on the host's
// local zone.
func LoadBusinessTimezone(name string) (*time.Location, error) {
	if name == "" {
		return nil, NewError(CodeValidationInvalid, "timezone: name is required")
	}
	if strings.EqualFold(name, "local") {
		return nil, NewError(CodeValidationInvalid, "timezone: %q is reserved; use an IANA zone such as Asia/Shanghai", name)
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, Wrap(err, CodeValidationInvalid, "timezone: unknown zone %q", truncate(name))
	}
	return loc, nil
}
