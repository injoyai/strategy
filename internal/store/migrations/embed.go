// Package migrations holds the versioned SQL schema, embedded into the
// binary so a deployed service always migrates with the exact statements it
// was built with.
package migrations

import (
	"embed"
	"io/fs"
)

//go:embed *.sql
var files embed.FS

// FS returns the migration files as a flat filesystem. File names follow the
// goose "<6-digit version>_<name>.sql" convention; the checksum verification
// in the store package relies on that format to locate files by version.
func FS() fs.FS { return files }
