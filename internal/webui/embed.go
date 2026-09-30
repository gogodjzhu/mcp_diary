// Package webui serves the embedded single-page web application.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
)

// assets holds the built web application. The dist directory is populated by
// the Vite build (see the "web-build" Makefile target); a placeholder file is
// committed so the package always compiles even before the frontend is built.
//
//go:embed all:dist
var assets embed.FS

// Handler serves the web UI. When staticDir is non-empty the assets are served
// from disk (useful during development); otherwise the embedded build is used.
func Handler(staticDir string) http.Handler {
	if staticDir != "" {
		return http.FileServer(http.Dir(staticDir))
	}

	sub, err := fs.Sub(assets, "dist")
	if err != nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(sub))
}
