package synthetic

import (
	"fmt"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// knownUnits is the closed set of currency codes the bar fixture accepts.
// A bar whose unit is not in this set is an Issue (unknown_unit), never
// silently rewritten to a default.
var knownUnits = map[string]bool{
	"CNY": true,
	"USD": true,
	"HKD": true,
}

// QualityEngine scans a batch of observations and surfaces Issues for the
// seven M0-06 rules: duplicate natural keys, OHLC violations, negative
// volume, missing trading days, unknown units, future available_at and
// superseded revisions. It never corrects rows in place; callers decide
// whether to publish based on the highest severity returned.
type QualityEngine struct {
	// tradingDays is the set of calendar event dates (YYYY-MM-DD) that the
	// bar dataset expects every instrument to cover. Missing days surface
	// as missing_trading_day issues.
	tradingDays map[string]bool
	// now is the ingestion reference time; available_at > now is a future
	// visibility violation (pit_unverified at minimum).
	now time.Time
}

// NewQualityEngine builds an engine over a calendar fixture and a pinned
// now. The calendar is a slice of observations (typically the calendar
// dataset); each one's EventTime date part is added to the expected days.
func NewQualityEngine(calendar []domain.Observation, now time.Time) *QualityEngine {
	days := make(map[string]bool, len(calendar))
	for _, obs := range calendar {
		if obs.Dataset == DatasetCalendar {
			days[obs.EventTime.Format("2006-01-02")] = true
		}
	}
	return &QualityEngine{tradingDays: days, now: now.UTC()}
}

// Issue reports one quality finding. Path is the natural key of the offending
// row so operators can locate it; severity is error for blockers and warning
// or info for tolerated conditions.
type Issue = domain.Issue

// Check scans obs and returns Issues in encounter order. The same row may
// produce several Issues (e.g. an OHLC violation and a negative volume); the
// caller should not assume one issue per row.
func (q *QualityEngine) Check(obs []domain.Observation) []Issue {
	var issues []Issue

	// Index by natural key (instrument_id + event_time) to detect duplicates
	// and revisions. A revision is a legitimate new revision_id on the same
	// key; it surfaces as info, not an error, so the snapshot can still
	// publish while operators see the supersession.
	type keyEntry struct {
		revision string
		row      int
	}
	seen := make(map[string][]keyEntry)
	for i, o := range obs {
		if o.InstrumentID == nil {
			continue
		}
		k := naturalKey(*o.InstrumentID, o.EventTime)
		seen[k] = append(seen[k], keyEntry{revision: o.Provenance.RevisionID, row: i})
	}

	for k, entries := range seen {
		// Duplicate natural key: same instrument + event_time + revision.
		// That is an error: a batch cannot carry two identical rows.
		revs := make(map[string]int, len(entries))
		for _, e := range entries {
			revs[e.revision]++
		}
		for rev, count := range revs {
			if count > 1 {
				issues = append(issues, Issue{
					Code:     domain.CodeQualityDuplicateNaturalKey,
					Path:     k,
					Severity: domain.SeverityError,
					Message:  fmt.Sprintf("duplicate natural key (instrument=%s, event_time=%s, revision=%s) appears %d times", *obs[entries[0].row].InstrumentID, obs[entries[0].row].EventTime.Format(time.RFC3339), rev, count),
				})
			}
		}
		// Superseded revision: more than one distinct revision on the same
		// natural key. This is expected when a provider publishes a
		// correction; surface it as info so downstream knows the latest wins.
		if len(revs) > 1 {
			issues = append(issues, Issue{
				Code:     domain.CodeQualitySupersededRevision,
				Path:     k,
				Severity: domain.SeverityInfo,
				Message:  fmt.Sprintf("natural key %s has %d revisions; latest wins within snapshot", k, len(revs)),
			})
		}
	}

	// Per-row field checks (OHLC, volume, unit, available_at).
	instrumentDays := make(map[string]map[string]bool)
	for _, o := range obs {
		if o.Dataset != DatasetBar {
			continue
		}
		path := naturalKeyPtr(o.InstrumentID, o.EventTime)

		// OHLC: high >= low; open and close in [low, high].
		high, _, hasHigh := decimalField(o, "high")
		low, _, hasLow := decimalField(o, "low")
		openV, _, hasOpen := decimalField(o, "open")
		closeV, _, hasClose := decimalField(o, "close")
		if hasHigh && hasLow {
			if diff, err := high.Sub(low); err == nil && diff.IsNegative() {
				issues = append(issues, Issue{
					Code:     domain.CodeQualityOHLCViolation,
					Path:     path,
					Severity: domain.SeverityError,
					Message:  fmt.Sprintf("OHLC violation: high (%s) < low (%s)", high, low),
				})
			}
			if hasOpen {
				if diff, err := openV.Sub(low); err == nil && diff.IsNegative() {
					issues = append(issues, Issue{
						Code:     domain.CodeQualityOHLCViolation,
						Path:     path,
						Severity: domain.SeverityError,
						Message:  fmt.Sprintf("OHLC violation: open (%s) < low (%s)", openV, low),
					})
				}
			}
			if hasClose {
				if diff, err := closeV.Sub(high); err == nil && diff.IsPositive() {
					issues = append(issues, Issue{
						Code:     domain.CodeQualityOHLCViolation,
						Path:     path,
						Severity: domain.SeverityError,
						Message:  fmt.Sprintf("OHLC violation: close (%s) > high (%s)", closeV, high),
					})
				}
			}
		}

		// Volume: non-negative.
		vol, volStr, hasVol := decimalField(o, "volume")
		if hasVol {
			if vol.IsNegative() {
				issues = append(issues, Issue{
					Code:     domain.CodeQualityNegativeVolume,
					Path:     path,
					Severity: domain.SeverityError,
					Message:  fmt.Sprintf("negative volume %s", volStr),
				})
			}
		}

		// Unit: must be in the known set.
		if u, ok := o.Values["unit"]; ok && u.MissingReason == "" {
			if !knownUnits[u.Encoded] {
				issues = append(issues, Issue{
					Code:     domain.CodeQualityUnknownUnit,
					Path:     path,
					Severity: domain.SeverityError,
					Message:  fmt.Sprintf("unknown unit %q", u.Encoded),
				})
			}
		}

		// available_at: must not be later than the ingestion reference time.
		if o.Provenance.AvailableAt.After(q.now) {
			issues = append(issues, Issue{
				Code:     domain.CodeQualityFutureAvailableAt,
				Path:     path,
				Severity: domain.SeverityError,
				Message:  fmt.Sprintf("available_at %s is after the ingestion time %s", o.Provenance.AvailableAt.Format(time.RFC3339), q.now.Format(time.RFC3339)),
			})
		}

		// Track which calendar days each instrument has a bar for; we report
		// missing days after the loop.
		if o.InstrumentID != nil {
			inst := o.InstrumentID.String()
			if instrumentDays[inst] == nil {
				instrumentDays[inst] = make(map[string]bool, len(q.tradingDays))
			}
			instrumentDays[inst][o.EventTime.Format("2006-01-02")] = true
		}
	}

	// Missing trading days: every instrument in the bar dataset should have
	// a bar for each calendar day. Missing days are warnings (the snapshot
	// may still publish with a documented gap) but operators must see them.
	for inst, days := range instrumentDays {
		for day := range q.tradingDays {
			if !days[day] {
				issues = append(issues, Issue{
					Code:     domain.CodeQualityMissingTradingDay,
					Path:     fmt.Sprintf("instrument=%s/day=%s", inst, day),
					Severity: domain.SeverityWarning,
					Message:  fmt.Sprintf("instrument %s has no bar for trading day %s", inst, day),
				})
			}
		}
	}

	return issues
}

// CanPublish reports whether the issues allow publishing. Error severity
// blocks; warnings and info pass through so a snapshot can still capture a
// known gap. Strict mode is the caller's decision (snapshot.strict_pit);
// the engine only reports what it found.
func CanPublish(issues []Issue) bool {
	for _, i := range issues {
		if i.Severity == domain.SeverityError {
			return false
		}
	}
	return true
}

// naturalKey builds the lookup key for a bar observation: instrument + event
// date (date only, so intraday revisions on the same day collapse).
func naturalKey(id domain.ID, at time.Time) string {
	return fmt.Sprintf("%s|%s", id.String(), at.Format("2006-01-02"))
}

func naturalKeyPtr(id *domain.ID, at time.Time) string {
	if id == nil {
		return "instrument=<nil>|" + at.Format("2006-01-02")
	}
	return naturalKey(*id, at)
}

// decimalField extracts a decimal field from an observation, parsing it
// through the domain's canonical form. The returned Decimal is usable for
// comparisons; the string form is the original encoded value for messages.
func decimalField(o domain.Observation, name string) (domain.Decimal, string, bool) {
	v, ok := o.Values[name]
	if !ok || v.MissingReason != "" || v.Encoded == "" {
		return "", "", false
	}
	parsed, err := domain.ParseDecimal(v.Encoded)
	if err != nil {
		return domain.Decimal(v.Encoded), v.Encoded, true // report raw so the issue is honest
	}
	return parsed, v.Encoded, true
}
