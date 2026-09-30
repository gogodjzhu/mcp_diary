// Package web is the web access layer: a small REST API over the same
// sandboxed, per-user filesystem the MCP tools use. Every request is
// authenticated with the shared Google bearer-token verifier and the resolved
// identity is injected into the request context so the workspace manager can
// resolve the right sandbox.
package web

import (
	"log/slog"
	"net/http"

	"github.com/gogodjzhu/mcp-diary/internal/auth"
	"github.com/gogodjzhu/mcp-diary/internal/workspace"
)

// Config configures the web access layer.
type Config struct {
	// Verifier validates the bearer token attached to every request.
	Verifier auth.Verifier
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

// ServeHTTP authenticates the request, injects the identity and dispatches to
// the matching endpoint. Unknown endpoints return a JSON 404 so API typos never
// fall through to the single-page application.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	identity, err := h.authenticate(w, r)
	if err != nil {
		return // the response has already been written
	}

	ctx := auth.WithIdentity(r.Context(), identity)
	r = r.WithContext(ctx)

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

func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (*auth.Identity, error) {
	token, ok := bearerToken(r)
	if !ok {
		err := errUnauthorized
		writeError(w, http.StatusUnauthorized, "invalid_request", "missing bearer token")
		return nil, err
	}

	identity, err := h.cfg.Verifier.Verify(r.Context(), token)
	if err != nil {
		if h.cfg.Logger != nil {
			h.cfg.Logger.Debug("web authentication failed", "error", err)
		}
		writeError(w, http.StatusUnauthorized, "invalid_token", err.Error())
		return nil, err
	}
	if h.cfg.Logger != nil {
		h.cfg.Logger.Debug("web request authenticated", "user", identity.Username())
	}
	return identity, nil
}
