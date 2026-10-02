package httpapi_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/transport/httpapi"
)

func newHandler() *httpapi.Handler {
	return httpapi.New(httpapi.Config{})
}

func doRequest(t *testing.T, h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if token == "good-token" {
		id := &identity.Identity{Subject: "google-sub-1", Email: "alice@example.com", Name: "Alice"}
		req = req.WithContext(identity.WithIdentity(req.Context(), id))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestMe(t *testing.T) {
	h := newHandler()

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
	if body["username"] != "alice@example.com" {
		t.Fatalf("username = %v", body["username"])
	}
}

func TestMeRequiresToken(t *testing.T) {
	h := newHandler()

	rec := doRequest(t, h, http.MethodGet, "/api/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUnknownEndpointReturnsJSON404(t *testing.T) {
	h := newHandler()

	rec := doRequest(t, h, http.MethodGet, "/api/nope", "good-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
}

func TestFileEndpointsRemoved(t *testing.T) {
	h := newHandler()

	for _, path := range []string{"/api/files", "/api/file?path=diary.txt"} {
		rec := doRequest(t, h, http.MethodGet, path, "good-token")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
}
