// Package tools contains the MCP tool implementations exposed by the server.
//
// Every tool implements the Tool interface and can be registered on an
// *server.MCPServer through the Registry. New tools only need to be added to
// the registry to become available.
package tools

import (
	"context"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Tool is a single MCP tool: its definition plus its handler.
type Tool interface {
	// Name is the unique tool name advertised to clients.
	Name() string
	// Definition returns the MCP tool metadata and input schema.
	Definition() mcp.Tool
	// Handle executes the tool for a single call.
	Handle(ctx context.Context, request mcp.CallToolRequest) (*mcp.CallToolResult, error)
}

// Registry holds the set of tools the server exposes.
type Registry struct {
	tools []Tool
	index map[string]struct{}
}

// NewRegistry creates a registry, optionally pre-populated with tools.
func NewRegistry(items ...Tool) *Registry {
	r := &Registry{index: make(map[string]struct{})}
	r.Add(items...)
	return r
}

// Add registers tools, ignoring any that were already added.
func (r *Registry) Add(items ...Tool) {
	for _, t := range items {
		if t == nil {
			continue
		}
		if _, exists := r.index[t.Name()]; exists {
			continue
		}
		r.index[t.Name()] = struct{}{}
		r.tools = append(r.tools, t)
	}
}

// Bind registers every tool on the MCP server.
func (r *Registry) Bind(s *server.MCPServer) {
	for _, t := range r.tools {
		s.AddTool(t.Definition(), t.Handle)
	}
}

// Tools returns the registered tools in insertion order.
func (r *Registry) Tools() []Tool { return r.tools }

// Names returns the names of the registered tools in insertion order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for _, t := range r.tools {
		names = append(names, t.Name())
	}
	return names
}
