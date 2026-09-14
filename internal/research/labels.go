package research

import (
	"context"
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/injoyai/strategy/internal/analysis"
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/ports"
)

// The fixed M1-09 label economics (D6's 标签价格、入场时点、分红/成本):
// close-to-close total return, gross of dividends and costs (the
// synthetic M1 data carries none), entering at the decision date's own
// close. Labels are evaluation-only: they cross into internal/analysis
// as explicit LabelSet inputs and are never exposed to factor or
// strategy data views (m1-data-factor.md §6.2).
const (
	LabelPrice = "close"
	LabelEntry = "decision_close"
	LabelCost  = "total_return_gross"

	labelDataset   = "bar"
	labelFrequency = "daily"
	labelField     = "close"
)

// barSeries is the label-side view of the price data: every distinct bar
// date in the loaded window plus per-member closes. Event times
// truncate to UTC midnight so decision times, label dates and horizon
// index arithmetic share one calendar.
type barSeries struct {
	dates  []time.Time
	index  map[time.Time]int
	closes map[domain.ID]map[time.Time]float64
}

// decisionDates returns the bar dates inside the analysis window.
func (b *barSeries) decisionDates(window domain.Interval) []time.Time {
	out := make([]time.Time, 0, len(b.dates))
	for _, d := range b.dates {
		if window.Contains(d) {
			out = append(out, d)
		}
	}
	return out
}

// labelSets builds one LabelSet per horizon: for a decision date at
// index i of the full date list and horizon h, the label is
// close(dates[i+h]) / close(dates[i]) - 1, present only when both
// closes exist and the entry price is non-zero. Dates whose exit lies
// beyond the loaded data carry no labels; the analysis reports them as
// missing, never as zero.
func (b *barSeries) labelSets(members []domain.ID, decisionDates []time.Time, horizons []int) map[int]analysis.LabelSet {
	out := make(map[int]analysis.LabelSet, len(horizons))
	for _, h := range horizons {
		returns := make(map[time.Time]map[domain.ID]float64)
		for _, d := range decisionDates {
			i, ok := b.index[d]
			if !ok || i+h >= len(b.dates) {
				continue
			}
			exit := b.dates[i+h]
			day := make(map[domain.ID]float64)
			for _, m := range members {
				entry, hasEntry := b.closes[m][d]
				exitClose, hasExit := b.closes[m][exit]
				if !hasEntry || !hasExit || entry == 0 {
					continue
				}
				ret := exitClose/entry - 1
				if math.IsNaN(ret) || math.IsInf(ret, 0) {
					continue
				}
				day[m] = ret
			}
			if len(day) > 0 {
				returns[d] = day
			}
		}
		out[h] = analysis.LabelSet{Horizon: h, Returns: returns}
	}
	return out
}

// loadBarSeries pages through the label view for the members' daily
// closes. The load window extends (maxHorizon + 2) days past the
// analysis window so exits beyond range.to stay visible; rows are
// PIT-filtered by the view (available_at <= as_of), so an early as_of
// simply yields fewer labels — an honest gap, not an error.
func loadBarSeries(ctx context.Context, view ports.DataView, members []domain.ID, window domain.Interval, maxHorizon int) (*barSeries, error) {
	series := &barSeries{
		index:  make(map[time.Time]int),
		closes: make(map[domain.ID]map[time.Time]float64),
	}
	if len(members) == 0 {
		return series, nil
	}
	loadRange := domain.Interval{
		From: window.From,
		To:   window.To.Add(time.Duration(maxHorizon+2) * 24 * time.Hour),
	}
	query := domain.DataQuery{
		SnapshotID:    view.SnapshotID(),
		AsOf:          view.AsOf(),
		Dataset:       labelDataset,
		Frequency:     labelFrequency,
		InstrumentIDs: members,
		Fields:        []string{labelField},
		Range:         loadRange,
	}
	for {
		page, err := view.Query(ctx, query)
		if err != nil {
			return nil, domain.Wrap(err, domain.CodeValidationInvalid, "research: load label prices")
		}
		for i := range page.Items {
			obs := &page.Items[i]
			if obs.InstrumentID == nil {
				continue
			}
			value, has := obs.Values[labelField]
			if !has || value.Kind != domain.ValueDecimal || value.MissingReason != "" {
				continue
			}
			closePrice, err := strconv.ParseFloat(value.Encoded, 64)
			if err != nil || math.IsNaN(closePrice) || math.IsInf(closePrice, 0) {
				// Undecodable price rows stay out of the labels; the
				// analysis surfaces the gap as missing labels rather than
				// failing the whole request.
				continue
			}
			date := obs.EventTime.Truncate(24 * time.Hour)
			if _, seen := series.index[date]; !seen {
				series.index[date] = 0 // rebuilt after sorting below
				series.dates = append(series.dates, date)
			}
			perMember := series.closes[*obs.InstrumentID]
			if perMember == nil {
				perMember = make(map[time.Time]float64)
				series.closes[*obs.InstrumentID] = perMember
			}
			perMember[date] = closePrice
		}
		if page.NextCursor == "" {
			break
		}
		query.Cursor = page.NextCursor
	}
	sort.Slice(series.dates, func(i, j int) bool { return series.dates[i].Before(series.dates[j]) })
	for i, d := range series.dates {
		series.index[d] = i
	}
	return series, nil
}
