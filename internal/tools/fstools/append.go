package fstools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

type appendFileTool struct{ workspaces tools.WorkspaceProvider }

func (t appendFileTool) Name() string { return "append_file" }

func (t appendFileTool) Definition() mcp.Tool {
	return mcp.NewTool(
		t.Name(),
		mcp.WithDescription("Append text to a file inside the workspace, creating it when it does not exist."),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to the file, absolute within the workspace or relative to its root."),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Text content to append."),
		),
		mcp.WithBoolean("create_dirs",
			mcp.Description("Create missing parent directories. Defaults to true."),
		),
	)
}

func (t appendFileTool) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := request.RequireString("path")
	if err != nil {
		return tools.Failure(err)
	}
	content, err := request.RequireString("content")
	if err != nil {
		return tools.Failure(err)
	}

	fs, err := t.workspaces.Filesystem(ctx)
	if err != nil {
		return tools.Failure(err)
	}

	result, err := fs.Append(ctx, path, content, filesystem.WriteOptions{
		CreateDirs: request.GetBool("create_dirs", true),
	})
	if err != nil {
		return tools.Failure(err)
	}

	return tools.Result(result), nil
}
