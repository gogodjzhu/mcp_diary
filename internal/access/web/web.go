// Package web is the web access layer: a small REST API over the same
// sandboxed, per-user filesystem the MCP tools use. Authentication is applied
// by the assembly root (the shared OAuth middleware), which injects the
// resolved identity into the request context; the handlers here only read it so
// the workspace manager can resolve the right sandbox.
package web

import (
	"log/slog"
	"net/http"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/core/workspace"
)

// Config configures the web access layer.
type Config struct {
	// Workspaces resolves the sandboxed filesystem for the caller identity.
	Workspaces *workspace.Manager
	// Logger receives debug logging.
	Logger *slog.Logger
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
	identity, ok := identity.IdentityFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing authenticated identity")
		return
	}

	switch r.URL.Path {
	case "/api/me":
		h.handleMe(w, identity)
	case "/api/files":
		h.handleList(w, r)
	case "/api/file":
		h.handleRead(w, r)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
	}
}
