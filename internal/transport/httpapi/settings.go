package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gogodjzhu/mcp-diary/internal/core/diarymeta"
)

// handleSettings reads and writes the per-workspace diary metadata settings.
func (h *Handler) handleSettings(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Meta == nil {
		writeError(w, http.StatusServiceUnavailable, "unavailable", "diary metadata is not configured")
		return
	}
	fs, ok := h.workspaceFS(w, r)
	if !ok {
		return
	}

	switch r.Method {
	case http.MethodGet:
		settings, err := h.cfg.Meta.Get(r.Context(), fs)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to read settings")
			return
		}
		writeJSON(w, http.StatusOK, settings)
	case http.MethodPut, http.MethodPatch:
		body, err := decodeSettings(r)
		if err != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", err.Error())
			return
		}
		settings, err := h.cfg.Meta.Save(r.Context(), fs, body)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "internal_error", "failed to save settings")
			return
		}
		writeJSON(w, http.StatusOK, settings)
	default:
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "method not allowed")
	}
}

func decodeSettings(r *http.Request) (diarymeta.Settings, error) {
	var body diarymeta.Settings
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&body); err != nil && !errors.Is(err, io.EOF) {
		return diarymeta.Settings{}, errors.New("invalid settings body")
	}
	return body, nil
}
