package app

import (
	"encoding/json"
	"net/http"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/transport/webui"
)

// HTTPHandler returns the HTTP handler tree: the OAuth authorization-server
// endpoints and discovery documents, a health probe, the (optionally
// authenticated) MCP Streamable HTTP endpoint, the web API and UI, and
// cross-cutting middleware.
func (a *App) HTTPHandler() http.Handler {
	mux := http.NewServeMux()

	if a.oauth != nil {
		a.oauth.RegisterRoutes(mux, a.cfg.EndpointPath)
	}

	mux.Handle(a.cfg.EndpointPath, a.protect(a.mcp.StreamableHandler()))
	mux.HandleFunc("/healthz", a.handleHealthz)

	if a.web != nil {
		mux.Handle("/api/", a.protect(a.web))
		mux.Handle("/", webui.Handler(a.cfg.Web.StaticDir))
	}

	return a.withAccessLog(a.withMaxBody(mux))
}

// protect wraps a handler with OAuth bearer-token authentication when enabled.
func (a *App) protect(next http.Handler) http.Handler {
	if a.oauth == nil {
		return next
	}
	return a.oauth.Protect(next)
}

func (a *App) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"status":       "ok",
		"name":         a.cfg.Name,
		"version":      a.cfg.Version,
		"read_only":    a.workspaces.ReadOnly(),
		"root":         a.workspaces.Root(),
		"users_dir":    a.workspaces.UsersDir(),
		"per_user":     a.workspaces.PerUser(),
		"auth_enabled": a.cfg.Auth.Enabled,
		"auth_issuer":  a.cfg.Auth.PublicURL,
		"web_enabled":  a.web != nil,
		"tools":        a.ToolNames(),
	})
}

// withMaxBody caps the size of incoming request bodies.
func (a *App) withMaxBody(next http.Handler) http.Handler {
	if a.cfg.MaxRequestBodyBytes <= 0 {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, a.cfg.MaxRequestBodyBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// withAccessLog logs every request. It deliberately avoids wrapping
// http.ResponseWriter so it never breaks SSE flushing or the optional
// http.Flusher interface.
func (a *App) withAccessLog(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		next.ServeHTTP(w, r)
		a.logger.Debug("http request",
			"method", r.Method,
			"path", r.URL.Path,
			"remote", r.RemoteAddr,
			"duration", time.Since(start),
		)
	})
}
