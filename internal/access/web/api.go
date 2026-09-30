package web

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gogodjzhu/mcp-diary/internal/auth"
	"github.com/gogodjzhu/mcp-diary/internal/filesystem"
	"github.com/gogodjzhu/mcp-diary/internal/workspace"
)

// errUnauthorized marks a request rejected before reaching the verifier.
var errUnauthorized = errors.New("missing bearer token")

func (h *Handler) handleMe(w http.ResponseWriter, identity *auth.Identity) {
	writeJSON(w, http.StatusOK, map[string]any{
		"subject":  identity.Subject,
		"email":    identity.Email,
		"name":     identity.Name,
		"username": identity.Username(),
		"scopes":   identity.Scopes,
	})
}

func (h *Handler) handleList(w http.ResponseWriter, r *http.Request) {
	fs, err := h.cfg.Workspaces.Filesystem(r.Context())
	if err != nil {
		writeFilesystemError(w, err)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		path = "."
	}

	entries, err := fs.List(r.Context(), path)
	if err != nil {
		writeFilesystemError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"path":    path,
		"count":   len(entries),
		"entries": entries,
	})
}

func (h *Handler) handleRead(w http.ResponseWriter, r *http.Request) {
	fs, err := h.cfg.Workspaces.Filesystem(r.Context())
	if err != nil {
		writeFilesystemError(w, err)
		return
	}

	path := r.URL.Query().Get("path")
	if path == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "path query parameter is required")
		return
	}

	content, err := fs.Read(r.Context(), path, queryInt(r, "offset"), queryInt(r, "limit"))
	if err != nil {
		writeFilesystemError(w, err)
		return
	}

	writeJSON(w, http.StatusOK, content)
}

func queryInt(r *http.Request, key string) int64 {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return 0
	}
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func bearerToken(r *http.Request) (string, bool) {
	const prefix = "Bearer "
	header := r.Header.Get("Authorization")
	if len(header) < len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return "", false
	}
	token := strings.TrimSpace(header[len(prefix):])
	return token, token != ""
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]string{
			"code":    code,
			"message": message,
		},
	})
}

// writeFilesystemError maps the shared sentinel errors onto HTTP status codes.
func writeFilesystemError(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	code := "internal_error"

	switch {
	case errors.Is(err, filesystem.ErrNotFound):
		status, code = http.StatusNotFound, "not_found"
	case errors.Is(err, filesystem.ErrOutsideRoot):
		status, code = http.StatusForbidden, "outside_root"
	case errors.Is(err, filesystem.ErrNotRegular):
		status, code = http.StatusBadRequest, "not_regular"
	case errors.Is(err, filesystem.ErrIsDirectory):
		status, code = http.StatusConflict, "is_directory"
	case errors.Is(err, filesystem.ErrReadOnly):
		status, code = http.StatusMethodNotAllowed, "read_only"
	case errors.Is(err, workspace.ErrUnauthenticated):
		status, code = http.StatusUnauthorized, "unauthenticated"
	}

	writeError(w, status, code, err.Error())
}
