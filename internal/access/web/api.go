package web

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/gogodjzhu/mcp-diary/internal/auth"
	"github.com/gogodjzhu/mcp-diary/internal/diary"
	"github.com/gogodjzhu/mcp-diary/internal/filesystem"
	"github.com/gogodjzhu/mcp-diary/internal/workspace"
)

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

func (h *Handler) workspaceRoot(w http.ResponseWriter, r *http.Request) (string, bool) {
	fs, err := h.cfg.Workspaces.Filesystem(r.Context())
	if err != nil {
		writeFilesystemError(w, err)
		return "", false
	}
	return fs.Root(), true
}

func (h *Handler) handleDiaryEntries(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Diary == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "diary service unavailable")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	root, ok := h.workspaceRoot(w, r)
	if !ok {
		return
	}
	entries, err := h.cfg.Diary.ListCommitted(root)
	if err != nil {
		writeDiaryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"entries": entries,
		"count":   len(entries),
	})
}

func (h *Handler) handleDiarySessions(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Diary == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "diary service unavailable")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	root, ok := h.workspaceRoot(w, r)
	if !ok {
		return
	}
	sessions, err := h.cfg.Diary.ListDrafts(root)
	if err != nil {
		writeDiaryError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"sessions": sessions,
		"count":    len(sessions),
	})
}

func (h *Handler) handleDiaryAttachments(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Diary == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "diary service unavailable")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "POST required")
		return
	}
	root, ok := h.workspaceRoot(w, r)
	if !ok {
		return
	}
	if err := r.ParseMultipartForm(diary.MaxVideoBytes + (1 << 20)); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "invalid multipart form")
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "file is required")
		return
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, diary.MaxVideoBytes+1))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "failed to read upload")
		return
	}
	filename := header.Filename
	if name := strings.TrimSpace(r.FormValue("filename")); name != "" {
		filename = name
	}
	out, err := h.cfg.Diary.AttachMedia(root, diary.AttachMediaIn{
		RequestID: diary.NewRequestID(),
		SessionID: strings.TrimSpace(r.FormValue("session_id")),
		EntryID:   strings.TrimSpace(r.FormValue("entry_id")),
		Filename:  filename,
		MIMEType:  header.Header.Get("Content-Type"),
		Data:      data,
	})
	if err != nil {
		writeDiaryError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, out)
}

func (h *Handler) handleDiaryAttachmentFile(w http.ResponseWriter, r *http.Request) {
	if h.cfg.Diary == nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "diary service unavailable")
		return
	}
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "GET required")
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/diary/attachments/")
	parts := strings.Split(rest, "/")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "expected /api/diary/attachments/{owner_id}/{attachment_id}")
		return
	}
	root, ok := h.workspaceRoot(w, r)
	if !ok {
		return
	}
	opened, err := h.cfg.Diary.OpenAttachment(root, diary.OpenAttachmentIn{
		OwnerID:      parts[0],
		AttachmentID: parts[1],
	})
	if err != nil {
		writeDiaryError(w, err)
		return
	}
	f, err := os.Open(opened.Path)
	if err != nil {
		writeError(w, http.StatusNotFound, "not_found", "attachment file missing")
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "stat attachment")
		return
	}
	w.Header().Set("Content-Type", opened.Attachment.MIMEType)
	w.Header().Set("Content-Disposition", "inline; filename=\""+opened.Attachment.Filename+"\"")
	w.Header().Set("Cache-Control", "private, max-age=3600")
	http.ServeContent(w, r, opened.Attachment.Filename, info.ModTime(), f)
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

func writeDiaryError(w http.ResponseWriter, err error) {
	de, ok := diary.IsError(err)
	if !ok {
		if errors.Is(err, workspace.ErrUnauthenticated) {
			writeError(w, http.StatusUnauthorized, "unauthenticated", err.Error())
			return
		}
		writeError(w, http.StatusInternalServerError, "internal_error", err.Error())
		return
	}
	status := http.StatusInternalServerError
	code := "internal_error"
	switch de.Code {
	case 400:
		status, code = http.StatusBadRequest, "invalid_request"
	case 401:
		status, code = http.StatusUnauthorized, "unauthenticated"
	case 403:
		status, code = http.StatusForbidden, "forbidden"
	case 404:
		status, code = http.StatusNotFound, "not_found"
	case 409:
		status, code = http.StatusConflict, "conflict"
	case 413:
		status, code = http.StatusRequestEntityTooLarge, "too_large"
	}
	writeError(w, status, code, de.Message)
}
