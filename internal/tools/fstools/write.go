package fstools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/mark3labs/mcp-go/mcp"
)

type writeFileTool struct{ workspaces tools.WorkspaceProvider }

func (t writeFileTool) Name() string { return "write_file" }

func (t writeFileTool) Definition() mcp.Tool {
	return mcp.NewTool(
		t.Name(),
		mcp.WithDescription("Create or overwrite a file inside the workspace with the given text content."),
		mcp.WithDestructiveHintAnnotation(true),
		mcp.WithString("path",
			mcp.Required(),
			mcp.Description("Path to the file, absolute within the workspace or relative to its root."),
		),
		mcp.WithString("content",
			mcp.Required(),
			mcp.Description("Full text content to write."),
		),
		mcp.WithBoolean("create_dirs",
			mcp.Description("Create missing parent directories. Defaults to true."),
		),
		mcp.WithBoolean("overwrite",
			mcp.Description("Allow replacing an existing file. Defaults to true."),
		),
	)
}

func (t writeFileTool) Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error) {
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

	result, err := fs.Write(ctx, path, content, filesystem.WriteOptions{
		CreateDirs:   request.GetBool("create_dirs", true),
		Overwrite:    request.GetBool("overwrite", true),
		OverwriteSet: true,
	})
	if err != nil {
		return tools.Failure(err)
	}

	return tools.Result(result), nil
}
