package fstools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

type readFileTool struct{ workspaces tools.WorkspaceProvider }

func (t readFileTool) Name() string { return "read_file" }

func (t readFileTool) Definition() mcp.Tool {
	return mcp.NewTool(
		t.Name(),
		mcp.WithDescription("Read the contents of a file inside the workspace. Binary-safe but returned as UTF-8 text; use offset and limit for large files."),
		mcp.WithReadOnlyHintAnnotation(true),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to the file, absolute within the workspace or relative to its root."),
		),
		mcp.WithInteger("offset",
			mcp.Description("Byte offset to start reading from. Defaults to 0."),
		),
		mcp.WithInteger("limit",
			mcp.Description("Maximum number of bytes to return. Defaults to the server maximum."),
		),
	)
}

func (t readFileTool) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	path, err := request.RequireString("path")
	if err != nil {
		return tools.Failure(err)
	}

	fs, err := t.workspaces.Filesystem(ctx)
	if err != nil {
		return tools.Failure(err)
	}

	content, err := fs.Read(ctx, path, int64(request.GetInt("offset", 0)), int64(request.GetInt("limit", 0)))
	if err != nil {
		return tools.Failure(err)
	}

	return tools.Result(content), nil
}
