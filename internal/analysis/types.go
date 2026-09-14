package analysis

import (
	"math"
	"time"
)

// Stat is one metric that may be unavailable. Value nil + Reason set is
// the null+reason contract of m1-data-factor.md §7 — zero denominators,
// insufficient samples and not-applicable cases are reported, never
// papered over with 0. A present Value is always finite: NaN and ±Inf
// are structurally kept out of JSON (requirements §6.5).
type Stat struct {
	Value  *float64 `json:"value"`
	Reason string   `json:"reason,omitempty"`
}

// valueStat wraps a computed metric. The finiteness guard is the last
// line of defense for the no-NaN-in-JSON rule; the math upstream should
// make it unreachable.
func valueStat(v float64) Stat {
	if math.IsNaN(v) || math.IsInf(v, 0) {
		return Stat{Reason: ReasonInvalidValue}
	}
	return Stat{Value: &v}
}

// nullStat builds an unavailable metric with its reason.
func nullStat(reason string) Stat { return Stat{Reason: reason} }

// Distribution summarizes one cross-section of factor values (or the
// pooled values of the whole run). Quantiles use linear interpolation
// over the sorted values.
type Distribution struct {
	Count int  `json:"count"`
	Mean  Stat `json:"mean"`
	Std   Stat `json:"std"` // sample (n-1); null when count < 2
	Min   Stat `json:"min"`
	Q05   Stat `json:"q05"`
	Q25   Stat `json:"q25"`
	Q50   Stat `json:"q50"`
	Q75   Stat `json:"q75"`
	Q95   Stat `json:"q95"`
	Max   Stat `json:"max"`
}

// SeriesSummary aggregates a daily series (IC values, spreads, ...) over
// the dates where it was computable; N is that date count. IR is mean
// over sample std — a constant series has zero std and reports the
// zero-denominator reason instead of an inflated ratio.
type SeriesSummary struct {
	N            int  `json:"n"`
	Mean         Stat `json:"mean"`
	Std          Stat `json:"std"`
	IR           Stat `json:"ir"`
	PositiveRate Stat `json:"positive_rate"` // share of values > 0
}

// BucketStats is one quantile bucket on one date: bucket 1 holds the
// lowest factor values, Groups the highest. Mean is the bucket's mean
// label return — factor evidence per EvidenceNote, not a tradable
// backtest.
type BucketStats struct {
	Bucket int  `json:"bucket"`
	Size   int  `json:"size"`
	Mean   Stat `json:"mean_return"`
}

// BucketSummary aggregates one bucket across dates: the mean of the
// daily bucket means, equal-weight per date.
type BucketSummary struct {
	Bucket int  `json:"bucket"`
	Dates  int  `json:"dates"`
	Mean   Stat `json:"mean_return"`
}

// HorizonDailyStats is the per-date, per-horizon correlation and
// grouping block. Pairs counts members with both a usable factor value
// and a label. Buckets is present only when Pairs >= Groups; Spread is
// then bucket Groups' mean minus bucket 1's mean, otherwise null with
// not_applicable.
type HorizonDailyStats struct {
	Horizon int           `json:"horizon"`
	Pairs   int           `json:"pairs"`
	IC      Stat          `json:"ic"`      // Pearson vs the horizon's returns
	RankIC  Stat          `json:"rank_ic"` // Spearman on average ranks
	Buckets []BucketStats `json:"buckets,omitempty"`
	Spread  Stat          `json:"high_low_spread"`
}

// DailyStats is one decision date's full record. The artifact series
// carries one entry per cross-section date at full fidelity — chart
// downsampling (D6) is a render-time concern that must never mutate the
// artifact.
type DailyStats struct {
	Date    time.Time `json:"date"`
	Segment string    `json:"segment,omitempty"` // "", train, validation or test
	Covered int       `json:"covered"`           // frame members carrying a value
	Total   int       `json:"total"`             // all frame members
	// MissingReasons counts per missing reason (reason -> member count).
	// The reason strings come from the factor engine's Frame; analysis
	// adds invalid_value for values that fail float decoding, so §6.2's
	// "report per reason" stays true even for undecodable decimals.
	MissingReasons map[string]int      `json:"missing_reasons,omitempty"`
	Distribution   Distribution        `json:"distribution"`
	Horizons       []HorizonDailyStats `json:"horizons"` // one entry per configured horizon, ascending
}

// CoverageSummary is the run-level coverage rollup. Covered follows the
// frame invariant (value or missing), so members with undecodable
// decimals stay counted here as covered and are separately visible
// under invalid_value in MissingReasons — §6.2: report coverage per
// reason, never substitute 0.
type CoverageSummary struct {
	Covered int  `json:"covered"`
	Total   int  `json:"total"`
	Rate    Stat `json:"rate"` // covered / total; an empty universe has no rate
}

// HorizonSummary aggregates one horizon across the run. This slice,
// ordered by ascending horizon, is D6's decay view: how IC, Rank IC,
// bucket means and the high-minus-low spread evolve as the holding
// period grows. Correlation aggregates the daily series selected by
// Config.Method; IC and Rank IC are always fully reported
// (requirements §7.1). Series are collected only over dates where the
// daily Stat carried a value — N reflects that computable-day count.
type HorizonSummary struct {
	Horizon           int             `json:"horizon"`
	LabelDates        int             `json:"label_dates"`         // label entries inside the range
	MissingLabelDates int             `json:"missing_label_dates"` // cross-section dates without labels
	Pairs             int             `json:"pairs"`
	IC                SeriesSummary   `json:"ic"`
	RankIC            SeriesSummary   `json:"rank_ic"`
	Correlation       SeriesSummary   `json:"correlation"`
	Buckets           []BucketSummary `json:"buckets,omitempty"`
	Spread            SeriesSummary   `json:"high_low_spread"`
}

// SegmentHorizon is the label count of one segment for one horizon —
// D6's train/validation/test label accounting.
type SegmentHorizon struct {
	Horizon int `json:"horizon"`
	Labels  int `json:"labels"` // paired label observations
}

// SegmentSummary counts dates, covered samples and per-horizon labels
// of one train/validation/test segment. Any future fitting must read
// the train segment only (requirements §7.1).
type SegmentSummary struct {
	Kind     string           `json:"kind"`
	From     time.Time        `json:"from"`
	To       time.Time        `json:"to"`
	Dates    int              `json:"dates"`   // cross-section dates in the segment
	Samples  int              `json:"samples"` // covered member-days
	Horizons []SegmentHorizon `json:"horizons"`
}

// Summary is the page-level rollup of the full series (D6: a page
// summary next to the complete artifact; downsampling only affects
// charts).
type Summary struct {
	Dates          int              `json:"dates"`
	Coverage       CoverageSummary  `json:"coverage"`
	MissingReasons map[string]int   `json:"missing_reasons,omitempty"`
	Pooled         Distribution     `json:"pooled_distribution"`
	Horizons       []HorizonSummary `json:"horizons"`
	Segments       []SegmentSummary `json:"segments,omitempty"`
	// UncoveredDates lists cross-section dates that fall into no
	// segment. Reported only when segments are configured — the spec's
	// "explicitly cover the run window": gaps are surfaced, never hidden.
	UncoveredDates []time.Time `json:"uncovered_dates,omitempty"`
}
