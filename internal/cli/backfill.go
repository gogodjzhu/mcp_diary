package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
	"github.com/spf13/cobra"
)

func newBackfillPreviewsCommand() *cobra.Command {
	var root string
	cmd := &cobra.Command{
		Use:   "backfill-previews",
		Short: "Fill missing image thumbnails in existing diary workspaces",
		RunE: func(cmd *cobra.Command, args []string) error {
			users := filepath.Join(root, "users")
			entries, err := os.ReadDir(users)
			if err != nil {
				return fmt.Errorf("read users dir: %w", err)
			}
			svc := diary.New(nil, nil)
			total := 0
			for _, entry := range entries {
				if !entry.IsDir() {
					continue
				}
				fs, err := filesystem.New(filesystem.Options{Root: filepath.Join(users, entry.Name())})
				if err != nil {
					return err
				}
				n, err := svc.BackfillPreviews(context.Background(), fs)
				_ = fs.Close()
				if err != nil {
					return fmt.Errorf("%s: %w", entry.Name(), err)
				}
				if n > 0 {
					fmt.Printf("%s: %d\n", entry.Name(), n)
					total += n
				}
			}
			fmt.Printf("filled %d\n", total)
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "/data", "workspace root that contains the users directory")
	return cmd
}
