package tools

import (
	"encoding/json"

	"github.com/mark3labs/mcp-go/mcp"
)

// Result renders v as both structured content and a pretty-printed JSON text
// fallback so every MCP client gets a readable response.
func Result(v any) *mcp.CallToolResult {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return mcp.NewToolResultError(err.Error())
	}
	return mcp.NewToolResultStructured(v, string(b))
}

// Failure converts an error into a tool-level error result. The MCP transport
// treats these as successful RPC calls carrying isError=true, which is what
// models expect for recoverable problems.
func Failure(err error) (*mcp.CallToolResult, error) {
	return mcp.NewToolResultError(err.Error()), nil
}
