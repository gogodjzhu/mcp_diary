package cli

import (
	"fmt"
	"text/tabwriter"

	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/gogodjzhu/mcp-diary/internal/diary"
	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/gogodjzhu/mcp-diary/internal/tools/diarytools"
	"github.com/gogodjzhu/mcp-diary/internal/tools/fstools"
	"github.com/gogodjzhu/mcp-diary/internal/workspace"
	"github.com/spf13/cobra"
)

func newToolsCommand() *cobra.Command {
	root := "."

	cmd := &cobra.Command{
		Use:   "tools",
		Short: "List the MCP tools exposed by this server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg := config.Default()

			workspaces, err := workspace.New(workspace.Config{
				Root:         root,
				ReadOnly:     cfg.ReadOnly,
				MaxReadBytes: cfg.MaxReadBytes,
			})
			if err != nil {
				return err
			}

			registry := tools.NewRegistry()
			registry.Add(diarytools.All(workspaces, diary.New(nil, nil))...)
			registry.Add(fstools.All(workspaces)...)

			w := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tDESCRIPTION")
			for _, t := range registry.Tools() {
				def := t.Definition()
				fmt.Fprintf(w, "%s\t%s\n", def.Name, def.Description)
			}
			return w.Flush()
		},
	}

	cmd.Flags().StringVar(&root, "root", root, "workspace root used to build the tool set")

	return cmd
}
