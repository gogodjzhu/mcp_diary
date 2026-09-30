package fstools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

type deletePathTool struct{ workspaces tools.WorkspaceProvider }

func (t deletePathTool) Name() string { return "delete_path" }

func (t deletePathTool) Definition() mcp.Tool {
	return mcp.NewTool(
		t.Name(),
		mcp.WithDescription("Delete a file or directory inside the workspace. Deleting the workspace root is refused."),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to delete, absolute within the workspace or relative to its root."),
		),
		mcp.WithBoolean("recursive",
			mcp.Description("Required to delete a directory and all of its contents. Defaults to false."),
		),
	)
}

func (t deletePathTool) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := request.RequireString("path")
	if err != nil {
		return tools.Failure(err)
	}

	fs, err := t.workspaces.Filesystem(ctx)
	if err != nil {
		return tools.Failure(err)
	}

	if err := fs.Delete(ctx, path, request.GetBool("recursive", false)); err != nil {
		return tools.Failure(err)
	}

	return tools.Result(map[string]any{"path": path, "deleted": true}), nil
}
