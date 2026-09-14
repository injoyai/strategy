package analysis

import (
	"math"
	"sort"

	"github.com/injoyai/strategy/internal/domain"
)

// mean returns the arithmetic mean; the caller guarantees len > 0.
func mean(xs []float64) float64 {
	sum := 0.0
	for _, x := range xs {
		sum += x
	}
	return sum / float64(len(xs))
}

// sampleStd is the n-1 standard deviation. ok=false when fewer than
// two samples — the statistic is undefined, not zero.
func sampleStd(xs []float64) (float64, bool) {
	if len(xs) < 2 {
		return 0, false
	}
	m := mean(xs)
	sumSq := 0.0
	for _, x := range xs {
		d := x - m
		sumSq += d * d
	}
	return math.Sqrt(sumSq / float64(len(xs)-1)), true
}

// quantile is the linear-interpolation quantile over sorted values (the
// numpy "linear" method): position q*(n-1), interpolated between
// neighbours. The caller guarantees len > 0 and 0 <= q <= 1.
func quantile(sorted []float64, q float64) float64 {
	if len(sorted) == 1 {
		return sorted[0]
	}
	pos := q * float64(len(sorted)-1)
	lo := int(math.Floor(pos))
	hi := int(math.Ceil(pos))
	if lo == hi {
		return sorted[lo]
	}
	return sorted[lo] + (sorted[hi]-sorted[lo])*(pos-float64(lo))
}

// pearson is the product-moment correlation. ok=false when either side
// has zero variance — the zero denominator D6 requires to surface as
// null + reason, not as 0.
func pearson(xs, ys []float64) (float64, bool) {
	mx, my := mean(xs), mean(ys)
	var sxy, sxx, syy float64
	for i := range xs {
		dx, dy := xs[i]-mx, ys[i]-my
		sxy += dx * dy
		sxx += dx * dx
		syy += dy * dy
	}
	if sxx == 0 || syy == 0 {
		return 0, false
	}
	return sxy / math.Sqrt(sxx*syy), true
}

// averageRanks maps values to 1-based ranks with ties sharing the mean
// of the covered rank positions — the standard Spearman tie handling.
func averageRanks(xs []float64) []float64 {
	order := make([]int, len(xs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool { return xs[order[a]] < xs[order[b]] })
	ranks := make([]float64, len(xs))
	i := 0
	for i < len(xs) {
		j := i
		for j+1 < len(xs) && xs[order[j+1]] == xs[order[i]] {
			j++
		}
		avg := float64(i+j+2) / 2 // mean of ranks i+1 .. j+1
		for k := i; k <= j; k++ {
			ranks[order[k]] = avg
		}
		i = j + 1
	}
	return ranks
}

// spearman is the Pearson correlation of the average ranks.
func spearman(xs, ys []float64) (float64, bool) {
	return pearson(averageRanks(xs), averageRanks(ys))
}

// pair is one member's usable factor value joined with its label return.
type pair struct {
	id    domain.ID
	value float64
	ret   float64
}

// newDistribution summarizes values. values may arrive in any order; a
// sorted copy drives the quantiles so the result never depends on input
// order.
func newDistribution(values []float64) Distribution {
	d := Distribution{Count: len(values)}
	if len(values) == 0 {
		reason := ReasonInsufficientSamples
		d.Mean, d.Std, d.Min, d.Max = nullStat(reason), nullStat(reason), nullStat(reason), nullStat(reason)
		d.Q05, d.Q25, d.Q50 = nullStat(reason), nullStat(reason), nullStat(reason)
		d.Q75, d.Q95 = nullStat(reason), nullStat(reason)
		return d
	}
	sorted := append([]float64(nil), values...)
	sort.Float64s(sorted)
	d.Mean = valueStat(mean(sorted))
	if std, ok := sampleStd(sorted); ok {
		d.Std = valueStat(std)
	} else {
		d.Std = nullStat(ReasonInsufficientSamples)
	}
	d.Min = valueStat(sorted[0])
	d.Max = valueStat(sorted[len(sorted)-1])
	d.Q05 = valueStat(quantile(sorted, 0.05))
	d.Q25 = valueStat(quantile(sorted, 0.25))
	d.Q50 = valueStat(quantile(sorted, 0.50))
	d.Q75 = valueStat(quantile(sorted, 0.75))
	d.Q95 = valueStat(quantile(sorted, 0.95))
	return d
}

// newSeriesSummary aggregates the computable daily values. Reasons:
// n == 0 -> insufficient_samples everywhere; n == 1 -> std and IR are
// insufficient_samples (undefined); a constant series (std == 0) ->
// IR zero_variance (the zero denominator D6 calls out).
func newSeriesSummary(values []float64) SeriesSummary {
	s := SeriesSummary{N: len(values)}
	if len(values) == 0 {
		s.Mean = nullStat(ReasonInsufficientSamples)
		s.Std = nullStat(ReasonInsufficientSamples)
		s.IR = nullStat(ReasonInsufficientSamples)
		s.PositiveRate = nullStat(ReasonInsufficientSamples)
		return s
	}
	m := mean(values)
	s.Mean = valueStat(m)
	positive := 0
	for _, v := range values {
		if v > 0 {
			positive++
		}
	}
	s.PositiveRate = valueStat(float64(positive) / float64(len(values)))
	if std, ok := sampleStd(values); ok {
		s.Std = valueStat(std)
		if std == 0 {
			s.IR = nullStat(ReasonZeroVariance)
		} else {
			s.IR = valueStat(m / std)
		}
	} else {
		s.Std = nullStat(ReasonInsufficientSamples)
		s.IR = nullStat(ReasonInsufficientSamples)
	}
	return s
}

// bucketize splits the pairs — sorted by (factor value asc, instrument
// id asc), the deterministic grouping rule — into groups contiguous
// buckets. With n = q*G + r the first r buckets take q+1 members so
// sizes differ by at most one; bucket 1 holds the lowest values.
// Returns the per-bucket stats and the high-minus-low spread (bucket
// Groups minus bucket 1); the caller guarantees n >= groups.
func bucketize(pairs []pair, groups int) ([]BucketStats, Stat) {
	order := make([]int, len(pairs))
	for i := range order {
		order[i] = i
	}
	sort.Slice(order, func(a, b int) bool {
		if pairs[order[a]].value != pairs[order[b]].value {
			return pairs[order[a]].value < pairs[order[b]].value
		}
		return pairs[order[a]].id < pairs[order[b]].id
	})
	out := make([]BucketStats, 0, groups)
	base, extra := len(pairs)/groups, len(pairs)%groups
	pos := 0
	firstMean, lastMean := 0.0, 0.0
	for b := 1; b <= groups; b++ {
		size := base
		if b <= extra {
			size++
		}
		returns := make([]float64, 0, size)
		for k := 0; k < size; k++ {
			returns = append(returns, pairs[order[pos+k]].ret)
		}
		m := mean(returns)
		out = append(out, BucketStats{Bucket: b, Size: size, Mean: valueStat(m)})
		if b == 1 {
			firstMean = m
		}
		if b == groups {
			lastMean = m
		}
		pos += size
	}
	return out, valueStat(lastMean - firstMean)
}
