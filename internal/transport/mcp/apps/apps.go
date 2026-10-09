// Package apps registers the MCP Apps (SEP-1865) diary widget.
package apps

import (
	"context"
	_ "embed"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

const (
	// WidgetURI is the ui:// resource hosts fetch to render diary tool results.
	WidgetURI = "ui://diary/widget"
	// WidgetMIME is the MCP Apps HTML profile required by SEP-1865.
	WidgetMIME = "text/html;profile=mcp-app"
)

//go:embed widget.html
var WidgetHTML string

// Resource is the diary widget advertised over resources/list.
func Resource() mcp.Resource {
	return mcp.NewResource(
		WidgetURI,
		"Diary Widget",
		mcp.WithResourceDescription("Renders diary entries and drafts from tool structuredContent."),
		mcp.WithMIMEType(WidgetMIME),
	)
}

// ToolMeta links a tool to the diary widget via _meta.ui.resourceUri.
func ToolMeta() *mcp.Meta {
	return mcp.NewMetaFromMap(map[string]any{
		"ui": map[string]any{
			"resourceUri": WidgetURI,
		},
	})
}

// WithWidget copies t and attaches the diary widget metadata.
func WithWidget(t mcp.Tool) mcp.Tool {
	t.Meta = ToolMeta()
	return t
}

// Read returns the self-contained widget HTML.
func Read(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      WidgetURI,
			MIMEType: WidgetMIME,
			Text:     WidgetHTML,
		},
	}, nil
}

// Register advertises the diary widget resource on s.
func Register(s *server.MCPServer) {
	s.AddResource(Resource(), Read)
}
