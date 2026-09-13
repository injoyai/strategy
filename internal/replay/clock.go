package replay

import "time"

// DecisionClock is the server-controlled historical calendar. Decision points
// are ascending Eastern-trading decision times owned by the service, so the
// client can neither reach into the future nor fabricate a decision time.
// A zero DecisionClock yields no decision points (strict, fail-closed).
type DecisionClock struct {
	Points []time.Time // ascending; already normalised by the server
}

// AtOrAfter returns the earliest decision point not before t.
func (d DecisionClock) AtOrAfter(t time.Time) (time.Time, bool) {
	for _, p := range d.Points {
		if !p.Before(t) {
			return p, true
		}
	}
	return time.Time{}, false
}

// NextAfter returns the earliest decision point strictly after t, used to
// compute the next advance target.
func (d DecisionClock) NextAfter(t time.Time) (time.Time, bool) {
	for _, p := range d.Points {
		if p.After(t) {
			return p, true
		}
	}
	return time.Time{}, false
}
