package domain

// Severity classes for issues surfaced by quality checks and providers.
const (
	SeverityInfo    = "info"
	SeverityWarning = "warning"
	SeverityError   = "error"
)

// Issue is a single structured finding attached to a data artifact or job.
// Code is a stable wire-visible code (see codes.go); Message is safe for
// clients and never contains internal causes.
type Issue struct {
	Code     string            `json:"code"`
	Path     string            `json:"path"`
	Message  string            `json:"message"`
	Severity string            `json:"severity"`
	Details  map[string]string `json:"details,omitempty"`
}
