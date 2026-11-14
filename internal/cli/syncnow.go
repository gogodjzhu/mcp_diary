package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/core/diarysync"
	"github.com/gogodjzhu/mcp-diary/internal/core/filesystem"
	"github.com/spf13/cobra"
)

func newSyncNowCommand() *cobra.Command {
	var root string
	var user string
	cmd := &cobra.Command{
		Use:   "sync-now",
		Short: "Sync one user workspace to its configured media, including pictures",
		RunE: func(cmd *cobra.Command, args []string) error {
			workspace := filepath.Join(root, "users", user)
			fs, err := filesystem.New(filesystem.Options{Root: workspace})
			if err != nil {
				return err
			}
			defer fs.Close()
			codec, err := diarysync.NewCodec(os.Getenv("SYNC_ENCRYPTION_KEY"))
			if err != nil {
				return err
			}
			registry := diarysync.NewRegistry()
			registry.Register(diarysync.KindGitHub, diarysync.GitHubFactory(nil, nil))
			syncSvc := diarysync.New(codec, registry, nil)
			diarySvc := diary.New(nil, nil)
			engine := diarysync.NewEngine(syncSvc, diarySvc, time.Second, nil, nil)
			defer engine.Stop()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
			defer cancel()
			if err := engine.TriggerFull(ctx, fs); err != nil {
				return err
			}
			media, err := syncSvc.List(ctx, fs)
			if err != nil {
				return err
			}
			for _, medium := range media {
				state, err := syncSvc.State(ctx, fs, medium.ID)
				if err != nil {
					fmt.Printf("%s: state error: %v\n", medium.Name, err)
					continue
				}
				fmt.Printf("%s: %s %s\n", medium.Name, state.Status, state.LastError)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&root, "root", "/data", "workspace root that contains the users directory")
	cmd.Flags().StringVar(&user, "user", "", "user workspace directory name")
	_ = cmd.MarkFlagRequired("user")
	return cmd
}
