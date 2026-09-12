package domain

import (
	"regexp"
	"strings"
)

// MaxIDLength bounds opaque identifiers on the wire.
const MaxIDLength = 64

// idPattern keeps IDs URL-safe and free of whitespace, control characters and
// path separators.
var idPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// ID is an opaque identifier. Values are server-generated or validated at
// every boundary; callers must not parse meaning out of them.
type ID string

// String returns the raw identifier text.
func (i ID) String() string { return string(i) }

// ParseID validates the wire form of an identifier.
func ParseID(s string) (ID, error) {
	if !idPattern.MatchString(s) {
		return "", NewError(CodeValidationInvalid, "invalid id %q", truncate(s))
	}
	return ID(s), nil
}

// LatestVersion is reserved for head lookups on read APIs and must never
// appear inside an immutable reference.
const LatestVersion = "latest"

// versionPattern: versions share the opaque, URL-safe shape of IDs.
var versionPattern = idPattern

// VersionRef addresses exactly one immutable revision of a versioned
// aggregate. Immutable references never float: the reserved value "latest"
// is rejected so lookups cannot silently fall back to a moving head.
type VersionRef struct {
	ID      ID     `json:"id"`
	Version string `json:"version"`
}

// Validate checks both fields and rejects the reserved "latest" spelling
// (case-insensitive).
func (r VersionRef) Validate() error {
	if r.ID == "" {
		return NewError(CodeValidationInvalid, "version ref: id is required")
	}
	if !idPattern.MatchString(r.ID.String()) {
		return NewError(CodeValidationInvalid, "version ref: invalid id %q", truncate(r.ID.String()))
	}
	if r.Version == "" {
		return NewError(CodeValidationInvalid, "version ref: version is required for %q", r.ID.String())
	}
	if !versionPattern.MatchString(r.Version) {
		return NewError(CodeValidationInvalid, "version ref: invalid version %q", truncate(r.Version))
	}
	if strings.EqualFold(r.Version, LatestVersion) {
		return NewError(CodeValidationInvalid, "version ref: %q is reserved; pin an exact version of %q", LatestVersion, r.ID.String())
	}
	return nil
}

// Match reports whether both fields are equal. There is no partial match:
// a reference with a stale version is a miss, never the head.
func (r VersionRef) Match(other VersionRef) bool {
	return r.ID == other.ID && r.Version == other.Version
}
