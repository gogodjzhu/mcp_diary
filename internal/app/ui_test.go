package app_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp/tools"
	ui "github.com/gogodjzhu/mcp-diary/internal/transport/mcp/ui"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// uiTool is a minimal tool used to exercise the registry UI binding.
type uiTool struct{ name string }

func (t uiTool) Name() string         { return t.name }
func (t uiTool) Definition() mcp.Tool { return mcp.NewTool(t.name) }
func (t uiTool) Handle(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultText("ok"), nil
}

func TestRegistryBindsUIViews(t *testing.T) {
	registry := tools.NewRegistry(uiTool{name: "renderDiaryEntries"}, uiTool{name: "getDiaryEntry"}, uiTool{name: "listDiaryEntries"})
	registry.AddViews(ui.Views()...)

	if got := len(registry.Views()); got != 2 {
		t.Fatalf("views = %d, want 2", got)
	}

	def := registry.Tool("renderDiaryEntries")
	if def == nil {
		t.Fatal("renderDiaryEntries definition missing")
	}
	b, err := def.MarshalJSON()
	if err != nil {
		t.Fatalf("marshal tool: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("decode tool: %v", err)
	}
	meta, ok := raw["_meta"].(map[string]any)
	if !ok {
		t.Fatalf("_meta missing: %s", b)
	}
	box, ok := meta["ui"].(map[string]any)
	if !ok || box["resourceUri"] != ui.EntriesURI {
		t.Fatalf("ui.resourceUri = %v, want %s", meta["ui"], ui.EntriesURI)
	}
	if meta["openai/outputTemplate"] != ui.EntriesURI {
		t.Fatalf("openai/outputTemplate = %v", meta["openai/outputTemplate"])
	}

	// Data tools keep a clean definition.
	plain := registry.Tool("listDiaryEntries")
	if plain == nil {
		t.Fatal("listDiaryEntries definition missing")
	}
	if plain.Meta != nil {
		t.Fatal("listDiaryEntries must not advertise UI metadata")
	}
}

func TestRegistryServesUIResource(t *testing.T) {
	registry := tools.NewRegistry()
	registry.AddViews(ui.Views()...)
	registry.SetOrigin("https://mcp.example.com")

	srv := server.NewMCPServer("test", "1", server.WithResourceCapabilities(false, false))
	registry.Bind(srv)

	entry, ok := srv.ListResources()[ui.EntriesURI]
	if !ok {
		t.Fatalf("resource %q not registered", ui.EntriesURI)
	}
	if entry.Resource.MIMEType != ui.ResourceMIMEType {
		t.Fatalf("mime type = %q, want %q", entry.Resource.MIMEType, ui.ResourceMIMEType)
	}
	requirement := mcp.ReadResourceRequest{}
	requirement.Params.URI = ui.EntriesURI
	contents, err := entry.Handler(t.Context(), requirement)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("contents = %d, want 1", len(contents))
	}
	text, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("contents type = %T", contents[0])
	}
	if !strings.Contains(text.Text, "window.diary") {
		t.Error("served HTML does not contain the bridge")
	}
	if strings.Contains(text.Text, "/* BRIDGE */") {
		t.Error("bridge placeholder was not replaced")
	}
	if text.Meta == nil {
		t.Fatal("contents _meta missing")
	}
	box, _ := text.Meta["ui"].(map[string]any)
	if box["domain"] != "https://mcp.example.com" {
		t.Fatalf("domain = %v", box["domain"])
	}
}
