package fstools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

type listDirectoryTool struct{ workspaces tools.WorkspaceProvider }

func (t listDirectoryTool) Name() string { return "list_directory" }

func (t listDirectoryTool) Definition() mcp.Tool {
	return mcp.NewTool(
		t.Name(),
		mcp.WithDescription("List the files and subdirectories of a directory inside the workspace."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("path",
			mcp.Description("Directory to list, absolute within the workspace or relative to its root. Defaults to the workspace root."),
		),
	)
}

func (t listDirectoryTool) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path := request.GetString("path", ".")

	fs, err := t.workspaces.Filesystem(ctx)
	if err != nil {
		return tools.Failure(err)
	}

	entries, err := fs.List(ctx, path)
	if err != nil {
		return tools.Failure(err)
	}

	return tools.Result(map[string]any{
		"path":    path,
		"count":   len(entries),
		"entries": entries,
	}), nil
}
