package web_test

import (
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/access/web"
	"github.com/gogodjzhu/mcp-diary/internal/auth"
	"github.com/gogodjzhu/mcp-diary/internal/diary"
	"github.com/gogodjzhu/mcp-diary/internal/workspace"
)

func newHandler(t *testing.T) (*web.Handler, *diary.Service, string) {
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

	now := time.Date(2026, 10, 1, 13, 0, 0, 0, time.UTC)
	svc := diary.New(func() time.Time { return now }, time.UTC)
	return web.New(web.Config{Workspaces: workspaces, Diary: svc}), svc, root
}

func pngFixture() []byte {
	return []byte{
		0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A,
		0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52,
		0x00, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x01,
		0x08, 0x02, 0x00, 0x00, 0x00, 0x90, 0x77, 0x53,
		0xDE, 0x00, 0x00, 0x00, 0x0C, 0x49, 0x44, 0x41,
		0x54, 0x08, 0xD7, 0x63, 0xF8, 0xCF, 0xC0, 0x00,
		0x00, 0x00, 0x03, 0x00, 0x01, 0x00, 0x05, 0xFE,
		0xD4, 0xEF, 0x00, 0x00, 0x00, 0x00, 0x49, 0x45,
		0x4E, 0x44, 0xAE, 0x42, 0x60, 0x82,
	}
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
	h, _, _ := newHandler(t)

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
	h, _, _ := newHandler(t)

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
	h, _, _ := newHandler(t)

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
	h, _, _ := newHandler(t)

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
	h, _, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/file?path=nope.txt", "good-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestUnknownEndpointReturnsJSON404(t *testing.T) {
	h, _, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/nope", "good-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
}

func TestEscapeRejected(t *testing.T) {
	h, _, _ := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/file?path=../../etc/passwd", "good-token")
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestUploadAndServeDiaryAttachment(t *testing.T) {
	h, svc, root := newHandler(t)
	userDir := filepath.Join(root, "users", "alice@example.com")
	created, err := svc.CreateSession(userDir, diary.CreateSessionIn{RequestID: "w1", DiaryDate: "2026-10-01"})
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	sessionID := created.(*diary.Session).SessionID

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	_ = mw.WriteField("session_id", sessionID)
	part, err := mw.CreateFormFile("file", "photo.png")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := part.Write(pngFixture()); err != nil {
		t.Fatal(err)
	}
	if err := mw.Close(); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/diary/attachments", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = req.WithContext(auth.WithIdentity(req.Context(), &auth.Identity{Subject: "google-sub-1", Email: "alice@example.com", Name: "Alice"}))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("upload status = %d body %s", rec.Code, rec.Body.String())
	}

	var uploaded struct {
		Attachment struct {
			AttachmentID string `json:"attachment_id"`
			OwnerID      string `json:"owner_id"`
			MIMEType     string `json:"mime_type"`
		} `json:"attachment"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &uploaded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if uploaded.Attachment.MIMEType != "image/png" {
		t.Fatalf("mime = %s", uploaded.Attachment.MIMEType)
	}

	fileRec := doRequest(t, h, http.MethodGet, "/api/diary/attachments/"+uploaded.Attachment.OwnerID+"/"+uploaded.Attachment.AttachmentID, "good-token")
	if fileRec.Code != http.StatusOK {
		t.Fatalf("serve status = %d body %s", fileRec.Code, fileRec.Body.String())
	}
	if ct := fileRec.Header().Get("Content-Type"); ct != "image/png" {
		t.Fatalf("content-type = %q", ct)
	}
	got, _ := io.ReadAll(fileRec.Body)
	if !bytes.Equal(got, pngFixture()) {
		t.Fatalf("served bytes mismatch")
	}

	listRec := doRequest(t, h, http.MethodGet, "/api/diary/sessions", "good-token")
	if listRec.Code != http.StatusOK {
		t.Fatalf("list sessions = %d %s", listRec.Code, listRec.Body.String())
	}

	bad := doRequest(t, h, http.MethodGet, "/api/diary/attachments/../etc/passwd/x", "good-token")
	if bad.Code != http.StatusBadRequest && bad.Code != http.StatusNotFound {
		t.Fatalf("escape status = %d", bad.Code)
	}
}
