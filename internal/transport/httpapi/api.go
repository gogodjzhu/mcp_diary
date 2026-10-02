package httpapi

import (
	"encoding/json"
	"net/http"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
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
