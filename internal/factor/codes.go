package factor

// Error codes returned as domain.Error codes (screening-style package
// prefix). Stable on the wire; append only.
const (
	codeSpecInvalid     = "factor.spec_invalid"
	codeDuplicateFactor = "factor.duplicate"
	codeNotRegistered   = "factor.not_registered"
	codeRequestInvalid  = "factor.request_invalid"
	codePreflightFailed = "factor.preflight_failed"
	codeCacheState      = "factor.cache_state"
	codeComputeFailed   = "factor.compute_failed"
)

// Problem codes reported by registration graph validation and Preflight.
// Problems are findings, not errors: preflight returns every problem at
// once so a caller can fix the whole request in one round (D5 §6).
const (
	ProblemCycle                = "cycle"
	ProblemDepMissing           = "dep_missing"
	ProblemParamInvalid         = "param_invalid"
	ProblemUniverseEmpty        = "universe_empty"
	ProblemDatasetMissing       = "dataset_missing"
	ProblemFieldMissing         = "field_missing"
	ProblemInsufficientHistory  = "insufficient_history"
	ProblemPITUnverified        = "pit_unverified"
	ProblemFrequencyUnsupported = "frequency_unsupported"
)

// Missing reasons carried per member on a Frame (D5 §6 reason set). A
// reason names why one instrument has no factor value; it is data, not an
// error.
const (
	ReasonMissingInput        = "missing_input"
	ReasonInsufficientHistory = "insufficient_history"
	ReasonStaleInput          = "stale_input"
	ReasonInvalidDenominator  = "invalid_denominator"
	ReasonPITUnverified       = "pit_unverified"
	ReasonNotApplicable       = "not_applicable"
)

// pitUnverifiedFlag is the quality flag that marks a row's point-in-time
// evidence as unverified even when it carries a published_at.
const pitUnverifiedFlag = "pit_unverified"
