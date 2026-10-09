package widget

import (
	"context"
	_ "embed"

	"github.com/mark3labs/mcp-go/mcp"
)

const (
	URI      = "ui://diary/widget"
	Name     = "diary_widget"
	MIMEType = "text/html;profile=mcp-app"
)

//go:embed widget.html
var HTML string

func Resource() mcp.Resource {
	return mcp.NewResource(
		URI,
		Name,
		mcp.WithMIMEType(MIMEType),
		mcp.WithResourceDescription("MCP Apps widget that renders diary entries and drafts from structured tool results."),
		mcp.WithResourceSize(int64(len(HTML))),
	)
}

func ToolMeta() *mcp.Meta {
	return mcp.NewMetaFromMap(map[string]any{
		"ui": map[string]any{
			"resourceUri": URI,
		},
	})
}

func Read(_ context.Context, _ mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
	return []mcp.ResourceContents{
		mcp.TextResourceContents{
			URI:      URI,
			MIMEType: MIMEType,
			Text:     HTML,
			Meta: map[string]any{
				"ui": map[string]any{
					"prefersBorder": true,
				},
			},
		},
	}, nil
}
