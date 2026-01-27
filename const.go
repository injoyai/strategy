package dist

import (
	"embed"
)

//go:embed web/dist/*
var Dist embed.FS
