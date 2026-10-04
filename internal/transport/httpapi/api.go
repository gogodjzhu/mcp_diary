package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
	"github.com/gogodjzhu/mcp-diary/internal/core/sync"
	"github.com/gogodjzhu/mcp-diary/internal/core/workspace"
)

func (h *Handler) handleMe(w http.ResponseWriter, id *identity.Identity) {
	writeJSON(w, http.StatusOK, map[string]any{
		"subject":  id.Subject,
		"email":    id.Email,
		"name":     id.Name,
		"username": id.Username(),
		"scopes":   id.Scopes,
	})
}

func (h *Handler) handleMediaCollection(w http.ResponseWriter, r *http.Request) {
	fs, ok := h.workspaceFS(w, r)
	if !ok {
		return
	}
	switch r.Method {
	case http.MethodGet:
		h.listMedia(w, r, fs)
	case http.MethodPost:
		h.createMedia(w, r, fs)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func (h *Handler) handleMediaItem(w http.ResponseWriter, r *http.Request, rest string) {
	id, action, ok := splitMediaPath(rest)
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
		return
	}
	fs, ok := h.workspaceFS(w, r)
	if !ok {
		return
	}
	switch action {
	case "":
		switch r.Method {
		case http.MethodGet:
			h.getMedia(w, r, fs, id)
		case http.MethodPut, http.MethodPatch:
			h.updateMedia(w, r, fs, id)
		case http.MethodDelete:
			h.deleteMedia(w, r, fs, id)
		default:
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
		}
	case "state":
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		h.getState(w, r, fs, id)
	case "sync":
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
			return
		}
		h.triggerSync(w, r, fs, id)
	default:
		writeError(w, http.StatusNotFound, "not_found", "unknown API endpoint")
	}
}

func splitMediaPath(rest string) (id, action string, ok bool) {
	rest = strings.Trim(rest, "/")
	if rest == "" {
		return "", "", false
	}
	id, action, _ = strings.Cut(rest, "/")
	id = strings.TrimSpace(id)
	if id == "" || strings.Contains(action, "/") {
		return "", "", false
	}
	return id, action, true
}

func (h *Handler) workspaceFS(w http.ResponseWriter, r *http.Request) (*filesystem.Service, bool) {
	if h.cfg.Workspaces == nil || h.cfg.Sync == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "sync is not configured")
		return nil, false
	}
	fs, err := h.cfg.Workspaces.Filesystem(r.Context())
	if err != nil {
		if errors.Is(err, workspace.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "invalid_request", "missing authenticated identity")
			return nil, false
		}
		writeError(w, http.StatusInternalServerError, "internal_error", "failed to open workspace")
		return nil, false
	}
	return fs, true
}

func (h *Handler) listMedia(w http.ResponseWriter, r *http.Request, fs *filesystem.Service) {
	views, err := h.cfg.Sync.ListViews(r.Context(), fs)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	if views == nil {
		views = []sync.MediumView{}
	}
	writeJSON(w, http.StatusOK, map[string]any{"media": views})
}

func (h *Handler) getMedia(w http.ResponseWriter, r *http.Request, fs *filesystem.Service, id string) {
	pub, err := h.cfg.Sync.Get(r.Context(), fs, id)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	st, err := h.cfg.Sync.State(r.Context(), fs, id)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sync.MediumView{PublicMedium: *pub, State: *st})
}

type mediaBody struct {
	Kind       string            `json:"kind"`
	Name       string            `json:"name"`
	Enabled    *bool             `json:"enabled"`
	Settings   map[string]string `json:"settings"`
	Credential string            `json:"credential"`
}

func (h *Handler) createMedia(w http.ResponseWriter, r *http.Request, fs *filesystem.Service) {
	body, ok := decodeMediaBody(w, r)
	if !ok {
		return
	}
	pub, err := h.cfg.Sync.Upsert(r.Context(), fs, sync.UpsertIn{
		Kind:       sync.Kind(strings.TrimSpace(body.Kind)),
		Name:       body.Name,
		Enabled:    body.Enabled,
		Settings:   body.Settings,
		Credential: body.Credential,
	})
	if err != nil {
		writeSyncError(w, err)
		return
	}
	st, err := h.cfg.Sync.State(r.Context(), fs, pub.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, sync.MediumView{PublicMedium: *pub, State: *st})
}

