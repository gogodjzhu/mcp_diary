package app_test

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gogodjzhu/mcp-diary/internal/app"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/gogodjzhu/mcp-diary/internal/platform/logging"
)

const testPublicURL = "https://mcp.test"

func newAuthApp(t *testing.T) *app.App {
	t.Helper()

	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.Version = "test"
	cfg.LogLevel = "error"
	cfg.Auth.Enabled = true
	cfg.Auth.PublicURL = testPublicURL
	cfg.Auth.GoogleClientID = "google-client-id"
	cfg.Auth.GoogleClientSecret = "google-client-secret"

	logger, err := logging.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	application, err := app.New(cfg, logger)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	t.Cleanup(func() { _ = application.Close() })
	return application
}

func TestAuthRequiredChallengeAndMetadata(t *testing.T) {
	application := newAuthApp(t)
	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	body := `{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"curl","version":"1"}}}`
	resp, err := ts.Client().Post(ts.URL+"/mcp", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /mcp: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
	challenge := resp.Header.Get("WWW-Authenticate")
	if !strings.Contains(challenge, "resource_metadata=") {
		t.Fatalf("unexpected challenge: %q", challenge)
	}

	metaResp, err := ts.Client().Get(ts.URL + "/.well-known/oauth-protected-resource/mcp")
	if err != nil {
		t.Fatalf("GET metadata: %v", err)
	}
	defer metaResp.Body.Close()
	if metaResp.StatusCode != http.StatusOK {
		t.Fatalf("metadata status = %d, want 200", metaResp.StatusCode)
	}
	var metadata map[string]any
	if err := json.NewDecoder(metaResp.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	if metadata["resource"] != testPublicURL+"/mcp" {
		t.Fatalf("resource = %v, want %s/mcp", metadata["resource"], testPublicURL)
	}
	servers, _ := metadata["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != testPublicURL {
		t.Fatalf("authorization_servers = %v, want [%s]", metadata["authorization_servers"], testPublicURL)
	}
}

func TestAuthorizationServerMetadata(t *testing.T) {
	application := newAuthApp(t)
	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/.well-known/oauth-authorization-server")
	if err != nil {
		t.Fatalf("GET AS metadata: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var metadata map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		t.Fatalf("decode metadata: %v", err)
	}
	for _, field := range []string{"authorization_endpoint", "token_endpoint", "registration_endpoint"} {
		if metadata[field] == nil || metadata[field] == "" {
			t.Fatalf("metadata missing %q: %v", field, metadata)
		}
	}
}

func TestDynamicClientRegistration(t *testing.T) {
	application := newAuthApp(t)
	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	body := `{"client_name":"test-cli","redirect_uris":["http://127.0.0.1:19876/callback"],"grant_types":["authorization_code","refresh_token"],"response_types":["code"],"token_endpoint_auth_method":"none"}`
	resp, err := ts.Client().Post(ts.URL+"/oauth/register", "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST /oauth/register: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		t.Fatalf("registration status = %d, want 200/201 (body %s)", resp.StatusCode, readBody(t, resp))
	}
	var registered map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&registered); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	if id, _ := registered["client_id"].(string); id == "" {
		t.Fatalf("registration did not return a client_id: %v", registered)
	}
}

func TestWebRoutesServedWithAuth(t *testing.T) {
	application := newAuthApp(t)
	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}

	apiResp, err := ts.Client().Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/me status = %d, want 401", apiResp.StatusCode)
	}
}

func readBody(t *testing.T, resp *http.Response) string {
	t.Helper()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return string(data)
}
