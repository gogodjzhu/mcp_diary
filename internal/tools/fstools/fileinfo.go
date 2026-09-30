package fstools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

type fileInfoTool struct{ workspaces tools.WorkspaceProvider }

func (t fileInfoTool) Name() string { return "file_info" }

func (t fileInfoTool) Definition() mcp.Tool {
	return mcp.NewTool(
		t.Name(),
		mcp.WithDescription("Return metadata (size, mode, modification time, type) for a path inside the workspace."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to inspect, absolute within the workspace or relative to its root."),
		),
	)
}

func (t fileInfoTool) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := request.RequireString("path")
	if err != nil {
		return tools.Failure(err)
	}

	fs, err := t.workspaces.Filesystem(ctx)
	if err != nil {
		return tools.Failure(err)
	}

	info, err := fs.Stat(ctx, path)
	if err != nil {
		return tools.Failure(err)
	}

	return tools.Result(info), nil
}
