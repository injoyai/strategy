package analysis

import (
	"github.com/injoyai/strategy/internal/domain"
	"github.com/injoyai/strategy/internal/factor"
)

// Correlation methods for the summary's Correlation aggregate.
// requirements.md §7.1 fixes the definitions — IC is the Pearson
// correlation with subsequent returns, Rank IC the rank (Spearman)
// correlation — so both are always fully reported per date; Method only
// selects which daily series the summary's Correlation aggregates.
const (
	MethodPearson  = "pearson"
	MethodSpearman = "spearman"
)

// SegmentKind labels the train/validation/test split. requirements.md
// §7.1: segments are ordered in time and never overlap, and any fitting
// reads the train segment only. This milestone computes reporting
// statistics and fits nothing — the segment accounting exists so a later
// fitting stage can bind exclusively to the train segment.
type SegmentKind string

const (
	SegmentTrain      SegmentKind = "train"
	SegmentValidation SegmentKind = "validation"
	SegmentTest       SegmentKind = "test"
)

// segmentRank keeps kinds in canonical order train < validation < test.
var segmentRank = map[SegmentKind]int{
	SegmentTrain:      0,
	SegmentValidation: 1,
	SegmentTest:       2,
}

// Segment is one contiguous split of the analysis range.
type Segment struct {
	Kind  SegmentKind     `json:"kind"`
	Range domain.Interval `json:"range"`
}

// Config is the fixed analysis configuration D6 requires: the label
// economics, the grouping rule, the minimum sample and the correlation
// method. The label economics fields are evidence of how the caller
// built labels — the label computation itself stays with the caller per
// the §6.2 isolation rule; analysis only consumes finished LabelSets.
type Config struct {
	Ref    factor.FactorRef
	Params map[string]any

	// Label economics: free-form strings fixed per run and echoed
	// verbatim into the artifact, e.g. LabelPrice "close", LabelEntry
	// "decision_close", LabelCost "total_return_gross".
	LabelPrice string
	LabelEntry string
	LabelCost  string

	Method     string // MethodPearson or MethodSpearman
	Groups     int    // quantile buckets, 2..10; bucket 1 = lowest values
	MinSamples int    // minimum paired samples for a daily correlation
	Range      domain.Interval
	Segments   []Segment
}

// validateSegments checks the segment contract: each range is a valid
// interval inside the analysis window, ranges are ordered and
// non-overlapping, and kinds follow the canonical train → validation →
// test order without repeats. Subsets are legitimate (train + test
// without validation is a valid split); gaps between segments are not
// errors — the summary reports their dates as uncovered.
func validateSegments(segments []Segment, window domain.Interval) ([]Segment, error) {
	out := make([]Segment, 0, len(segments))
	prevTo := window.From
	prevRank := -1
	for _, seg := range segments {
		rank, ok := segmentRank[seg.Kind]
		if !ok {
			return nil, domain.NewError(codeRequestInvalid, "analysis: segment kind %q must be train, validation or test", seg.Kind)
		}
		if rank <= prevRank {
			return nil, domain.NewError(codeRequestInvalid, "analysis: segment kinds must be unique and follow the train, validation, test order")
		}
		interval, err := domain.NewInterval(seg.Range.From, seg.Range.To)
		if err != nil {
			return nil, domain.Wrap(err, codeRequestInvalid, "analysis: segment %s range invalid", seg.Kind)
		}
		if interval.From.Before(window.From) || interval.To.After(window.To) {
			return nil, domain.NewError(codeRequestInvalid, "analysis: segment %s range must lie inside the analysis range", seg.Kind)
		}
		if interval.From.Before(prevTo) {
			return nil, domain.NewError(codeRequestInvalid, "analysis: segment %s overlaps or precedes the previous segment", seg.Kind)
		}
		out = append(out, Segment{Kind: seg.Kind, Range: interval})
		prevTo, prevRank = interval.To, rank
	}
	return out, nil
}
