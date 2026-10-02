// Package mcp is the MCP access layer: it assembles the MCP server, binds the
// tool registry and exposes the protocol over the Streamable HTTP transport (or
// stdio). Everything specific to the MCP protocol lives here; the shared
// filesystem, workspace and auth logic lives outside this package.
package mcp

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/gogodjzhu/mcp-diary/internal/config"
	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/gogodjzhu/mcp-diary/internal/workspace"
	"github.com/mark3labs/mcp-go/server"
)

// Server is the MCP access layer: a fully assembled MCP server plus the HTTP
// handlers that expose it.
type Server struct {
	cfg        config.Config
	logger     *slog.Logger
	mcp        *server.MCPServer
	registry   *tools.Registry
	workspaces *workspace.Manager
}

// New builds the MCP server from the configuration, the resolved workspaces and
// the tool registry.
func New(cfg config.Config, workspaces *workspace.Manager, registry *tools.Registry, logger *slog.Logger) *Server {
	s := &Server{
		cfg:        cfg,
		logger:     logger,
		registry:   registry,
		workspaces: workspaces,
	}

	s.mcp = server.NewMCPServer(
		cfg.Name,
		cfg.Version,
		server.WithTitle(cfg.Name),
		server.WithInstructions(instructions(workspaces)),
		server.WithToolCapabilities(true),
		server.WithRecovery(),
		server.WithLogging(),
	)
	registry.Bind(s.mcp)

	return s
}

// MCPServer returns the underlying MCP server.
func (s *Server) MCPServer() *server.MCPServer { return s.mcp }

// ToolNames returns the names of the registered tools.
func (s *Server) ToolNames() []string { return s.registry.Names() }

// ServeStdio serves MCP over stdin/stdout. The stdio server owns process stdio
// and cannot be shut down gracefully, so cancellation simply releases the
// caller.
func (s *Server) ServeStdio(ctx context.Context) error {
	errCh := make(chan error, 1)
	go func() { errCh <- server.ServeStdio(s.mcp) }()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func instructions(workspaces *workspace.Manager) string {
	mode := "read and write"
	if workspaces.ReadOnly() {
		mode = "read-only"
	}
	diaryGuide := "Use the diary tools to record daily journals as plain text: createDiarySession, appendDiarySession, getDiarySession, updateDiarySession, then commitDiarySession only after the user confirms. Attach images or videos with attachDiaryMedia (workspace-relative source_path) and remove them with removeDiaryAttachment. Do not invent structured fields. After commit, use getDiaryEntry, listDiaryEntries, updateDiaryEntry, and deleteDiaryEntry. deleteDiaryEntry requires explicit user confirmation."
	if workspaces.PerUser() {
		return fmt.Sprintf(
			"You are connected to a private, authenticated diary and file workspace with %s access. "+
				"The workspace belongs to the authenticated user and is fully isolated from other users. "+
				"All paths are resolved inside it; attempts to escape are rejected. %s",
			mode, diaryGuide,
		)
	}
	return fmt.Sprintf(
		"You are connected to a diary and file workspace rooted at %q with %s access. "+
			"All paths are resolved inside this root; attempts to escape it are rejected. "+
			"Use the filesystem tools to inspect, read and modify files. %s",
		workspaces.Root(), mode, diaryGuide,
	)
}
