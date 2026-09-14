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
	// catalog exists yet — the ingestion field mapping (units in particular)
	// is not persisted, so dataset/field/unit cannot be checked.
	codeCatalogUnavailable = "screenrun.catalog_unavailable"
	// codeDataAvailabilityUnchecked: the factor reference and its parameters
	// are valid, but the factor's data availability (PIT, lookback,
	// staleness) was not evaluated because the window derivation the engine
	// needs is not defined yet.
	codeDataAvailabilityUnchecked = "screenrun.data_availability_unchecked"
)
