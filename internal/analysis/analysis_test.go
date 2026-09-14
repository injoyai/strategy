package analysis

import (
	"encoding/json"
	"errors"
	"math"
	"strings"
	"testing"
	"time"

	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
	"github.com/injoyai/strategy/internal/ports"
)

// Fixture dates: one calendar week plus sentinels outside the window.
var (
	d0 = time.Date(2026, 1, 4, 0, 0, 0, 0, time.UTC)
	d1 = time.Date(2026, 1, 5, 0, 0, 0, 0, time.UTC)
	d2 = time.Date(2026, 1, 6, 0, 0, 0, 0, time.UTC)
	d3 = time.Date(2026, 1, 7, 0, 0, 0, 0, time.UTC)
	d4 = time.Date(2026, 1, 8, 0, 0, 0, 0, time.UTC)
	d5 = time.Date(2026, 1, 9, 0, 0, 0, 0, time.UTC)
	d9 = time.Date(2026, 1, 20, 0, 0, 0, 0, time.UTC)
)

var testRef = factor.FactorRef{ID: "test-alpha", Version: "v1"}

// newTestEngine registers a minimal builtin spec (a required integer
// parameter exercises the fail-closed canonicalization) and returns an
// engine over a real registry. Frames are hand-built: the analysis
// boundary is the Frame type, and keeping the data stack out of these
// fixtures is itself part of the label-isolation story.
func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	registry, err := factor.NewRegistry(ports.SHA256Checksummer{})
	if err != nil {
		t.Fatal(err)
	}
	spec := &factor.Spec{
		ID:      "test-alpha",
		Version: "v1",
		Title:   "Test alpha",
		Kind:    factor.KindBuiltin,
		Params: []factor.Param{
			{Name: "window", Type: factor.ParamInteger, Required: true},
		},
		Inputs: []factor.Input{{
			Name: "close", Dataset: "bar", Field: "close", Frequency: "daily",
			Lookback: 1, Unit: "usd", PIT: true,
		}},
		OutputUnit:   "usd",
		AssetClasses: []string{"equity"},
		Compute: func(*factor.ComputeContext) (domain.Decimal, string, error) {
			return domain.Decimal("0"), "", nil
		},
	}
	if err := registry.Register(spec); err != nil {
		t.Fatal(err)
	}
	engine, err := NewEngine(registry, ports.SHA256Checksummer{})
	if err != nil {
		t.Fatal(err)
	}
	return engine
}

// frame builds one hand-made factor.Frame with the configured ref.
func frame(t time.Time, values map[string]string, missing map[string]string) *factor.Frame {
	f := &factor.Frame{
		Ref:     testRef,
		AsOf:    t,
		Values:  make(map[domain.ID]domain.Decimal, len(values)),
		Missing: make(map[domain.ID]string, len(missing)),
	}
	for id, v := range values {
		f.Values[domain.ID(id)] = domain.Decimal(v)
	}
	for id, reason := range missing {
		f.Missing[domain.ID(id)] = reason
	}
	return f
}

// labelSet builds one LabelSet from string-keyed fixtures.
func labelSet(horizon int, days map[time.Time]map[string]float64) LabelSet {
	set := LabelSet{Horizon: horizon, Returns: make(map[time.Time]map[domain.ID]float64, len(days))}
	for date, members := range days {
		day := make(map[domain.ID]float64, len(members))
		for id, ret := range members {
			day[domain.ID(id)] = ret
		}
		set.Returns[date] = day
	}
	return set
}

func baseConfig() Config {
	return Config{
		Ref:        testRef,
		Params:     map[string]any{"window": 20},
		LabelPrice: "close",
		LabelEntry: "decision_close",
		LabelCost:  "total_return_gross",
		Method:     MethodPearson,
		Groups:     2,
		MinSamples: 2,
		Range:      domain.Interval{From: d1, To: d4},
	}
}

func baseRequest() Request {
	values := map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}
	return Request{
		Config: baseConfig(),
		Series: []CrossSection{
			{Time: d1, Frame: frame(d1, values, nil)},
			{Time: d2, Frame: frame(d2, values, nil)},
			{Time: d3, Frame: frame(d3, values, nil)},
		},
		Labels: map[int]LabelSet{
			1: labelSet(1, map[time.Time]map[string]float64{
				d1: {"a": 0.1, "b": 0.2, "c": 0.3, "d": 0.4},
				d2: {"a": 0.1, "b": 0.2, "c": 0.3, "d": 0.4},
				d3: {"a": 0.1, "b": 0.2, "c": 0.3, "d": 0.4},
			}),
		},
	}
}

