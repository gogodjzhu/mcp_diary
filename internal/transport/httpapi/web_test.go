package httpapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/core/diarymeta"
	"github.com/gogodjzhu/mcp-diary/internal/core/diarysync"
	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
	"github.com/gogodjzhu/mcp-diary/internal/core/workspace"
	"github.com/gogodjzhu/mcp-diary/internal/transport/httpapi"
)

const testKey = "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY="

func newHandler(t *testing.T) *httpapi.Handler {
	t.Helper()
	return newHandlerWith(t, nil)
}

func newHandlerWith(t *testing.T, engine diarysync.Runner) *httpapi.Handler {
	t.Helper()
	codec, err := diarysync.NewCodec(testKey)
	if err != nil {
		t.Fatalf("NewCodec: %v", err)
	}
	reg := diarysync.NewRegistry()
	reg.Register(diarysync.KindGitHub, diarysync.MemoryFactory(diarysync.KindGitHub, nil))
	svc := diarysync.New(codec, reg, func() time.Time {
		return time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	})
	if engine != nil {
		svc.SetRunner(engine)
	}
	wm, err := workspace.New(workspace.Config{
		Root:    t.TempDir(),
		PerUser: true,
	})
	if err != nil {
		t.Fatalf("workspace.New: %v", err)
	}
	return httpapi.New(httpapi.Config{
		Workspaces: wm,
		Sync:       svc,
		Meta:       diarymeta.New(),
	})
}

func TestSettingsRoundTrip(t *testing.T) {
	h := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/settings", "good-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET settings status = %d body=%s", rec.Code, rec.Body.String())
	}

	body := map[string]any{"lunar_enabled": true, "weather_enabled": true, "weather_location": "Beijing"}
	rec = doRequestBody(t, h, http.MethodPut, "/api/settings", "good-token", body)
	if rec.Code != http.StatusOK {
		t.Fatalf("PUT settings status = %d body=%s", rec.Code, rec.Body.String())
	}
	got := decode(t, rec)
	if got["weather_location"] != "Beijing" || got["lunar_enabled"] != true {
		t.Fatalf("settings = %v", got)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/settings", "good-token")
	got = decode(t, rec)
	if got["weather_location"] != "Beijing" {
		t.Fatalf("reloaded settings = %v", got)
	}
}

func TestSettingsRequiresAuth(t *testing.T) {
	h := newHandler(t)
	rec := doRequest(t, h, http.MethodGet, "/api/settings", "bad-token")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func doRequest(t *testing.T, h http.Handler, method, target, token string) *httptest.ResponseRecorder {
	t.Helper()
	return doRequestBody(t, h, method, target, token, nil)
}

func doRequestBody(t *testing.T, h http.Handler, method, target, token string, body any) *httptest.ResponseRecorder {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(raw)
	}
	req := httptest.NewRequest(method, target, rdr)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if token == "good-token" {
		id := &identity.Identity{Subject: "google-sub-1", Email: "alice@example.com", Name: "Alice"}
		req = req.WithContext(identity.WithIdentity(req.Context(), id))
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode %q: %v", rec.Body.String(), err)
	}
	return body
}

func TestMe(t *testing.T) {
	h := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/me", "good-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %s)", rec.Code, rec.Body.String())
	}

	body := decode(t, rec)
	if body["email"] != "alice@example.com" {
		t.Fatalf("email = %v", body["email"])
	}
	if body["username"] != "alice@example.com" {
		t.Fatalf("username = %v", body["username"])
	}
}

