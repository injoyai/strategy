package analysis

import (
	"math"
	"sort"
	"strconv"
	"time"

	"github.com/injoyai/strategy/internal/domain"
)

// run accumulates the per-date computation state the summary needs
// beyond the DailyStats themselves. One run instance owns one analysis;
// it is never reused across requests.
type run struct {
	cfg      Config
	horizons []int
	labels   map[int]map[time.Time]map[domain.ID]float64
	segments []Segment

	daily   []DailyStats
	pooled  []float64
	missing map[string]int
	covered int
	members int
}

func newRun(cfg Config, horizons []int, labels map[int]map[time.Time]map[domain.ID]float64, segments []Segment) *run {
	return &run{
		cfg:      cfg,
		horizons: horizons,
		labels:   labels,
		segments: segments,
		missing:  make(map[string]int),
	}
}

// usableValue is one member's factor value decoded to float64. Frames
// carry domain.Decimal; analysis statistics work in float64
// (requirements §6.5), with undecodable values reported under
// invalid_value instead of silently dropped.
type usableValue struct {
	id    domain.ID
	value float64
}

// addDate computes one cross-section's daily record: coverage, missing
// reasons, the factor distribution and one block per horizon. Members
// are traversed in sorted id order — the fixed order behind every
// floating-point sum in this package, so equal inputs always yield
// bit-identical artifacts.
func (r *run) addDate(cs CrossSection) {
	frame := cs.Frame
	date := cs.Time.UTC()
	total := len(frame.Values) + len(frame.Missing)
	r.members += total
	r.covered += len(frame.Values)

	ids := make([]domain.ID, 0, len(frame.Values))
	for id := range frame.Values {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })

	values := make([]float64, 0, len(ids))
	usable := make([]usableValue, 0, len(ids))
	invalid := 0
	for _, id := range ids {
		// ParseFloat accepts "NaN"/"Inf" literals and saturates on
		// overflow — both must be rejected, not fed to the statistics.
		v, err := strconv.ParseFloat(string(frame.Values[id]), 64)
		if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
			invalid++
			continue
		}
		usable = append(usable, usableValue{id: id, value: v})
		values = append(values, v)
	}

	reasons := make(map[string]int, len(frame.Missing)+1)
	for _, reason := range frame.Missing {
		reasons[reason]++
	}
	if invalid > 0 {
		reasons[ReasonInvalidValue] += invalid
	}

	stats := DailyStats{
		Date:         date,
		Segment:      r.segmentOf(date),
		Covered:      len(frame.Values),
		Total:        total,
		Distribution: newDistribution(values),
		Horizons:     make([]HorizonDailyStats, 0, len(r.horizons)),
	}
	if len(reasons) > 0 {
		stats.MissingReasons = reasons
	}
	for _, h := range r.horizons {
		stats.Horizons = append(stats.Horizons, r.horizonStats(date, h, usable))
	}
	r.daily = append(r.daily, stats)
	r.pooled = append(r.pooled, values...)
	for reason, count := range reasons {
		r.missing[reason] += count
	}
}

// segmentOf returns which segment contains date ("" when none).
func (r *run) segmentOf(date time.Time) string {
	for _, seg := range r.segments {
		if seg.Range.Contains(date) {
			return string(seg.Kind)
		}
	}
	return ""
}

// horizonStats computes one date × horizon block: pairs, correlations
// and quantile buckets. Reasons surface D6's null contract: no label
// entry for the date -> missing_labels; pairs below MinSamples ->
// insufficient_samples; degenerate (zero-variance) inputs ->
// zero_variance; fewer pairs than buckets -> grouping not applicable.
func (r *run) horizonStats(date time.Time, horizon int, usable []usableValue) HorizonDailyStats {
	day, labelDate := r.labels[horizon][date]
	stats := HorizonDailyStats{Horizon: horizon}
	pairs := make([]pair, 0, len(usable))
	for _, u := range usable {
		if ret, ok := day[u.id]; ok {
			pairs = append(pairs, pair{id: u.id, value: u.value, ret: ret})
		}
	}
	stats.Pairs = len(pairs)
	if !labelDate || len(pairs) < r.cfg.MinSamples {
		stats.Spread = nullStat(ReasonNotApplicable)
		if !labelDate {
			stats.IC = nullStat(ReasonMissingLabels)
			stats.RankIC = nullStat(ReasonMissingLabels)
		} else {
			stats.IC = nullStat(ReasonInsufficientSamples)
			stats.RankIC = nullStat(ReasonInsufficientSamples)
		}
		return stats
	}

	xs := make([]float64, len(pairs))
	ys := make([]float64, len(pairs))
	for i, p := range pairs {
		xs[i], ys[i] = p.value, p.ret
	}
	if ic, ok := pearson(xs, ys); ok {
		stats.IC = valueStat(ic)
	} else {
		stats.IC = nullStat(ReasonZeroVariance)
	}
	if rankIC, ok := spearman(xs, ys); ok {
		stats.RankIC = valueStat(rankIC)
	} else {
		stats.RankIC = nullStat(ReasonZeroVariance)
	}
	// Grouping is independent of correlation validity: buckets answer a
	// conditional-mean question, not a co-movement one, and remain
	// meaningful when one series is constant.
	if len(pairs) >= r.cfg.Groups {
		stats.Buckets, stats.Spread = bucketize(pairs, r.cfg.Groups)
	} else {
		stats.Spread = nullStat(ReasonNotApplicable)
	}
	return stats
}

