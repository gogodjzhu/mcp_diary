// Package mcpui declares the optional interactive UI (MCP Apps) that the MCP
// layer returns for selected tools.
//
// The UI is a self-contained HTML document rendered by the host inside a
// sandboxed iframe. It communicates with the host through the MCP Apps bridge
// (JSON-RPC over postMessage): the host pushes tool input and results with
// ui/notifications/tool-input and ui/notifications/tool-result, and the UI can
// call tools back through tools/call. Nothing outside the package needs to know
// the protocol; the MCP layer only registers the resources and tags the tools
// that should render them.
//
// The UI is strictly optional: every tool keeps working without it, so
// clients that do not support MCP Apps get plain text and JSON as before.
package mcpui

import (
	"bytes"
	"embed"
)

const bridgeMarker = "/* BRIDGE */"

const (
	// EntriesURI is the UI resource shown for a list of committed diary
	// entries. Treat it as a cache key: publish a new URI when the HTML
	// changes in a breaking way.
	EntriesURI = "ui://mcp-diary/entries.html"

	// entryURI is the UI resource that renders a single committed diary entry.
	entryURI = "ui://mcp-diary/entry.html"

	// ResourceMIMEType is the MCP Apps UI MIME type.
	ResourceMIMEType = "text/html;profile=mcp-app"
)

//go:embed assets/entries.html assets/entry.html assets/bridge.js
var assets embed.FS

// View is one UI resource: where it is served, which tools render it and the
// HTML document itself.
type View struct {
	// URI is the ui:// resource URI that hosts fetch and cache.
	URI string
	// Name is the MCP resource name.
	Name string
	// Description is the MCP resource description.
	Description string
	// Tools lists the tool names that render this view.
	Tools []string
	// HTML is the document served for URI.
	HTML []byte
}

// Meta returns the _meta object a host reads to render the view in an MCP
// Apps compatible client. The UI is fully self-contained (inline CSS and JS),
// so the content security policy stays empty: no external origin is needed.
func (v View) Meta() map[string]any {
	return map[string]any{
		"ui": map[string]any{
			"prefersBorder": true,
			"csp": map[string]any{
				"connectDomains":  []string{},
				"resourceDomains": []string{},
			},
		},
	}
}

// ToolMeta returns the _meta object attached to a tool definition so the host
// associates the tool call with the view. Both the portable MCP Apps field and
// the ChatGPT compatibility alias are set.
func (v View) ToolMeta() map[string]any {
	return map[string]any{
		"ui":                    map[string]any{"resourceUri": v.URI},
		"openai/outputTemplate": v.URI,
	}
}

// Views returns the UI views in registration order.
func Views() []View {
	return []View{
		{
			URI:         EntriesURI,
			Name:        "diary-entries",
			Description: "Interactive list of committed diary entries.",
			Tools:       []string{"renderDiaryEntries"},
			HTML:        mustAsset("assets/entries.html"),
		},
		{
			URI:         entryURI,
			Name:        "diary-entry",
			Description: "Interactive reader for a single committed diary entry.",
			Tools:       []string{"getDiaryEntry"},
			HTML:        mustAsset("assets/entry.html"),
		},
	}
}

// ToolMetas returns the _meta to attach to each tool definition, keyed by tool
// name. Tools missing from the map render no UI.
func ToolMetas() map[string]map[string]any {
	out := make(map[string]map[string]any)
	for _, view := range Views() {
		for _, tool := range view.Tools {
			out[tool] = view.ToolMeta()
		}
	}
	return out
}

// Render inlines the shared bridge script into a view document. The bridge is
// kept in one file so both views share it, while the served HTML stays a single
// self-contained document with no external requests.
func Render(html []byte) ([]byte, error) {
	bridge := mustAsset("assets/bridge.js")
	if !bytes.Contains(html, []byte(bridgeMarker)) {
		return html, nil
	}
	return bytes.ReplaceAll(html, []byte(bridgeMarker), bridge), nil
}

func mustAsset(name string) []byte {
	b, err := assets.ReadFile(name)
	if err != nil {
		panic("mcpui: missing embedded asset " + name + ": " + err.Error())
	}
	return b
}