func TestMeRequiresToken(t *testing.T) {
	h := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/me", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

func TestUnknownEndpointReturnsJSON404(t *testing.T) {
	h := newHandler(t)

	rec := doRequest(t, h, http.MethodGet, "/api/nope", "good-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Fatalf("content-type = %q, want application/json", ct)
	}
}

func TestFileEndpointsRemoved(t *testing.T) {
	h := newHandler(t)

	for _, path := range []string{"/api/files", "/api/file?path=diary.txt"} {
		rec := doRequest(t, h, http.MethodGet, path, "good-token")
		if rec.Code != http.StatusNotFound {
			t.Fatalf("GET %s status = %d, want 404", path, rec.Code)
		}
	}
}

func TestSyncMediaCRUDAndManualTrigger(t *testing.T) {
	token := "ghp_super_secret_token"
	var synced string
	h := newHandlerWith(t, funcRunner(func(_ context.Context, _ *filesystem.Service, mediumID string) error {
		synced = mediumID
		return nil
	}))

	rec := doRequest(t, h, http.MethodGet, "/api/sync/media", "good-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("list empty status = %d body %s", rec.Code, rec.Body.String())
	}
	if media, _ := decode(t, rec)["media"].([]any); len(media) != 0 {
		t.Fatalf("want empty list, got %v", media)
	}

	rec = doRequestBody(t, h, http.MethodPost, "/api/sync/media", "good-token", map[string]any{
		"kind":       "github",
		"name":       "personal github",
		"credential": token,
		"settings":   map[string]string{"owner": "alice", "repo": "diary"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body %s", rec.Code, rec.Body.String())
	}
	created := decode(t, rec)
	if strings.Contains(rec.Body.String(), token) {
		t.Fatal("create response leaked credential")
	}
	if created["has_credential"] != true {
		t.Fatalf("has_credential = %v", created["has_credential"])
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatalf("missing id: %v", created)
	}
	settings, _ := created["settings"].(map[string]any)
	if settings["owner"] != "alice" || settings["repo"] != "diary" {
		t.Fatalf("settings = %v", settings)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/sync/media", "good-token")
	listed := decode(t, rec)
	media, _ := listed["media"].([]any)
	if len(media) != 1 {
		t.Fatalf("list = %v", listed)
	}

	rec = doRequest(t, h, http.MethodGet, "/api/sync/media/"+id+"/state", "good-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("state status = %d body %s", rec.Code, rec.Body.String())
	}
	if decode(t, rec)["status"] != "idle" {
		t.Fatalf("state = %s", rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodPost, "/api/sync/media/"+id+"/sync", "good-token")
	if rec.Code != http.StatusOK {
		t.Fatalf("sync status = %d body %s", rec.Code, rec.Body.String())
	}
	if synced != id {
		t.Fatalf("engine medium = %q, want %s", synced, id)
	}

	rec = doRequestBody(t, h, http.MethodPut, "/api/sync/media/"+id, "good-token", map[string]any{
		"name":     "renamed",
		"enabled":  true,
		"settings": map[string]string{"owner": "alice", "repo": "notes"},
	})
	if rec.Code != http.StatusOK {
		t.Fatalf("update status = %d body %s", rec.Code, rec.Body.String())
	}
	if decode(t, rec)["name"] != "renamed" {
		t.Fatalf("update = %s", rec.Body.String())
	}

	rec = doRequest(t, h, http.MethodDelete, "/api/sync/media/"+id, "good-token")
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete status = %d body %s", rec.Code, rec.Body.String())
	}
	rec = doRequest(t, h, http.MethodGet, "/api/sync/media/"+id, "good-token")
	if rec.Code != http.StatusNotFound {
		t.Fatalf("get after delete = %d", rec.Code)
	}
}

func TestSyncMediaRejectsCredentialInSettingsAndNeverReturnsPlaintext(t *testing.T) {
	h := newHandler(t)
	rec := doRequestBody(t, h, http.MethodPost, "/api/sync/media", "good-token", map[string]any{
		"kind":     "github",
		"name":     "bad",
		"settings": map[string]string{"token": "ghp_xxx"},
	})
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400 body %s", rec.Code, rec.Body.String())
	}

	token := "ghp_plain_token_must_not_persist"
	rec = doRequestBody(t, h, http.MethodPost, "/api/sync/media", "good-token", map[string]any{
		"kind":       "github",
		"name":       "github",
		"credential": token,
		"settings":   map[string]string{"owner": "alice", "repo": "diary"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create status = %d body %s", rec.Code, rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), token) {
		t.Fatal("response leaked credential")
	}
	created := decode(t, rec)
	if created["has_credential"] != true {
		t.Fatalf("has_credential = %v", created["has_credential"])
	}
	if _, ok := created["secret"]; ok {
		t.Fatal("response exposed secret field")
	}
	if strings.Contains(rec.Body.String(), "ciphertext") {
		t.Fatal("response exposed ciphertext")
	}
}

func TestManualSyncWithoutEngineReturnsState(t *testing.T) {
	h := newHandler(t)
	rec := doRequestBody(t, h, http.MethodPost, "/api/sync/media", "good-token", map[string]any{
		"kind":       "github",
		"name":       "github",
		"credential": "ghp_x",
		"settings":   map[string]string{"owner": "alice", "repo": "diary"},
	})
	id, _ := decode(t, rec)["id"].(string)
	rec = doRequest(t, h, http.MethodPost, "/api/sync/media/"+id+"/sync", "good-token")
	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 body %s", rec.Code, rec.Body.String())
	}
	body := decode(t, rec)
	if strings.Contains(rec.Body.String(), "ghp_x") {
		t.Fatal("error response leaked credential")
	}
	state, _ := body["state"].(map[string]any)
	if state["status"] != "failed" {
		t.Fatalf("state = %v", state)
	}
}

func TestSyncMediaRequiresAuth(t *testing.T) {
	h := newHandler(t)
	rec := doRequest(t, h, http.MethodGet, "/api/sync/media", "")
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rec.Code)
	}
}

type funcRunner func(context.Context, *filesystem.Service, string) error

func (f funcRunner) SyncAll(ctx context.Context, fs *filesystem.Service, mediumID string) error {
	return f(ctx, fs, mediumID)
}
