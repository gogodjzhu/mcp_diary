// Package httpapi is the web access layer: a small REST API for the browser
// UI. Authentication is applied by the assembly root (the shared OAuth
// middleware), which injects the resolved identity into the request context;
// the handlers here only read it.
package httpapi

import (
	"log/slog"
	"net/http"
	"strings"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/core/sync"
	"github.com/gogodjzhu/mcp-diary/internal/core/workspace"
)

// Config configures the web access layer.
type Config struct {
	Logger     *slog.Logger
	Workspaces *workspace.Manager
	Sync       *sync.Service
}

// Handler is the REST API handler. It is mounted under the "/api/" prefix and
// is only registered by the assembly root when authentication is enabled.
type Handler struct {
	cfg Config
}

// New builds the web access layer.
func New(cfg Config) *Handler { return &Handler{cfg: cfg} }

// ServeHTTP reads the authenticated identity injected by the OAuth middleware
// and dispatches to the matching endpoint. Unknown endpoints return a JSON 404
// so API typos never fall through to the single-page application.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	id, ok := identity.IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing authenticated identity")
		return
	}

	path := r.URL.Path
	switch {
	case path == "/api/me":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		h.handleMe(w, id)
	case path == "/api/sync/media":
		h.handleMediaCollection(w, r)
	case strings.HasPrefix(path, "/api/sync/media/"):
		h.handleMediaItem(w, r, strings.TrimPrefix(path, "/api/sync/media/"))
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
	}
}
