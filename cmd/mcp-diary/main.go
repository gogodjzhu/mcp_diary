// Command mcp-diary runs an MCP server that exposes a sandboxed file workspace
// over the Streamable HTTP transport (or stdio).
package main

import (
	"fmt"
	"os"

	"github.com/gogodjzhu/mcp-diary/internal/cli"
)

// version is overridden at build time via -ldflags.
var version = "dev"

func main() {
	if err := cli.NewRootCommand(version).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
