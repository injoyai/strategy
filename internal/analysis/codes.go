package analysis

// Error codes surfaced as domain.Error codes, mirroring the factor.*
// prefixing style. Stable on the wire; append only.
const (
	codeRequestInvalid = "analysis.request_invalid"
	codeSeriesInvalid  = "analysis.series_invalid"
	codeLabelsInvalid  = "analysis.labels_invalid"
)

// Null reasons carried by Stat when a metric cannot be computed
// (m1-data-factor.md §7): zero denominators, insufficient samples and
// not-applicable situations return null + reason, never a silent zero.
// insufficient_samples also covers n < 2 cases where a statistic is
// undefined; missing_labels marks dates whose label series is absent;
// invalid_value guards the no-NaN-in-JSON rule (requirements §6.5).
const (
	ReasonInsufficientSamples = "insufficient_samples"
	ReasonZeroVariance        = "zero_variance"
	ReasonNotApplicable       = "not_applicable"
	ReasonMissingLabels       = "missing_labels"
	ReasonInvalidValue        = "invalid_value"
)

// EvidenceNote is the fixed disclaimer D6 requires next to IC and
// quantile group returns: they are factor evidence, not a tradable
// backtest. Every artifact carries it.
const EvidenceNote = "IC and quantile group returns are factor evidence, not equivalent to a tradable backtest."
