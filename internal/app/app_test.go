package app_test

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/app"
	"github.com/gogodjzhu/mcp-diary/internal/config"
	"github.com/gogodjzhu/mcp-diary/internal/logging"
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
	for _, want := range []string{"read_file", "write_file", "append_file", "list_directory"} {
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
