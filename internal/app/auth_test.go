package app_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/app"
	"github.com/gogodjzhu/mcp-diary/internal/config"
	"github.com/gogodjzhu/mcp-diary/internal/logging"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
)

func newTokenInfoServer(t *testing.T) *httptest.Server {
	t.Helper()
	claims := map[string]any{
		"sub":            "google-sub-1",
		"email":          "alice@example.com",
		"email_verified": "true",
		"aud":            "test-client",
		"scope":          "openid email profile",
		"exp":            strconv.FormatInt(time.Now().Add(time.Hour).Unix(), 10),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("access_token") != "good-token" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":             "invalid_token",
				"error_description": "bad token",
			})
			return
		}
		_ = json.NewEncoder(w).Encode(claims)
	}))
	t.Cleanup(server.Close)
	return server
}

func newAuthApp(t *testing.T, root string, tokenInfoURL string) *app.App {
	t.Helper()

	cfg := config.Default()
	cfg.Root = root
	cfg.Version = "test"
	cfg.LogLevel = "error"
	cfg.Auth.Enabled = true
	cfg.Auth.Issuer = "https://accounts.google.com"
	cfg.Auth.ClientID = "test-client"
	cfg.Auth.TokenInfoURL = tokenInfoURL
	cfg.Auth.UserInfoURL = ""
	cfg.Auth.UsersDir = "users"

	logger, err := logging.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	application, err := app.New(cfg, logger)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}
	return application
}

func TestAuthRequiredChallengeAndMetadata(t *testing.T) {
	root := t.TempDir()
	tokenInfo := newTokenInfoServer(t)
	application := newAuthApp(t, root, tokenInfo.URL)

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
	if !strings.Contains(challenge, "resource_metadata=") || !strings.Contains(challenge, `error="invalid_request"`) {
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
	if metadata["resource"] != ts.URL+"/mcp" {
		t.Fatalf("resource = %v, want %s/mcp", metadata["resource"], ts.URL)
	}
	servers, _ := metadata["authorization_servers"].([]any)
	if len(servers) != 1 || servers[0] != "https://accounts.google.com" {
		t.Fatalf("authorization_servers = %v", metadata["authorization_servers"])
	}
}

func TestAuthenticatedPerUserWorkspaceEndToEnd(t *testing.T) {
	root := t.TempDir()
	tokenInfo := newTokenInfoServer(t)
	application := newAuthApp(t, root, tokenInfo.URL)

	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(
		ts.URL+"/mcp",
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer good-token"}),
	)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = mcp.Implementation{Name: "test-client", Version: "1.0.0"}
	if _, err := c.Initialize(ctx, initRequest); err != nil {
		t.Fatalf("Initialize: %v", err)
	}

	callTool(t, ctx, c, "write_file", map[string]any{
		"path":    "diary.txt",
		"content": "alice private notes",
	})

	userFile := filepath.Join(root, "users", "alice@example.com", "diary.txt")
	data, err := os.ReadFile(userFile)
	if err != nil {
		t.Fatalf("expected per-user file at %s: %v", userFile, err)
	}
	if string(data) != "alice private notes" {
		t.Fatalf("user file content = %q", data)
	}

	if _, err := os.Stat(filepath.Join(root, "diary.txt")); !os.IsNotExist(err) {
		t.Fatalf("file must not leak into base root, stat err = %v", err)
	}
}

func TestWebRoutesServedWithAuth(t *testing.T) {
	root := t.TempDir()
	tokenInfo := newTokenInfoServer(t)
	application := newAuthApp(t, root, tokenInfo.URL)

	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	// The embedded UI is served at the root.
	resp, err := ts.Client().Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET /: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET / status = %d, want 200", resp.StatusCode)
	}

	// The API requires a bearer token.
	apiResp, err := ts.Client().Get(ts.URL + "/api/me")
	if err != nil {
		t.Fatalf("GET /api/me: %v", err)
	}
	defer apiResp.Body.Close()
	if apiResp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("GET /api/me status = %d, want 401", apiResp.StatusCode)
	}
}

func TestInvalidTokenRejectedOverMCP(t *testing.T) {
	root := t.TempDir()
	tokenInfo := newTokenInfoServer(t)
	application := newAuthApp(t, root, tokenInfo.URL)

	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(
		ts.URL+"/mcp",
		transport.WithHTTPHeaders(map[string]string{"Authorization": "Bearer wrong-token"}),
	)
	if err != nil {
		t.Fatalf("NewStreamableHttpClient: %v", err)
	}
	defer c.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := c.Start(ctx); err != nil {
		t.Fatalf("client.Start: %v", err)
	}
	_, err = c.Initialize(ctx, mcp.InitializeRequest{})
	if err == nil {
		t.Fatal("expected Initialize to fail with an invalid token")
	}
	if !strings.Contains(err.Error(), "401") && !strings.Contains(strings.ToLower(err.Error()), "unauthor") {
		t.Logf("Initialize error (accepted): %v", err)
	}
}
