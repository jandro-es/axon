// Package web embeds the built dashboard SPA (web/dist) into the Go binary, so
// the distributed binary is self-contained and needs no Node toolchain at
// runtime (ADR-003). The dist directory is produced by `npm run build` (Vite);
// install.sh builds it before `go build`.
package web

import (
	"embed"
	"io/fs"
)

//go:embed all:dist
var distFS embed.FS

// Assets returns the embedded SPA file system rooted at the build output, or nil
// if no build is embedded (in which case the dashboard serves a minimal page).
func Assets() fs.FS {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		return nil
	}
	// If dist is empty (no index.html), signal "no assets" so the server falls
	// back to its built-in page.
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}
