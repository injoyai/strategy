package dist

import (
	"embed"
	_ "embed"
)

//go:embed web/dist
var Dist embed.FS
