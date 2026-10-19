// Package tools contains the MCP tool implementations exposed by the server.
//
// Every tool implements the Tool interface and can be registered on an
// *server.MCPServer through the Registry. New tools only need to be added to
// the registry to become available.
package tools

import (
	"context"
	"strings"

	ui "github.com/gogodjzhu/mcp-diary/internal/transport/mcp/ui"
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

// MetaProvider is implemented by tools that advertise extra _meta, such as an
// MCP Apps UI resource. The registry merges it into the definition on bind, so
// tools keep their plain definition in tests and the CLI listing.
type MetaProvider interface {
	Meta() map[string]any
}

// Registry holds the set of tools the server exposes.
type Registry struct {
	tools  []Tool
	index  map[string]struct{}
	views  []ui.View
	origin string
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
		s.AddTool(r.definition(t), t.Handle)
	}
	for _, view := range r.views {
		res := resource(view, r.origin)
		s.AddResource(res.Resource, res.Handler)
	}
}

// definition returns the tool definition with its _meta applied. The tool's
// own metadata wins; a view that lists the tool adds the UI binding (and the
// ChatGPT alias) unless the tool already declares one. Tools a view may call
// but the model must not see are marked private.
func (r *Registry) definition(t Tool) mcp.Tool {
	def := t.Definition()

	merged := make(map[string]any)
	if provider, ok := t.(MetaProvider); ok {
		for k, v := range provider.Meta() {
			merged[k] = v
		}
	}
	if meta, ok := ui.ToolMetas()[t.Name()]; ok {
		if _, declared := merged["ui"]; !declared {
			for k, v := range meta {
				merged[k] = v
			}
			if box, ok := merged["ui"].(map[string]any); ok {
				merged["ui"] = cloneMap(box)
			}
		}
	}
	if meta, ok := ui.RegistryVisibility()[t.Name()]; ok {
		// The view declares visibility; merge it into whatever ui box the
		// tool already has so the resource binding survives.
		box, _ := merged["ui"].(map[string]any)
		box = cloneMap(box)
		for k, v := range meta["ui"].(map[string]any) {
			box[k] = v
		}
		merged["ui"] = box
		for k, v := range meta {
			if k != "ui" {
				merged[k] = v
			}
		}
	}
	if len(merged) == 0 {
		return def
	}
	merged = withDomain(merged, r.origin)
	def.Meta = mcp.NewMetaFromMap(merged)
	return def
}

func cloneMap(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+1)
	for k, v := range in {
		out[k] = v
	}
	return out
}

// withDomain adds the public origin to the ui metadata when known.
func withDomain(meta map[string]any, origin string) map[string]any {
	if origin == "" {
		return meta
	}
	box, ok := meta["ui"].(map[string]any)
	if !ok {
		return meta
	}
	out := make(map[string]any, len(box)+1)
	for k, v := range box {
		out[k] = v
	}
	out["domain"] = origin
	clone := make(map[string]any, len(meta))
	for k, v := range meta {
		clone[k] = v
	}
	clone["ui"] = out
	return clone
}

// Tools returns the registered tools in insertion order.
func (r *Registry) Tools() []Tool { return r.tools }

// SetOrigin sets the public origin (scheme://host) of this server. It is
// advertised to UI hosts as the domain that serves the views. Empty means
// unknown (for example stdio); the views still work locally.
func (r *Registry) SetOrigin(origin string) { r.origin = origin }

// Origin returns the configured public origin.
func (r *Registry) Origin() string { return r.origin }

// Views returns the registered UI views in registration order.
func (r *Registry) Views() []ui.View { return r.views }

// AddViews registers the UI views served as MCP resources. Tools tagged by a
// view (ui.ToolMetas) start rendering it once bound: the UI binding lives in
// the package owning the views, so tool implementations stay view-agnostic.
func (r *Registry) AddViews(views ...ui.View) {
	for _, view := range views {
		if !strings.HasPrefix(view.URI, "ui://") {
			continue
		}
		r.views = append(r.views, view)
	}
}

// Tool returns the definition the registry would advertise for name, with its
// UI metadata applied (as Bind does).
func (r *Registry) Tool(name string) *mcp.Tool {
	for _, t := range r.tools {
		if t.Name() == name {
			def := r.definition(t)
			return &def
		}
	}
	return nil
}

// Names returns the names of the registered tools in insertion order.
func (r *Registry) Names() []string {
	names := make([]string, 0, len(r.tools))
	for _, t := range r.tools {
		names = append(names, t.Name())
	}
	return names
}

// resource turns a UI view into an MCP resource whose contents are the
// self-contained HTML document.
func resource(view ui.View, origin string) server.ServerResource {
	html, err := ui.Render(view.HTML)
	if err != nil {
		html = view.HTML
	}
	meta := view.Meta()
	if origin != "" {
		if box, ok := meta["ui"].(map[string]any); ok {
			withDomain := make(map[string]any, len(box)+1)
			for k, v := range box {
				withDomain[k] = v
			}
			withDomain["domain"] = origin
			meta["ui"] = withDomain
		}
	}

	res := mcp.NewResource(view.URI, view.Name, mcp.WithMIMEType(ui.ResourceMIMEType))
	res.Description = view.Description
	res.Meta = mcp.NewMetaFromMap(meta)

	return server.ServerResource{
		Resource: res,
		Handler: func(_ context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      request.Params.URI,
					MIMEType: ui.ResourceMIMEType,
					Text:     string(html),
					Meta:     meta,
				},
			}, nil
		},
	}
}
