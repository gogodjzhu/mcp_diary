// Package cli implements the command line interface.
package cli

import (
	"github.com/spf13/cobra"
)

// NewRootCommand builds the root command tree.
func NewRootCommand(version string) *cobra.Command {
	var (
		logLevel  string
		logFormat string
	)

	root := &cobra.Command{
		Use:   "mcp-diary",
		Short: "A Streamable HTTP MCP server for diary storage",
		Long: "mcp-diary is an MCP (Model Context Protocol) server that exposes diary\n" +
			"session/entry tools over Streamable HTTP (or stdio).",
		SilenceUsage:  true,
		SilenceErrors: true,
	}

	root.PersistentFlags().StringVar(&logLevel, "log-level", "info", "log level: debug, info, warn, error")
	root.PersistentFlags().StringVar(&logFormat, "log-format", "text", "log format: text, json")

	root.AddCommand(
		newServeCommand(version, &logLevel, &logFormat),
		newToolsCommand(),
		newVersionCommand(version),
	)

	return root
}
