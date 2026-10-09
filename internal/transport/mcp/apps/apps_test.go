package apps

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestWidgetHTMLIsSelfContained(t *testing.T) {
	if WidgetHTML == "" {
		t.Fatal("embedded widget HTML is empty")
	}
	if !strings.Contains(WidgetHTML, "<!DOCTYPE html>") {
		t.Fatal("widget is not an HTML5 document")
	}
	if !strings.Contains(WidgetHTML, "ui/notifications/tool-result") {
		t.Fatal("widget does not listen for tool-result notifications")
	}
	lower := strings.ToLower(WidgetHTML)
	for _, needle := range []string{
		"src=\"http",
		"src='http",
		"href=\"http",
		"href='http",
		"fetch(",
		"xmlhttprequest",
		"new websocket",
	} {
		if strings.Contains(lower, needle) {
			t.Fatalf("widget must not load external resources; found %q", needle)
		}
	}
}

func TestToolMetaSerializesResourceURI(t *testing.T) {
	tool := WithWidget(mcp.NewTool("listDiaryEntries"))
	b, err := json.Marshal(tool)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	meta, _ := raw["_meta"].(map[string]any)
	ui, _ := meta["ui"].(map[string]any)
	if ui["resourceUri"] != WidgetURI {
		t.Fatalf("_meta.ui.resourceUri = %v, want %s", ui["resourceUri"], WidgetURI)
	}
}