func (h *Handler) updateMedia(w http.ResponseWriter, r *http.Request, fs *filesystem.Service, id string) {
	body, ok := decodeMediaBody(w, r)
	if !ok {
		return
	}
	pub, err := h.cfg.Sync.Upsert(r.Context(), fs, sync.UpsertIn{
		ID:         id,
		Kind:       sync.Kind(strings.TrimSpace(body.Kind)),
		Name:       body.Name,
		Enabled:    body.Enabled,
		Settings:   body.Settings,
		Credential: body.Credential,
	})
	if err != nil {
		writeSyncError(w, err)
		return
	}
	st, err := h.cfg.Sync.State(r.Context(), fs, pub.ID)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, sync.MediumView{PublicMedium: *pub, State: *st})
}

func (h *Handler) deleteMedia(w http.ResponseWriter, r *http.Request, fs *filesystem.Service, id string) {
	if err := h.cfg.Sync.Delete(r.Context(), fs, id); err != nil {
		writeSyncError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) getState(w http.ResponseWriter, r *http.Request, fs *filesystem.Service, id string) {
	st, err := h.cfg.Sync.State(r.Context(), fs, id)
	if err != nil {
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (h *Handler) triggerSync(w http.ResponseWriter, r *http.Request, fs *filesystem.Service, id string) {
	st, err := h.cfg.Sync.Trigger(r.Context(), fs, id)
	if err != nil {
		if st != nil && (errors.Is(err, sync.ErrEngineNotConfigured) || errors.Is(err, sync.ErrMediumDisabled)) {
			writeJSON(w, statusFor(err), map[string]any{
				"error": map[string]string{
					"code":    codeFor(err),
					"message": err.Error(),
				},
				"state": st,
			})
			return
		}
		if st != nil && !errors.Is(err, sync.ErrNotFound) {
			writeJSON(w, http.StatusBadGateway, map[string]any{
				"error": map[string]string{
					"code":    "sync_failed",
					"message": err.Error(),
				},
				"state": st,
			})
			return
		}
		writeSyncError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func decodeMediaBody(w http.ResponseWriter, r *http.Request) (mediaBody, bool) {
	defer r.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "failed to read request body")
		return mediaBody{}, false
	}
	if len(strings.TrimSpace(string(raw))) == 0 {
		writeError(w, http.StatusBadRequest, "invalid_request", "request body is required")
		return mediaBody{}, false
	}
	var body mediaBody
	if err := json.Unmarshal(raw, &body); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid JSON")
		return mediaBody{}, false
	}
	return body, true
}

func writeSyncError(w http.ResponseWriter, err error) {
	writeError(w, statusFor(err), codeFor(err), err.Error())
}

func statusFor(err error) int {
	switch {
	case errors.Is(err, sync.ErrNotFound):
		return http.StatusNotFound
	case errors.Is(err, sync.ErrUnknownKind),
		errors.Is(err, sync.ErrMediumNameRequired),
		errors.Is(err, sync.ErrMediumKindRequired),
		errors.Is(err, sync.ErrMediumIDRequired),
		errors.Is(err, sync.ErrSecretKey),
		errors.Is(err, sync.ErrInvalidCiphertext):
		return http.StatusBadRequest
	case errors.Is(err, sync.ErrNoEncryptionKey),
		errors.Is(err, sync.ErrInvalidKey),
		errors.Is(err, sync.ErrEngineNotConfigured):
		return http.StatusServiceUnavailable
	case errors.Is(err, sync.ErrMediumDisabled),
		errors.Is(err, filesystem.ErrReadOnly):
		return http.StatusConflict
	case errors.Is(err, sync.ErrAttachmentNotSupported):
		return http.StatusNotImplemented
	default:
		return http.StatusInternalServerError
	}
}

func codeFor(err error) string {
	switch {
	case errors.Is(err, sync.ErrNotFound):
		return "not_found"
	case errors.Is(err, sync.ErrUnknownKind):
		return "unknown_kind"
	case errors.Is(err, sync.ErrMediumNameRequired),
		errors.Is(err, sync.ErrMediumKindRequired),
		errors.Is(err, sync.ErrMediumIDRequired),
		errors.Is(err, sync.ErrSecretKey):
		return "invalid_request"
	case errors.Is(err, sync.ErrNoEncryptionKey),
		errors.Is(err, sync.ErrInvalidKey):
		return "encryption_unavailable"
	case errors.Is(err, sync.ErrEngineNotConfigured):
		return "engine_unavailable"
	case errors.Is(err, sync.ErrMediumDisabled):
		return "medium_disabled"
	case errors.Is(err, filesystem.ErrReadOnly):
		return "read_only"
	default:
		return "internal_error"
	}
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