func assertErr(t *testing.T, err error, code, contains string) {
	t.Helper()
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	var domErr *domain.Error
	if !errors.As(err, &domErr) {
		t.Fatalf("expected *domain.Error, got %T: %v", err, err)
	}
	if domErr.Code != code {
		t.Fatalf("code = %q, want %q (err: %v)", domErr.Code, code, err)
	}
	if !strings.Contains(err.Error(), contains) {
		t.Fatalf("error %q does not contain %q", err.Error(), contains)
	}
}

func requireValue(t *testing.T, s Stat, want float64) {
	t.Helper()
	if s.Reason != "" {
		t.Fatalf("stat carried reason %q, want value %v", s.Reason, want)
	}
	if s.Value == nil {
		t.Fatalf("stat value is nil, want %v", want)
	}
	if math.Abs(*s.Value-want) > 1e-9 {
		t.Fatalf("stat value = %v, want %v", *s.Value, want)
	}
}

func requireReason(t *testing.T, s Stat, reason string) {
	t.Helper()
	if s.Value != nil {
		t.Fatalf("stat value = %v, want null with reason %q", *s.Value, reason)
	}
	if s.Reason != reason {
		t.Fatalf("stat reason = %q, want %q", s.Reason, reason)
	}
}

func horizonOf(t *testing.T, day DailyStats, horizon int) HorizonDailyStats {
	t.Helper()
	for _, block := range day.Horizons {
		if block.Horizon == horizon {
			return block
		}
	}
	t.Fatalf("date %s has no horizon %d", day.Date.Format(time.RFC3339), horizon)
	return HorizonDailyStats{}
}

func summaryHorizon(t *testing.T, s Summary, horizon int) HorizonSummary {
	t.Helper()
	for _, hs := range s.Horizons {
		if hs.Horizon == horizon {
			return hs
		}
	}
	t.Fatalf("summary has no horizon %d", horizon)
	return HorizonSummary{}
}

