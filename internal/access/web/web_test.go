package web_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gogodjzhu/mcp-diary/internal/access/web"
	"github.com/gogodjzhu/mcp-diary/internal/auth"
	"github.com/gogodjzhu/mcp-diary/internal/workspace"
)

func newHandler(t *testing.T) (*web.Handler, string) {
	t.Helper()

	root := t.TempDir()
	workspaces, err := workspace.New(workspace.Config{
		Root:         root,
		UsersDir:     "users",
		PerUser:      true,
		MaxReadBytes: 1 << 20,
	})
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}

	userDir := filepath.Join(root, "users", "alice@example.com")
	if err := os.MkdirAll(userDir, 0o700); err != nil {
		t.Fatalf("mkdir user dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(userDir, "diary.txt"), []byte("hello diary"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	return web.New(web.Config{Workspaces: workspaces}), root
}

func doRequest(t *testing.T, h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if token == "good-token" {
		identity := &auth.Identity{Subject: "google-sub-1", Email: "alice@example.com", Name: "Alice"}
		req = req.WithContext(auth.WithIdentity(req.Context(), identity))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMe(t *testing.T) {
	h, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/me", "good-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["email"] != "alice@example.com" {
		t.Fatalf("email = %v", body["email"])
	}
}

func TestMeRequiresToken(t *testing.T) {
	h, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/me", "wrong-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestListUsesPerUserWorkspace(t *testing.T) {
	h, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/files", "good-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Count   int `json:"count"`
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Count != 1 || body.Entries[0].Name != "diary.txt" {
		t.Fatalf("unexpected listing: %+v", body)
	}
}

func TestReadFile(t *testing.T) {
	h, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/file?path=diary.txt", "good-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	var body struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Content != "hello diary" {
		t.Fatalf("content = %q", body.Content)
	}
}

func TestReadMissingFileReturns404(t *testing.T) {
	h, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/file?path=nope.txt", "good-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestUnknownEndpointReturnsJSON404(t *testing.T) {
	h, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/nope", "good-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
}

func TestEscapeRejected(t *testing.T) {
	h, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/file?path=../../etc/passwd", "good-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
}
