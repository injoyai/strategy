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
