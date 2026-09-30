package tools

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/filesystem"
)

// WorkspaceProvider resolves the sandboxed filesystem a tool call may operate
// on. Implementing it per request (rather than binding a single filesystem at
// startup) is what allows authenticated users to be isolated from one another.
type WorkspaceProvider interface {
	Filesystem(ctx context.Context) (*filesystem.Service, error)
}
