package analysis

import (
	"math"
	"sort"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// LabelSet is the ONLY way future information enters analysis
// (m1-data-factor.md §6.2): future-return labels are an independent
// analysis input computed by the caller, and Factor/Strategy DataViews
// expose no label reads. The import allowlist in architecture_test.go
// keeps this package away from the data stack structurally, so the
// isolation cannot regress silently. Horizon is the holding period in
// the caller's unit (e.g. trading days); Returns maps decision date ->
// instrument -> forward return over that horizon.
type LabelSet struct {
	Horizon int
	Returns map[time.Time]map[domain.ID]float64
}

// normalizeLabels validates every label entry fail-closed and rebuilds
// the map with UTC-normalized decision dates: two caller keys denoting
// the same instant in different zones would silently collide, so they
// are rejected instead. NaN and ±Inf label values are rejected —
// garbage must not reach the statistics (requirements §6.5).
func normalizeLabels(labels map[int]LabelSet, window domain.Interval) (map[int]map[time.Time]map[domain.ID]float64, error) {
	if len(labels) == 0 {
		return nil, domain.NewError(codeLabelsInvalid, "analysis: at least one label horizon is required")
	}
	out := make(map[int]map[time.Time]map[domain.ID]float64, len(labels))
	for horizon, set := range labels {
		if horizon <= 0 {
			return nil, domain.NewError(codeLabelsInvalid, "analysis: label horizon must be positive, got %d", horizon)
		}
		if set.Horizon != horizon {
			return nil, domain.NewError(codeLabelsInvalid, "analysis: label key %d carries horizon %d", horizon, set.Horizon)
		}
		days := make(map[time.Time]map[domain.ID]float64, len(set.Returns))
		for date, members := range set.Returns {
			key := date.UTC()
			if _, exists := days[key]; exists {
				return nil, domain.NewError(codeLabelsInvalid, "analysis: horizon %d has two label dates on the same instant %s", horizon, key.Format(time.RFC3339Nano))
			}
			if !window.Contains(key) {
				return nil, domain.NewError(codeLabelsInvalid, "analysis: horizon %d label date %s is outside the analysis range", horizon, key.Format(time.RFC3339Nano))
			}
			day := make(map[domain.ID]float64, len(members))
			for id, ret := range members {
				if id == "" {
					return nil, domain.NewError(codeLabelsInvalid, "analysis: horizon %d label date %s has an empty instrument id", horizon, key.Format(time.RFC3339Nano))
				}
				if math.IsNaN(ret) || math.IsInf(ret, 0) {
					return nil, domain.NewError(codeLabelsInvalid, "analysis: horizon %d label for %s on %s is not finite", horizon, id, key.Format(time.RFC3339Nano))
				}
				day[id] = ret
			}
			days[key] = day
		}
		out[horizon] = days
	}
	return out, nil
}

// sortedHorizons returns the label horizons ascending — the canonical
// order every horizon-indexed slice is built in.
func sortedHorizons(labels map[int]map[time.Time]map[domain.ID]float64) []int {
	horizons := make([]int, 0, len(labels))
	for h := range labels {
		horizons = append(horizons, h)
	}
	sort.Ints(horizons)
	return horizons
}