// TestRunCoverageAndMissingReasons walks the D6 "coverage and missing
// reasons over time" requirement: per-date coverage, per-reason counts
// (including the analysis-added invalid_value), the distribution, and
// the run-level rollups.
func TestRunCoverageAndMissingReasons(t *testing.T) {
	engine := newTestEngine(t)
	req := Request{
		Config: baseConfig(),
		Series: []CrossSection{
			{Time: d1, Frame: frame(d1, map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}, nil)},
			{Time: d2, Frame: frame(d2, map[string]string{"a": "1", "b": "not-a-decimal", "d": "4"},
				map[string]string{"c": factor.ReasonMissingInput, "e": factor.ReasonStaleInput})},
			{Time: d3, Frame: frame(d3, nil, nil)},
		},
		Labels: map[int]LabelSet{
			1: labelSet(1, map[time.Time]map[string]float64{
				d1: {"a": 0.1, "b": 0.2, "c": 0.3, "d": 0.4},
			}),
		},
	}
	artifact, err := engine.Run(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(artifact.Series) != 3 {
		t.Fatalf("series length = %d, want 3", len(artifact.Series))
	}

	// d1: full coverage, interpolated quantiles over [1,2,3,4].
	day1 := artifact.Series[0]
	if day1.Covered != 4 || day1.Total != 4 {
		t.Fatalf("d1 covered/total = %d/%d, want 4/4", day1.Covered, day1.Total)
	}
	if len(day1.MissingReasons) != 0 {
		t.Fatalf("d1 missing reasons = %v, want none", day1.MissingReasons)
	}
	if day1.Distribution.Count != 4 {
		t.Fatalf("d1 distribution count = %d, want 4", day1.Distribution.Count)
	}
	requireValue(t, day1.Distribution.Mean, 2.5)
	requireValue(t, day1.Distribution.Min, 1)
	requireValue(t, day1.Distribution.Max, 4)
	requireValue(t, day1.Distribution.Q05, 1.15)
	requireValue(t, day1.Distribution.Q25, 1.75)
	requireValue(t, day1.Distribution.Q50, 2.5)
	requireValue(t, day1.Distribution.Q75, 3.25)
	requireValue(t, day1.Distribution.Q95, 3.85)
	requireValue(t, day1.Distribution.Std, math.Sqrt(5.0/3.0))

	// d1 horizon 1: perfect correlation, two buckets, high-minus-low.
	block1 := horizonOf(t, day1, 1)
	if block1.Pairs != 4 {
		t.Fatalf("d1 pairs = %d, want 4", block1.Pairs)
	}
	requireValue(t, block1.IC, 1)
	requireValue(t, block1.RankIC, 1)
	if len(block1.Buckets) != 2 {
		t.Fatalf("d1 buckets = %d, want 2", len(block1.Buckets))
	}
	if block1.Buckets[0].Bucket != 1 || block1.Buckets[0].Size != 2 {
		t.Fatalf("d1 bucket 1 = %+v", block1.Buckets[0])
	}
	requireValue(t, block1.Buckets[0].Mean, 0.15)
	requireValue(t, block1.Buckets[1].Mean, 0.35)
	requireValue(t, block1.Spread, 0.2)

	// d2: one undecodable decimal plus two frame-level reasons — every
	// reason is counted separately, nothing is zeroed.
	day2 := artifact.Series[1]
	if day2.Covered != 3 || day2.Total != 5 {
		t.Fatalf("d2 covered/total = %d/%d, want 3/5", day2.Covered, day2.Total)
	}
	if day2.MissingReasons[factor.ReasonMissingInput] != 1 ||
		day2.MissingReasons[factor.ReasonStaleInput] != 1 ||
		day2.MissingReasons[ReasonInvalidValue] != 1 {
		t.Fatalf("d2 missing reasons = %v, want one each of missing_input, stale_input, invalid_value", day2.MissingReasons)
	}
	if day2.Distribution.Count != 2 {
		t.Fatalf("d2 distribution count = %d, want 2 (undecodable value excluded)", day2.Distribution.Count)
	}
	requireValue(t, day2.Distribution.Mean, 2.5)
	requireValue(t, day2.Distribution.Std, math.Sqrt(4.5))

	// d2 horizon 1: the label date is absent -> missing_labels.
	block2 := horizonOf(t, day2, 1)
	if block2.Pairs != 0 {
		t.Fatalf("d2 pairs = %d, want 0", block2.Pairs)
	}
	requireReason(t, block2.IC, ReasonMissingLabels)
	requireReason(t, block2.RankIC, ReasonMissingLabels)
	requireReason(t, block2.Spread, ReasonNotApplicable)
	if block2.Buckets != nil {
		t.Fatalf("d2 buckets = %+v, want none", block2.Buckets)
	}

	// d3: an empty universe yields no coverage rate at all.
	day3 := artifact.Series[2]
	if day3.Covered != 0 || day3.Total != 0 {
		t.Fatalf("d3 covered/total = %d/%d, want 0/0", day3.Covered, day3.Total)
	}
	requireReason(t, day3.Distribution.Mean, ReasonInsufficientSamples)

	// Run-level rollups.
	if artifact.Summary.Dates != 3 {
		t.Fatalf("summary dates = %d, want 3", artifact.Summary.Dates)
	}
	if artifact.Summary.Coverage.Covered != 7 || artifact.Summary.Coverage.Total != 9 {
		t.Fatalf("summary covered/total = %d/%d, want 7/9", artifact.Summary.Coverage.Covered, artifact.Summary.Coverage.Total)
	}
	requireValue(t, artifact.Summary.Coverage.Rate, 7.0/9.0)
	if artifact.Summary.Pooled.Count != 6 {
		t.Fatalf("pooled count = %d, want 6", artifact.Summary.Pooled.Count)
	}
	requireValue(t, artifact.Summary.Pooled.Mean, 2.5)
	if artifact.Summary.MissingReasons[factor.ReasonStaleInput] != 1 ||
		artifact.Summary.MissingReasons[ReasonInvalidValue] != 1 {
		t.Fatalf("summary missing reasons = %v", artifact.Summary.MissingReasons)
	}

	hs := summaryHorizon(t, artifact.Summary, 1)
	if hs.LabelDates != 1 || hs.MissingLabelDates != 2 || hs.Pairs != 4 {
		t.Fatalf("horizon summary label dates/pairs = %d/%d/%d, want 1/2/4", hs.LabelDates, hs.MissingLabelDates, hs.Pairs)
	}
	if hs.IC.N != 1 {
		t.Fatalf("IC n = %d, want 1", hs.IC.N)
	}
	requireValue(t, hs.IC.Mean, 1)
	requireReason(t, hs.IC.Std, ReasonInsufficientSamples)
	requireReason(t, hs.IC.IR, ReasonInsufficientSamples)
	requireValue(t, hs.IC.PositiveRate, 1)
	requireValue(t, hs.Correlation.Mean, 1) // pearson method selects the IC series
	if len(hs.Buckets) != 2 {
		t.Fatalf("summary buckets = %d, want 2", len(hs.Buckets))
	}
	if hs.Buckets[0].Dates != 1 {
		t.Fatalf("summary bucket dates = %d, want 1", hs.Buckets[0].Dates)
	}
	requireValue(t, hs.Buckets[0].Mean, 0.15)
	requireValue(t, hs.Buckets[1].Mean, 0.35)
	requireValue(t, hs.Spread.Mean, 0.2)
}

