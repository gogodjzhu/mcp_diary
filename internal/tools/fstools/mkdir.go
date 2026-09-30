package fstools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

type createDirectoryTool struct{ workspaces tools.WorkspaceProvider }

func (t createDirectoryTool) Name() string { return "create_directory" }

func (t createDirectoryTool) Definition() mcp.Tool {
	return mcp.NewTool(
		t.Name(),
		mcp.WithDescription("Create a directory inside the workspace."),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Directory to create, absolute within the workspace or relative to its root."),
		),
		mcp.WithBoolean("all",
			mcp.Description("Create missing parent directories and accept an existing directory. Defaults to true."),
		),
	)
}

func (t createDirectoryTool) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := request.RequireString("path")
	if err != nil {
		return tools.Failure(err)
	}

	fs, err := t.workspaces.Filesystem(ctx)
	if err != nil {
		return tools.Failure(err)
	}

	if err := fs.Mkdir(ctx, path, request.GetBool("all", true)); err != nil {
		return tools.Failure(err)
	}

	return tools.Result(map[string]any{"path": path, "created": true}), nil
}
