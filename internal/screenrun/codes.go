package screenrun

// Stable wire codes for screening-run findings. The code carries the class and
// the issue carries the severity, so a warning and an error can share a code
// family without the caller having to parse messages.
const (
	// codeEmptyPopulation: the mother pool has no members at the decision
	// time. A run would succeed with an empty result, so this is a finding
	// rather than a failure.
	codeEmptyPopulation = "screenrun.empty_population"
	// codeCatalogUnavailable: a binding cannot be resolved because no input
	// catalog entry exists for it (an unknown binding kind).
	codeCatalogUnavailable = "screenrun.catalog_unavailable"
	// codeDatasetUnknown: a field binding references a dataset the catalog does
	// not have.
	codeDatasetUnknown = "screenrun.dataset_unknown"
	// codeFieldUnknown: the dataset exists but has no such field.
	codeFieldUnknown = "screenrun.field_unknown"
	// codeFieldUnobserved: the field is declared but no value was ever
	// observed, so its type is unknown and unit/kind checks cannot run.
	codeFieldUnobserved = "screenrun.field_unobserved"
	// codeUnitUndeclared: a factor input was bound to a field whose unit was
	// never declared, so the factor's unit contract cannot be verified. An
	// unverifiable contract is blocked rather than assumed.
	codeUnitUndeclared = "screenrun.unit_undeclared"
	// codeUnitMismatch: the field's declared unit is not the unit the factor
	// input declares, so the values would be used under a unit they do not
	// carry.
	codeUnitMismatch = "screenrun.unit_mismatch"
	// CodePreflightFailed: submission re-runs resolution and refuses to compute
	// when any error-severity finding came out of it. The HTTP mapping needs
	// the literal, so it is exported rather than repeated.
	CodePreflightFailed = "screenrun.preflight_failed"
	// CodeEmptySelection: saving a pool from a run that selected nothing is
	// refused rather than recorded. An empty member list is not a research
	// decision, and a saved pool that resolves to nobody would silently make
	// every later backtest empty.
	CodeEmptySelection = "screenrun.empty_selection"
)