// TestRunNullReasons covers the D6 null+reason contract: zero
// denominators (zero variance), insufficient samples, and grouping that
// is not applicable.
func TestRunNullReasons(t *testing.T) {
	engine := newTestEngine(t)
	cfg := baseConfig()
	cfg.Range = domain.Interval{From: d1, To: d5}
	cfg.Groups = 3
	req := Request{
		Config: cfg,
		Series: []CrossSection{
			// Constant labels: correlation undefined, buckets still meaningful.
			{Time: d1, Frame: frame(d1, map[string]string{"a": "1", "b": "2", "c": "3", "d": "4"}, nil)},
			// Constant factor: ties order buckets by instrument id.
			{Time: d2, Frame: frame(d2, map[string]string{"a": "1", "b": "1", "c": "1", "d": "1"}, nil)},
			// Pairs at MinSamples but below Groups: correlations live, no buckets.
			{Time: d3, Frame: frame(d3, map[string]string{"a": "5", "b": "6"}, nil)},
			// Pairs below MinSamples.
			{Time: d4, Frame: frame(d4, map[string]string{"a": "7"}, nil)},
		},
		Labels: map[int]LabelSet{
			1: labelSet(1, map[time.Time]map[string]float64{
				d1: {"a": 0.5, "b": 0.5, "c": 0.5, "d": 0.5},
				d2: {"a": 1, "b": 2, "c": 3, "d": 4},
				d3: {"a": 0.1, "b": 0.9},
				d4: {"a": 0.3},
			}),
		},
	}
	artifact, err := engine.Run(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	block := horizonOf(t, artifact.Series[0], 1)
	requireReason(t, block.IC, ReasonZeroVariance)
	requireReason(t, block.RankIC, ReasonZeroVariance)
	if len(block.Buckets) != 3 {
		t.Fatalf("constant labels: buckets = %d, want 3", len(block.Buckets))
	}
	for _, b := range block.Buckets {
		requireValue(t, b.Mean, 0.5)
	}
	requireValue(t, block.Spread, 0)

	block = horizonOf(t, artifact.Series[1], 1)
	requireReason(t, block.IC, ReasonZeroVariance)
	requireReason(t, block.RankIC, ReasonZeroVariance)
	if block.Buckets[0].Size != 2 || block.Buckets[1].Size != 1 || block.Buckets[2].Size != 1 {
		t.Fatalf("tie bucket sizes = %d %d %d, want 2 1 1", block.Buckets[0].Size, block.Buckets[1].Size, block.Buckets[2].Size)
	}
	requireValue(t, block.Buckets[0].Mean, 1.5) // {a, b} by (value, id) order
	requireValue(t, block.Buckets[1].Mean, 3)   // {c}
	requireValue(t, block.Buckets[2].Mean, 4)   // {d}
	requireValue(t, block.Spread, 2.5)

	block = horizonOf(t, artifact.Series[2], 1)
	requireValue(t, block.IC, 1)
	if block.Buckets != nil {
		t.Fatalf("pairs below groups: buckets = %+v, want none", block.Buckets)
	}
	requireReason(t, block.Spread, ReasonNotApplicable)

	block = horizonOf(t, artifact.Series[3], 1)
	requireReason(t, block.IC, ReasonInsufficientSamples)
	requireReason(t, block.RankIC, ReasonInsufficientSamples)
	requireReason(t, block.Spread, ReasonNotApplicable)
}

// TestRunSeriesSummaryReasons covers the summary-level nulls: a
// constant IC series has zero std and no IR (zero denominator); a
// horizon with no label dates reports everything null.
func TestRunSeriesSummaryReasons(t *testing.T) {
	engine := newTestEngine(t)
	cfg := baseConfig()
	cfg.Range = domain.Interval{From: d1, To: d5}
	cfg.Method = MethodSpearman
	req := Request{
		Config: cfg,
		Series: []CrossSection{
			{Time: d1, Frame: frame(d1, map[string]string{"a": "1", "b": "2"}, nil)},
			{Time: d2, Frame: frame(d2, map[string]string{"a": "2", "b": "4"}, nil)},
			{Time: d3, Frame: frame(d3, map[string]string{"a": "3", "b": "6"}, nil)},
		},
		Labels: map[int]LabelSet{
			// Monotone pairs over binary-exact values: IC and Rank IC are
			// exactly +1 every day, so the IC series is strictly constant —
			// std 0, IR zero_variance.
			1: labelSet(1, map[time.Time]map[string]float64{
				d1: {"a": 0.5, "b": 1.0},
				d2: {"a": 1.0, "b": 2.0},
				d3: {"a": 1.5, "b": 3.0},
			}),
			// Horizon 5 exists but carries no label dates: every day is
			// missing_labels and the series summary is null.
			5: {Horizon: 5, Returns: map[time.Time]map[domain.ID]float64{}},
		},
	}
	artifact, err := engine.Run(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	if len(artifact.Summary.Horizons) != 2 {
		t.Fatalf("horizons = %d, want 2", len(artifact.Summary.Horizons))
	}
	if artifact.Summary.Horizons[0].Horizon != 1 || artifact.Summary.Horizons[1].Horizon != 5 {
		t.Fatalf("horizons not ascending: %d then %d", artifact.Summary.Horizons[0].Horizon, artifact.Summary.Horizons[1].Horizon)
	}

	hs := summaryHorizon(t, artifact.Summary, 1)
	if hs.IC.N != 3 {
		t.Fatalf("IC n = %d, want 3", hs.IC.N)
	}
	requireValue(t, hs.IC.Mean, 1)
	requireValue(t, hs.IC.Std, 0)
	requireReason(t, hs.IC.IR, ReasonZeroVariance)
	requireValue(t, hs.IC.PositiveRate, 1)
	// Spearman method selects the Rank IC series for the headline.
	requireValue(t, hs.Correlation.Mean, 1)

	empty := summaryHorizon(t, artifact.Summary, 5)
	if empty.LabelDates != 0 || empty.MissingLabelDates != 3 {
		t.Fatalf("empty horizon label dates = %d/%d, want 0/3", empty.LabelDates, empty.MissingLabelDates)
	}
	requireReason(t, empty.IC.Mean, ReasonInsufficientSamples)
	requireReason(t, empty.RankIC.Mean, ReasonInsufficientSamples)
	requireReason(t, empty.Correlation.Mean, ReasonInsufficientSamples)
}

// TestRunSegments covers the train/validation/test accounting: per-date
// segment tags, per-segment dates/samples/label counts, and uncovered
// dates reported rather than hidden.
func TestRunSegments(t *testing.T) {
	engine := newTestEngine(t)
	cfg := baseConfig()
	cfg.Range = domain.Interval{From: d1, To: d5}
	cfg.Segments = []Segment{
		{Kind: SegmentTrain, Range: domain.Interval{From: d1, To: d3}},
		{Kind: SegmentTest, Range: domain.Interval{From: d3, To: d4}},
	}
	series := make([]CrossSection, 0, 4)
	days := map[time.Time]map[string]float64{}
	for _, d := range []time.Time{d1, d2, d3, d4} {
		series = append(series, CrossSection{Time: d, Frame: frame(d, map[string]string{"a": "1", "b": "2"}, nil)})
		days[d] = map[string]float64{"a": 0.1, "b": 0.2}
	}
	req := Request{Config: cfg, Series: series, Labels: map[int]LabelSet{1: labelSet(1, days)}}
	artifact, err := engine.Run(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}

	// d1, d2 train; d3 test; d4 falls into no segment.
	wantSegments := []string{"train", "train", "test", ""}
	for i, want := range wantSegments {
		if artifact.Series[i].Segment != want {
			t.Fatalf("series[%d].segment = %q, want %q", i, artifact.Series[i].Segment, want)
		}
	}

	if len(artifact.Summary.Segments) != 2 {
		t.Fatalf("segments = %d, want 2", len(artifact.Summary.Segments))
	}
	train := artifact.Summary.Segments[0]
	if train.Kind != "train" || train.Dates != 2 || train.Samples != 4 {
		t.Fatalf("train summary = %+v", train)
	}
	if len(train.Horizons) != 1 || train.Horizons[0].Horizon != 1 || train.Horizons[0].Labels != 4 {
		t.Fatalf("train horizon labels = %+v, want horizon 1 with 4 labels", train.Horizons)
	}
	test := artifact.Summary.Segments[1]
	if test.Kind != "test" || test.Dates != 1 || test.Samples != 2 || test.Horizons[0].Labels != 2 {
		t.Fatalf("test summary = %+v", test)
	}
	if len(artifact.Summary.UncoveredDates) != 1 || !artifact.Summary.UncoveredDates[0].Equal(d4) {
		t.Fatalf("uncovered dates = %v, want [d4]", artifact.Summary.UncoveredDates)
	}
	if len(artifact.Config.Segments) != 2 {
		t.Fatalf("artifact config segments = %d, want 2", len(artifact.Config.Segments))
	}
}

// TestRunDeterminism: equal inputs produce byte-identical artifacts
// (input order aside), and tampering breaks the checksum.
func TestRunDeterminism(t *testing.T) {
	engine := newTestEngine(t)
	build := func() Request {
		return Request{
			Config: baseConfig(),
			Series: []CrossSection{
				{Time: d1, Frame: frame(d1, map[string]string{"a": "3", "b": "1", "c": "2"}, nil)},
				{Time: d2, Frame: frame(d2, map[string]string{"a": "2", "b": "5", "c": "4"}, nil)},
			},
			Labels: map[int]LabelSet{
				1: labelSet(1, map[time.Time]map[string]float64{
					d1: {"a": 0.3, "b": 0.1, "c": 0.2},
					d2: {"a": 0.2, "b": 0.5, "c": 0.4},
				}),
			},
		}
	}
	first, err := engine.Run(build())
	if err != nil {
		t.Fatalf("first run: %v", err)
	}
	// Reverse the series order: sorting inside Run must neutralize it.
	reversed := build()
	reversed.Series = []CrossSection{reversed.Series[1], reversed.Series[0]}
	second, err := engine.Run(reversed)
	if err != nil {
		t.Fatalf("second run: %v", err)
	}
	firstJSON, err := json.Marshal(first)
	if err != nil {
		t.Fatal(err)
	}
	secondJSON, err := json.Marshal(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(firstJSON) != string(secondJSON) {
		t.Fatal("equal inputs must produce byte-identical artifacts")
	}
	ok, err := first.VerifyChecksum(ports.SHA256Checksummer{})
	if err != nil || !ok {
		t.Fatalf("verify checksum = %v %v, want true", ok, err)
	}

	first.Summary.Dates = 99
	ok, err = first.VerifyChecksum(ports.SHA256Checksummer{})
	if err != nil || ok {
		t.Fatalf("verify after tamper = %v %v, want false", ok, err)
	}
}

// TestRunJSONSafety guards requirements §6.5 end to end: no NaN or
// Infinity may appear in the artifact JSON, null values carry reasons,
// and the evidence note and schema version ride along.
func TestRunJSONSafety(t *testing.T) {
	engine := newTestEngine(t)
	req := Request{
		Config: baseConfig(),
		Series: []CrossSection{
			{Time: d1, Frame: frame(d1, map[string]string{"a": "1", "b": "2"}, nil)},
			{Time: d2, Frame: frame(d2, map[string]string{"a": "1", "b": "2"},
				map[string]string{"c": factor.ReasonInsufficientHistory})},
		},
		Labels: map[int]LabelSet{
			1: labelSet(1, map[time.Time]map[string]float64{
				d1: {"a": 0.1, "b": 0.2},
			}),
		},
	}
	artifact, err := engine.Run(req)
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	encoded, err := json.Marshal(artifact)
	if err != nil {
		t.Fatalf("marshal: %v (NaN/Inf must never reach JSON)", err)
	}
	text := string(encoded)
	for _, banned := range []string{"NaN", "Infinity"} {
		if strings.Contains(text, banned) {
			t.Fatalf("artifact JSON contains %q", banned)
		}
	}
	if !strings.Contains(text, `"value":null`) {
		t.Fatal("artifact JSON should carry null values")
	}
	if !strings.Contains(text, `"reason":"`) {
		t.Fatal("artifact JSON should carry reasons next to null values")
	}
	if !strings.Contains(text, EvidenceNote) {
		t.Fatal("artifact must carry the evidence note")
	}
	if !strings.Contains(text, `"schema_version":"analysis-artifact/1"`) {
		t.Fatal("artifact must carry its schema version")
	}
}

// TestStatJSON pins the Stat wire shape: value null + reason, and a
// plain number when present.
func TestStatJSON(t *testing.T) {
	encoded, err := json.Marshal(nullStat(ReasonZeroVariance))
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"value":null,"reason":"zero_variance"}` {
		t.Fatalf("null stat JSON = %s", encoded)
	}
	v := 1.5
	encoded, err = json.Marshal(Stat{Value: &v})
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != `{"value":1.5}` {
		t.Fatalf("value stat JSON = %s", encoded)
	}
}

// TestRunValidationErrors walks the fail-closed request contract: one
// malformed field, one precise error.
func TestRunValidationErrors(t *testing.T) {
	engine := newTestEngine(t)
	cases := []struct {
		name     string
		mutate   func(req *Request)
		code     string
		contains string
	}{
		{"empty label price", func(r *Request) { r.Config.LabelPrice = "" }, codeRequestInvalid, "label price"},
		{"unknown method", func(r *Request) { r.Config.Method = "kendall" }, codeRequestInvalid, "method"},
		{"groups too small", func(r *Request) { r.Config.Groups = 1 }, codeRequestInvalid, "groups"},
		{"groups too large", func(r *Request) { r.Config.Groups = 11 }, codeRequestInvalid, "groups"},
		{"min samples too small", func(r *Request) { r.Config.MinSamples = 1 }, codeRequestInvalid, "min samples"},
		{"inverted range", func(r *Request) { r.Config.Range = domain.Interval{From: d3, To: d1} }, codeRequestInvalid, "range"},
		{
			"segments out of order",
			func(r *Request) {
				r.Config.Segments = []Segment{
					{Kind: SegmentTest, Range: domain.Interval{From: d1, To: d2}},
					{Kind: SegmentTrain, Range: domain.Interval{From: d2, To: d3}},
				}
			},
			codeRequestInvalid, "train, validation, test",
		},
		{
			"segments overlap",
			func(r *Request) {
				r.Config.Segments = []Segment{
					{Kind: SegmentTrain, Range: domain.Interval{From: d1, To: d3}},
					{Kind: SegmentValidation, Range: domain.Interval{From: d2, To: d3}},
				}
			},
			codeRequestInvalid, "overlap",
		},
		{
			"segment outside range",
			func(r *Request) {
				r.Config.Segments = []Segment{{Kind: SegmentTrain, Range: domain.Interval{From: d0, To: d2}}}
			},
			codeRequestInvalid, "inside the analysis range",
		},
		{
			"segment kind invalid",
			func(r *Request) {
				r.Config.Segments = []Segment{{Kind: SegmentKind("warmup"), Range: domain.Interval{From: d1, To: d2}}}
			},
			codeRequestInvalid, "train, validation or test",
		},
		{"factor params rejected", func(r *Request) { r.Config.Params = map[string]any{"window": "soon"} }, "factor.request_invalid", "params"},
		{"factor not registered", func(r *Request) { r.Config.Ref = factor.FactorRef{ID: "ghost", Version: "v1"} }, "factor.not_registered", "not registered"},
		{"empty series", func(r *Request) { r.Series = nil }, codeSeriesInvalid, "cross-section"},
		{"nil frame", func(r *Request) { r.Series[1].Frame = nil }, codeSeriesInvalid, "nil frame"},
		{"frame ref mismatch", func(r *Request) { r.Series[1].Frame.Ref = factor.FactorRef{ID: "other", Version: "v1"} }, codeSeriesInvalid, "does not match"},
		{"as_of mismatch", func(r *Request) { r.Series[1].Time = d2.Add(time.Hour) }, codeSeriesInvalid, "as_of"},
		{
			"duplicate decision time",
			func(r *Request) {
				r.Series[2] = CrossSection{Time: d1, Frame: frame(d1, map[string]string{"a": "1"}, nil)}
			},
			codeSeriesInvalid, "duplicate",
		},
		{
			"decision time outside range",
			func(r *Request) {
				r.Series[2] = CrossSection{Time: d9, Frame: frame(d9, map[string]string{"a": "1"}, nil)}
			},
			codeSeriesInvalid, "outside the analysis range",
		},
		{
			"member in both values and missing",
			func(r *Request) {
				r.Series[1].Frame = frame(d2, map[string]string{"a": "1"}, map[string]string{"a": factor.ReasonMissingInput})
			},
			codeSeriesInvalid, "both values and missing",
		},
		{"no labels", func(r *Request) { r.Labels = nil }, codeLabelsInvalid, "horizon"},
		{"horizon key mismatch", func(r *Request) {
			set := r.Labels[1]
			set.Horizon = 4
			r.Labels[1] = set
		}, codeLabelsInvalid, "carries horizon"},
		{"horizon not positive", func(r *Request) { r.Labels[-1] = LabelSet{Horizon: -1} }, codeLabelsInvalid, "positive"},
		{
			"label date outside range",
			func(r *Request) { r.Labels[1].Returns[d9] = map[domain.ID]float64{"a": 0.1} },
			codeLabelsInvalid, "outside the analysis range",
		},
		{
			"label value not finite",
			func(r *Request) { r.Labels[1].Returns[d1]["a"] = math.NaN() },
			codeLabelsInvalid, "not finite",
		},
		{
			// Two label keys denoting the same instant in different
			// zones would silently collide after UTC normalization.
			"label date collision",
			func(r *Request) {
				shifted := d1.In(time.FixedZone("shift", 8*3600))
				r.Labels[1].Returns[shifted] = map[domain.ID]float64{"a": 0.2}
			},
			codeLabelsInvalid, "same instant",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := baseRequest()
			tc.mutate(&req)
			_, err := engine.Run(req)
			assertErr(t, err, tc.code, tc.contains)
		})
	}
}

func TestQuantile(t *testing.T) {
	sorted := []float64{1, 2, 3, 4}
	cases := []struct {
		q    float64
		want float64
	}{
		{0, 1}, {0.05, 1.15}, {0.25, 1.75}, {0.5, 2.5}, {0.75, 3.25}, {0.95, 3.85}, {1, 4},
	}
	for _, c := range cases {
		if got := quantile(sorted, c.q); math.Abs(got-c.want) > 1e-12 {
			t.Fatalf("quantile(%v, %v) = %v, want %v", sorted, c.q, got, c.want)
		}
	}
	if got := quantile([]float64{7}, 0.5); got != 7 {
		t.Fatalf("single-point quantile = %v, want 7", got)
	}
}

func TestSpearmanTies(t *testing.T) {
	xs := []float64{1, 1, 2}
	ys := []float64{1, 2, 2}
	rankX := averageRanks(xs)
	if rankX[0] != 1.5 || rankX[1] != 1.5 || rankX[2] != 3 {
		t.Fatalf("average ranks = %v, want [1.5 1.5 3]", rankX)
	}
	r, ok := spearman(xs, ys)
	if !ok || math.Abs(r-0.5) > 1e-12 {
		t.Fatalf("spearman = %v %v, want 0.5", r, ok)
	}
	// Fully tied input: rank variance is zero, a zero denominator.
	if _, ok := spearman([]float64{2, 2, 2}, ys); ok {
		t.Fatal("constant factor must yield ok=false")
	}
}

func TestPearsonExtremes(t *testing.T) {
	if r, ok := pearson([]float64{1, 2, 3}, []float64{3, 2, 1}); !ok || r != -1 {
		t.Fatalf("anti-correlated pearson = %v %v, want -1", r, ok)
	}
	if r, ok := pearson([]float64{1, 2, 3}, []float64{2, 4, 6}); !ok || r != 1 {
		t.Fatalf("correlated pearson = %v %v, want 1", r, ok)
	}
	if _, ok := pearson([]float64{1, 1, 1}, []float64{1, 2, 3}); ok {
		t.Fatal("constant factor must yield ok=false")
	}
}
