package ports

// ScreenerFilter is the list query for screener versions, with the same
// Sort / AfterID / Limit semantics as UniverseFilter. Q is a substring filter
// on the screener name.
type ScreenerFilter struct {
	Q       string
	Sort    string
	AfterID string
	Limit   int
}

// ScreenRunFilter is the list query for screening runs: the same Sort /
// AfterID / Limit keyset semantics as the other listings, narrowed by the
// screener identity a run was submitted for and by the job state the run's job
// currently reports. Both filters are optional; an empty value means "all".
type ScreenRunFilter struct {
	ScreenerID string
	JobState   string
	Sort       string
	AfterID    string
	Limit      int
}

// ScreenRunRowFilter pages the frozen result rows of one published run.
// AfterOrdinal is the exclusive resume point in the run's canonical row order
// (the same order the API reports); nil starts at the first row. Selected
// narrows to selected (true) or excluded (false) rows; nil means every row, in
// which case a page can mix both.
type ScreenRunRowFilter struct {
	AfterOrdinal *int64
	Selected     *bool
	Limit        int
}
