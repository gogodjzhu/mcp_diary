package app_test

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/app"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/gogodjzhu/mcp-diary/internal/platform/logging"
	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/mcp"
)

func callTool(t *testing.T, ctx context.Context, c *client.Client, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()

	req := mcp.CallToolRequest{}
	req.Params.Name = name
	req.Params.Arguments = args

	result, err := c.CallTool(ctx, req)
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if result.IsError {
		t.Fatalf("CallTool(%s) returned tool error: %s", name, textOf(result))
	}
	return result
}

func textOf(result *mcp.CallToolResult) string {
	var b strings.Builder
	for _, content := range result.Content {
		if text, ok := content.(mcp.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func TestStreamableHTTPEndToEnd(t *testing.T) {
	root := t.TempDir()

	cfg := config.Default()
	cfg.Root = root
	cfg.Version = "test"
	cfg.LogLevel = "error"

	logger, err := logging.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}

	application, err := app.New(cfg, logger)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL + cfg.EndpointPath)
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

	tools, err := c.ListTools(ctx, mcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	names := make(map[string]bool)
	for _, tool := range tools.Tools {
		names[tool.Name] = true
	}
	for _, want := range []string{"read_file", "write_file", "append_file", "list_directory", "createDiarySession", "commitDiarySession", "listDiaryEntries"} {
		if !names[want] {
			t.Fatalf("tool %q not listed; got %v", want, names)
		}
	}

	callTool(t, ctx, c, "write_file", map[string]any{
		"path":    "diary/entry.txt",
		"content": "first line",
	})
	callTool(t, ctx, c, "append_file", map[string]any{
		"path":    "diary/entry.txt",
		"content": "\nsecond line",
	})

	read := callTool(t, ctx, c, "read_file", map[string]any{"path": "diary/entry.txt"})
	if got := textOf(read); !strings.Contains(got, "first line") || !strings.Contains(got, "second line") {
		t.Fatalf("read_file text missing content: %s", got)
	}

	onDisk, err := os.ReadFile(filepath.Join(root, "diary", "entry.txt"))
	if err != nil {
		t.Fatalf("reading file on disk: %v", err)
	}
	if string(onDisk) != "first line\nsecond line" {
		t.Fatalf("on-disk content = %q", onDisk)
	}

	list := callTool(t, ctx, c, "list_directory", map[string]any{"path": "diary"})
	if got := textOf(list); !strings.Contains(got, "entry.txt") {
		t.Fatalf("list_directory did not return entry.txt: %s", got)
	}
}

func TestHTTPHealthz(t *testing.T) {
	cfg := config.Default()
	cfg.Root = t.TempDir()
	cfg.LogLevel = "error"

	logger, _ := logging.New(cfg.LogLevel, cfg.LogFormat)
	application, err := app.New(cfg, logger)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	resp, err := ts.Client().Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
}

func envelopeOf(t *testing.T, result *mcp.CallToolResult) map[string]any {
	t.Helper()
	var payload map[string]any
	if result.StructuredContent != nil {
		b, err := json.Marshal(result.StructuredContent)
		if err != nil {
			t.Fatalf("marshal structured: %v", err)
		}
		if err := json.Unmarshal(b, &payload); err != nil {
			t.Fatalf("decode structured: %v", err)
		}
		return payload
	}
	if err := json.Unmarshal([]byte(textOf(result)), &payload); err != nil {
		t.Fatalf("decode text %q: %v", textOf(result), err)
	}
	return payload
}

func TestDiaryMCPEndToEnd(t *testing.T) {
	root := t.TempDir()

	cfg := config.Default()
	cfg.Root = root
	cfg.Version = "test"
	cfg.LogLevel = "error"

	logger, err := logging.New(cfg.LogLevel, cfg.LogFormat)
	if err != nil {
		t.Fatalf("logging.New: %v", err)
	}
	application, err := app.New(cfg, logger)
	if err != nil {
		t.Fatalf("app.New: %v", err)
	}

	ts := httptest.NewServer(application.HTTPHandler())
	defer ts.Close()

	c, err := client.NewStreamableHttpClient(ts.URL + cfg.EndpointPath)
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

	created := envelopeOf(t, callTool(t, ctx, c, "createDiarySession", map[string]any{
		"request_id": "req_001",
		"diary_date": "2026-10-01",
	}))
	if created["code"].(float64) != 0 {
		t.Fatalf("create code = %v message=%v", created["code"], created["message"])
	}
	data := created["data"].(map[string]any)
	sessionID := data["session_id"].(string)
	if data["status"] != "draft" {
		t.Fatalf("status = %v", data["status"])
	}

	appended := envelopeOf(t, callTool(t, ctx, c, "appendDiarySession", map[string]any{
		"request_id":        "req_002",
		"session_id":        sessionID,
		"expected_revision": 1,
		"content":           "今天开会时需求反复调整，我感到沮丧。",
	}))
	if appended["code"].(float64) != 0 {
		t.Fatalf("append failed: %v", appended)
	}

	conflict := envelopeOf(t, callTool(t, ctx, c, "appendDiarySession", map[string]any{
		"request_id":        "req_002b",
		"session_id":        sessionID,
		"expected_revision": 1,
		"content":           "stale",
	}))
	if conflict["code"].(float64) != 409 {
		t.Fatalf("want 409, got %v", conflict)
	}

	committed := envelopeOf(t, callTool(t, ctx, c, "commitDiarySession", map[string]any{
		"request_id":        "req_003",
		"session_id":        sessionID,
		"expected_revision": 2,
	}))
	if committed["code"].(float64) != 0 {
		t.Fatalf("commit failed: %v", committed)
	}
	entryID := committed["data"].(map[string]any)["entry_id"].(string)

	replay := envelopeOf(t, callTool(t, ctx, c, "commitDiarySession", map[string]any{
		"request_id":        "req_003",
		"session_id":        sessionID,
		"expected_revision": 2,
	}))
	if replay["data"].(map[string]any)["entry_id"] != entryID {
		t.Fatalf("idempotent commit created a new entry")
	}

	got := envelopeOf(t, callTool(t, ctx, c, "getDiaryEntry", map[string]any{
		"request_id": "req_004",
		"entry_id":   entryID,
	}))
	if got["data"].(map[string]any)["content"] != "今天开会时需求反复调整，我感到沮丧。" {
		t.Fatalf("entry content = %v", got["data"])
	}

	listed := envelopeOf(t, callTool(t, ctx, c, "listDiaryEntries", map[string]any{
		"request_id":      "req_005",
		"diary_date_from": "2026-10-01",
		"diary_date_to":   "2026-10-01",
	}))
	if listed["data"].(map[string]any)["total"].(float64) != 1 {
		t.Fatalf("list total = %v", listed["data"])
	}

	deleted := envelopeOf(t, callTool(t, ctx, c, "deleteDiaryEntry", map[string]any{
		"request_id":        "req_006",
		"entry_id":          entryID,
		"expected_revision": 1,
	}))
	if deleted["data"].(map[string]any)["deleted"] != true {
		t.Fatalf("delete = %v", deleted)
	}
}
