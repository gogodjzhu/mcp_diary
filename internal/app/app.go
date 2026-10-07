// Package app is the composition root. It resolves the shared dependencies
// (workspaces and authentication), wires the MCP and web access layers together
// and runs the resulting HTTP server.
package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/gogodjzhu/mcp-diary/internal/auth/oauth"
	"github.com/gogodjzhu/mcp-diary/internal/core/diary"
	"github.com/gogodjzhu/mcp-diary/internal/core/diarymeta"
	"github.com/gogodjzhu/mcp-diary/internal/core/diarysync"
	"github.com/gogodjzhu/mcp-diary/internal/core/workspace"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/gogodjzhu/mcp-diary/internal/transport/httpapi"
	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp"
	"github.com/gogodjzhu/mcp-diary/internal/transport/mcp/tools"
	diarytools "github.com/gogodjzhu/mcp-diary/internal/transport/mcp/tools/diary"
)

// App is a fully assembled server exposing both the MCP and the web access
// layers over a single HTTP server.
type App struct {
	cfg        config.Config
	logger     *slog.Logger
	workspaces *workspace.Manager
	registry   *tools.Registry
	diary      *diary.Service
	sync       *diarysync.Service
	engine     *diarysync.Engine

	mcp   *mcp.Server
	oauth *oauth.OAuth
	web   http.Handler
}

// BuildRegistry assembles the MCP tool registry bound to the given workspaces.
// It is shared by the server assembly and the CLI `tools` listing command so
// both always expose the same tool set. Both dependencies are required: a nil
// value is a wiring bug, not something to paper over with a throwaway service.
func BuildRegistry(workspaces *workspace.Manager, diarySvc *diary.Service) (*tools.Registry, error) {
	if workspaces == nil {
		return nil, fmt.Errorf("workspace manager is required")
	}
	if diarySvc == nil {
		return nil, fmt.Errorf("diary service is required")
	}
	registry := tools.NewRegistry()
	registry.Add(diarytools.All(workspaces, diarySvc)...)
	return registry, nil
}

// New builds the application: it opens the workspace root, optionally wires the
// OAuth 2.1 authorization server and the web access layer, and assembles the
// MCP server with every tool registered.
func New(cfg config.Config, logger *slog.Logger) (*App, error) {
	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}
	if logger == nil {
		logger = slog.Default()
	}

	workspaces, err := workspace.New(workspace.Config{
		Root:         cfg.Root,
		UsersDir:     cfg.Auth.UsersDir,
		PerUser:      cfg.Auth.Enabled,
		ReadOnly:     cfg.ReadOnly,
		MaxReadBytes: cfg.MaxReadBytes,
	})
	if err != nil {
		return nil, fmt.Errorf("open workspace: %w", err)
	}

	codec, err := diarysync.NewCodec(cfg.Sync.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("configure sync encryption: %w", err)
	}
	if cfg.Sync.EncryptionKey == "" {
		logger.Warn("no sync encryption key configured; storage-medium credentials cannot be saved")
	}
	registry := diarysync.NewRegistry()
	registry.Register(diarysync.KindGitHub, diarysync.GitHubFactory(nil, nil))
	syncSvc := diarysync.New(codec, registry, nil)
	diarySvc := diary.New(nil, nil)
	metaSvc := diarymeta.New()
	diarySvc.SetMetaEnricher(diarymeta.NewEnricher(metaSvc, diarymeta.NewWeatherClient(nil), logger))
	engine := diarysync.NewEngine(syncSvc, diarySvc, diarysync.DefaultDebounce, nil, logger)
	syncSvc.SetRunner(engine)

	toolRegistry, err := BuildRegistry(workspaces, diarySvc)
	if err != nil {
		return nil, fmt.Errorf("build tool registry: %w", err)
	}

	app := &App{
		cfg:        cfg,
		logger:     logger,
		workspaces: workspaces,
		registry:   toolRegistry,
		diary:      diarySvc,
		sync:       syncSvc,
		engine:     engine,
		mcp:        mcp.New(cfg, workspaces, toolRegistry, logger),
	}

	if cfg.Auth.Enabled {
		storePath := filepath.Join(resolveDir(cfg.Root, cfg.Auth.StoreDir), "oauth.json")
		oauthSrv, err := oauth.New(cfg.Auth, cfg.EndpointPath, storePath, logger)
		if err != nil {
			return nil, fmt.Errorf("configure authorization server: %w", err)
		}
		app.oauth = oauthSrv
	}

	switch {
	case cfg.Web.Enabled && cfg.Auth.Enabled:
		app.web = httpapi.New(httpapi.Config{
			Logger:     logger,
			Workspaces: workspaces,
			Sync:       syncSvc,
			Meta:       metaSvc,
		})
	case cfg.Web.Enabled:
		logger.Warn("web UI is enabled but authentication is disabled; web routes are not registered")
	}

	logger.Info("workspace opened",
		"root", workspaces.Root(),
		"read_only", workspaces.ReadOnly(),
		"auth_enabled", cfg.Auth.Enabled,
		"per_user_workspaces", workspaces.PerUser(),
		"users_dir", workspaces.UsersDir(),
		"web_enabled", app.web != nil,
		"tools", toolRegistry.Names(),
	)

	return app, nil
}

// Close releases resources owned by the application.
func (a *App) Close() error {
	if a.engine != nil {
		a.engine.Stop()
	}
	var first error
	if a.oauth != nil {
		if err := a.oauth.Close(); err != nil {
			first = err
		}
	}
	if a.workspaces != nil {
		if err := a.workspaces.Close(); err != nil && first == nil {
			first = err
		}
	}
	return first
}

// MCPServer returns the underlying MCP server.
func (a *App) MCPServer() *mcp.Server { return a.mcp }

// ToolNames returns the registered tool names.
func (a *App) ToolNames() []string { return a.registry.Names() }

// Workspaces returns the workspace manager.
func (a *App) Workspaces() *workspace.Manager { return a.workspaces }

// Sync returns the storage-medium sync service.
func (a *App) Sync() *diarysync.Service { return a.sync }

// Engine returns the diary-to-medium sync scheduler.
func (a *App) Engine() *diarysync.Engine { return a.engine }

// Diary returns the diary domain service.
func (a *App) Diary() *diary.Service { return a.diary }

// AuthEnabled reports whether OAuth authentication is active.
func (a *App) AuthEnabled() bool { return a.cfg.Auth.Enabled }

func resolveDir(root, dir string) string {
	if dir == "" {
		return root
	}
	if filepath.IsAbs(dir) {
		return dir
	}
	return filepath.Join(root, dir)
}
