package screening

import (
	"math/big"
	"strings"

	"github.com/injoyai/strategy/internal/domain"
)

type Scoring struct {
	Places   int32
	Rounding domain.RoundingMode
}

func DefaultScoring() Scoring {
	return Scoring{Places: 6, Rounding: domain.RoundHalfAwayFromZero}
}

type ScoreEvidence struct {
	BindingID    domain.ID      `json:"binding_id"`
	RawValue     domain.Value   `json:"raw_value"`
	Percentile   domain.Decimal `json:"percentile"`
	Weight       domain.Decimal `json:"weight"`
	Contribution domain.Decimal `json:"contribution"`
}

type rankRow struct {
	instrument domain.ID
	sortKeys   []parsedValue
	compValues []parsedValue
	compRaws   []domain.Value
	score      domain.Decimal
	hasScore   bool
	evidence   []ScoreEvidence
}

// compareRows orders rankable instruments by the declared sort fields with
// direction, appending instrument_id ascending as the stable tie-breaker.
func compareRows(a, b rankRow, fields []RankField) int {
	for i, f := range fields {
		cmp, err := compareParsed(a.sortKeys[i], b.sortKeys[i])
		if err != nil {
			continue
		}
		if cmp != 0 {
			if f.Direction == DirectionDesc {
				return -cmp
			}
			return cmp
		}
	}
	return strings.Compare(a.instrument.String(), b.instrument.String())
}

// scoreRows computes weighted percentile scores for all rankable candidates.
// Candidate component values and raw values are stored on compValues/compRaws
// in the same order as components. Percentiles use integer arithmetic with
// round-half-up, scaled by 10^Places; larger_is_better uses the percentile,
// smaller_is_better its complement. Every candidate contributes its weighted
// share; missing components were already excluded upstream so weights are
// never redistributed here.
func scoreRows(candidates []rankRow, components []ScoreComponent, scoring Scoring) error {
	n := len(candidates)
	for i := range candidates {
		candidates[i].score = domain.Decimal("0")
		candidates[i].hasScore = false
		candidates[i].evidence = nil
	}
	for ci, comp := range components {
		order := make([]int, n)
		for i := range order {
			order[i] = i
		}
		sortStable(order, func(i, j int) bool {
			return lessRanked(candidates[order[i]], candidates[order[j]], ci)
		})
		places := big.NewInt(10).Exp(big.NewInt(10), big.NewInt(int64(scoring.Places)), nil)
		i := 0
		for i < n {
			j := i + 1
			for j < n && eqParsed(candidates[order[j]].compValues[ci], candidates[order[i]].compValues[ci]) {
				j++
			}
			var pScaled *big.Int
			if n <= 1 {
				pScaled = new(big.Int).Rsh(places, 1)
			} else {
				pScaled = percentileScaled(int64(i+1), int64(j), int64(n), places)
			}
			pDec, err := scaledToDecimal(pScaled, scoring.Places)
			if err != nil {
				return err
			}
			var directed domain.Decimal
			if comp.Direction == ScoreSmallerBetter {
				inv, err := oneDecimal().Sub(pDec)
				if err != nil {
					return err
				}
				directed = inv
			} else {
				directed = pDec
			}
			weight := comp.Weight
			contribution, err := weight.Mul(directed)
			if err != nil {
				return err
			}
			for idx := i; idx < j; idx++ {
				r := order[idx]
				candidates[r].score, err = candidates[r].score.Add(contribution)
				if err != nil {
					return err
				}
				candidates[r].hasScore = true
				candidates[r].evidence = append(candidates[r].evidence, ScoreEvidence{
					BindingID:    components[ci].Input.BindingID,
					RawValue:     candidates[r].compRaws[ci],
					Percentile:   pDec,
					Weight:       weight,
					Contribution: contribution,
				})
			}
			i = j
		}
	}
	return nil
}

func lessRanked(a, b rankRow, ci int) bool {
	cmp, err := compareParsed(a.compValues[ci], b.compValues[ci])
	if err != nil {
		return false
	}
	if cmp != 0 {
		return cmp < 0
	}
	return strings.Compare(a.instrument.String(), b.instrument.String()) < 0
}

func eqParsed(a, b parsedValue) bool {
	cmp, err := compareParsed(a, b)
	return err == nil && cmp == 0
}

func sortStable(slice []int, less func(i, j int) bool) {
	// insertion sort is stable and simple; candidate counts are engine-bounded
	n := len(slice)
	for i := 1; i < n; i++ {
		for j := i; j > 0 && less(j, j-1); j-- {
			slice[j], slice[j-1] = slice[j-1], slice[j]
		}
	}
}

func oneDecimal() domain.Decimal { return domain.Decimal("1") }

// percentileScaled returns the round-half-up average-rank percentile scaled
// by 10^Places. first/last are 1-based group bounds over m total candidates.
// With m <= 1 the average rank is 1, giving p = 0.5 scaled.
func percentileScaled(first, last, m int64, places *big.Int) *big.Int {
	if m <= 1 {
		return new(big.Int).Rsh(places, 1)
	}
	n := new(big.Int).SetInt64(first + last - 2) // (first+last)/2 - 1 scaled
	n = n.Mul(n, places)
	d := 2 * (m - 1)
	shift := new(big.Int).Rsh(big.NewInt(d), 1)
	q := new(big.Int).Add(n, shift)
	q = q.Div(q, big.NewInt(d))
	return q
}

// scaledToDecimal converts an integer scaled by 10^Places into a Decimal in
// [0, 1] with Places fractional digits.
func scaledToDecimal(scaled *big.Int, places int32) (domain.Decimal, error) {
	s := scaled.String()
	if places <= 0 {
		return domain.ParseDecimal(s)
	}
	if int32(len(s)) <= places {
		s = strings.Repeat("0", int(places)+1-len(s)) + s
	}
	dot := int32(len(s)) - places
	out := s[:dot] + "." + s[dot:]
	return domain.ParseDecimal(out)
}
