package widget

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func TestHTMLIsSelfContained(t *testing.T) {
	if !strings.HasPrefix(strings.TrimSpace(HTML), "<!DOCTYPE html>") {
		t.Fatalf("widget HTML must be a complete HTML5 document")
	}
	lower := strings.ToLower(HTML)
	for _, needle := range []string{`src="http`, `href="http`, `src='http`, `href='http`} {
		if strings.Contains(lower, needle) {
			t.Fatalf("widget HTML must not load external resources, found %q", needle)
		}
	}
	if !strings.Contains(HTML, "ui/notifications/tool-result") {
		t.Fatal("widget must listen for ui/notifications/tool-result")
	}
	if !strings.Contains(HTML, "structuredContent") {
		t.Fatal("widget must render structuredContent")
	}
}

func TestToolMetaResourceURI(t *testing.T) {
	raw, err := json.Marshal(mcp.Tool{Name: "listDiaryEntries", Meta: ToolMeta()})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	meta, _ := decoded["_meta"].(map[string]any)
	ui, _ := meta["ui"].(map[string]any)
	if ui["resourceUri"] != URI {
		t.Fatalf("_meta.ui.resourceUri = %#v, want %q", ui["resourceUri"], URI)
	}
}

func TestReadReturnsMCPAppHTML(t *testing.T) {
	contents, err := Read(context.Background(), mcp.ReadResourceRequest{})
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if len(contents) != 1 {
		t.Fatalf("len(contents) = %d, want 1", len(contents))
	}
	text, ok := contents[0].(mcp.TextResourceContents)
	if !ok {
		t.Fatalf("contents[0] is %T, want TextResourceContents", contents[0])
	}
	if text.URI != URI {
		t.Fatalf("URI = %q, want %q", text.URI, URI)
	}
	if text.MIMEType != MIMEType {
		t.Fatalf("MIMEType = %q, want %q", text.MIMEType, MIMEType)
	}
	if text.Text != HTML {
		t.Fatal("resource text must match the embedded widget HTML")
	}
}
