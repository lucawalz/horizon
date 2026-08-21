//go:build !no_ui

package site

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distDir embed.FS

// Embedding from inside the Vite root keeps emptyOutDir authoritative, so a stale bundle cannot survive a rebuild.
var DistDirFS, _ = fs.Sub(distDir, "dist")