// summary rolls the daily records up: pooled distribution, missing
// reasons, coverage, the per-horizon decay block and the segment
// accounting with uncovered dates.
func (r *run) summary() Summary {
	s := Summary{
		Dates:          len(r.daily),
		Coverage:       CoverageSummary{Covered: r.covered, Total: r.members},
		MissingReasons: r.missing,
		Pooled:         newDistribution(r.pooled),
		Horizons:       make([]HorizonSummary, 0, len(r.horizons)),
	}
	if r.members == 0 {
		// No members at all: no rate can exist. zero_variance is
		// reserved for statistics whose inputs exist but have zero
		// spread; an empty universe is a sample problem.
		s.Coverage.Rate = nullStat(ReasonInsufficientSamples)
	} else {
		s.Coverage.Rate = valueStat(float64(r.covered) / float64(r.members))
	}
	for _, h := range r.horizons {
		s.Horizons = append(s.Horizons, r.horizonSummary(h))
	}
	if len(r.segments) > 0 {
		s.Segments, s.UncoveredDates = r.segmentSummaries()
	}
	return s
}

// horizonSummary aggregates one horizon across all dates.
func (r *run) horizonSummary(horizon int) HorizonSummary {
	hs := HorizonSummary{Horizon: horizon}
	days := r.labels[horizon]
	hs.LabelDates = len(days)
	var ic, rankIC, spreads []float64
	bucketMeans := make([][]float64, r.cfg.Groups)
	for i := range r.daily {
		day := &r.daily[i]
		if _, present := days[day.Date]; !present {
			hs.MissingLabelDates++
		}
		var block HorizonDailyStats
		for j := range day.Horizons {
			if day.Horizons[j].Horizon == horizon {
				block = day.Horizons[j]
				break
			}
		}
		hs.Pairs += block.Pairs
		if block.IC.Value != nil {
			ic = append(ic, *block.IC.Value)
		}
		if block.RankIC.Value != nil {
			rankIC = append(rankIC, *block.RankIC.Value)
		}
		if block.Spread.Value != nil {
			spreads = append(spreads, *block.Spread.Value)
		}
		for _, b := range block.Buckets {
			if b.Mean.Value != nil {
				idx := b.Bucket - 1
				bucketMeans[idx] = append(bucketMeans[idx], *b.Mean.Value)
			}
		}
	}
	hs.IC = newSeriesSummary(ic)
	hs.RankIC = newSeriesSummary(rankIC)
	// The method-selected aggregate: Config.Method chooses which daily
	// series the summary headlines; both defined series stay fully
	// reported (requirements §7.1).
	if r.cfg.Method == MethodSpearman {
		hs.Correlation = hs.RankIC
	} else {
		hs.Correlation = hs.IC
	}
	hs.Spread = newSeriesSummary(spreads)
	for b := 1; b <= r.cfg.Groups; b++ {
		means := bucketMeans[b-1]
		summary := BucketSummary{Bucket: b, Dates: len(means), Mean: nullStat(ReasonInsufficientSamples)}
		if len(means) > 0 {
			summary.Mean = valueStat(mean(means))
		}
		hs.Buckets = append(hs.Buckets, summary)
	}
	return hs
}

// segmentSummaries counts per-segment dates, covered samples and
// per-horizon label pairs, and collects the cross-section dates that
// fall into no segment — the explicit run-window coverage the spec
// demands (gaps are reported, never hidden).
func (r *run) segmentSummaries() ([]SegmentSummary, []time.Time) {
	out := make([]SegmentSummary, 0, len(r.segments))
	var uncovered []time.Time
	for _, seg := range r.segments {
		ss := SegmentSummary{
			Kind:     string(seg.Kind),
			From:     seg.Range.From,
			To:       seg.Range.To,
			Horizons: make([]SegmentHorizon, 0, len(r.horizons)),
		}
		pairs := make(map[int]int, len(r.horizons))
		for i := range r.daily {
			day := &r.daily[i]
			if seg.Range.Contains(day.Date) {
				ss.Dates++
				ss.Samples += day.Covered
				for j := range day.Horizons {
					pairs[day.Horizons[j].Horizon] += day.Horizons[j].Pairs
				}
			}
		}
		for _, h := range r.horizons {
			ss.Horizons = append(ss.Horizons, SegmentHorizon{Horizon: h, Labels: pairs[h]})
		}
		out = append(out, ss)
	}
	for i := range r.daily {
		if r.segmentOf(r.daily[i].Date) == "" {
			uncovered = append(uncovered, r.daily[i].Date)
		}
	}
	return out, uncovered
}
