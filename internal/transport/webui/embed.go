// Package webui serves the embedded single-page web application.
package webui

import (
	"embed"
	"io/fs"
	"net/http"
	"os"
	"path"
	"strings"
)

// assets holds the built web application. The dist directory is populated by
// the Vite build (see the "web-build" Makefile target); a placeholder file is
// committed so the package always compiles even before the frontend is built.
//
//go:embed all:dist
var assets embed.FS

// Handler serves the web UI. When staticDir is non-empty the assets are served
// from disk (useful during development); otherwise the embedded build is used.
//
// Unknown paths fall back to index.html so client-side routes such as the
// OAuth redirect target (/auth/callback) are handled by the SPA.
func Handler(staticDir string) http.Handler {
	var fsys fs.FS
	if staticDir != "" {
		fsys = os.DirFS(staticDir)
	} else {
		sub, err := fs.Sub(assets, "dist")
		if err != nil {
			return http.NotFoundHandler()
		}
		fsys = sub
	}

	fileServer := http.FileServer(http.FS(fsys))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(path.Clean(r.URL.Path), "/")
		if name != "" {
			info, err := fs.Stat(fsys, name)
			if err != nil || info.IsDir() {
				// Missing paths and directories (which would otherwise be
				// listed) fall through to the SPA entry point.
				r = r.Clone(r.Context())
				r.URL.Path = "/"
			}
		}
		fileServer.ServeHTTP(w, r)
	})
}
