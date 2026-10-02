// Package fstools exposes the sandboxed filesystem as a set of MCP tools.
package fs

import (
	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp/tools"
)

// All returns every filesystem tool bound to the supplied workspace provider.
func All(workspaces tools.WorkspaceProvider) []tools.Tool {
	return []tools.Tool{
		readFileTool{workspaces: workspaces},
		writeFileTool{workspaces: workspaces},
		appendFileTool{workspaces: workspaces},
		listDirectoryTool{workspaces: workspaces},
		fileInfoTool{workspaces: workspaces},
		createDirectoryTool{workspaces: workspaces},
		deletePathTool{workspaces: workspaces},
	}
}
