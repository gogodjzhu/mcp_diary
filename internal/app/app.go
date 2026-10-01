// Package app is the composition root. It resolves the shared dependencies
// (workspaces and authentication), wires the MCP and web access layers together
// and runs the resulting HTTP server.
package app

import (
	"fmt"
	"log/slog"
	"net/http"
	"path/filepath"

	"github.com/gogodjzhu/mcp-diary/internal/access/mcp"
	"github.com/gogodjzhu/mcp-diary/internal/access/web"
	"github.com/gogodjzhu/mcp-diary/internal/config"
	"github.com/gogodjzhu/mcp-diary/internal/diary"
	"github.com/gogodjzhu/mcp-diary/internal/oauthserver"
	"github.com/gogodjzhu/mcp-diary/internal/tools"
	"github.com/gogodjzhu/mcp-diary/internal/tools/diarytools"
	"github.com/gogodjzhu/mcp-diary/internal/tools/fstools"
	"github.com/gogodjzhu/mcp-diary/internal/workspace"
)

// App is a fully assembled server exposing both the MCP and the web access
// layers over a single HTTP server.
type App struct {
	cfg        config.Config
	logger     *slog.Logger
	workspaces *workspace.Manager
	registry   *tools.Registry

	mcp   *mcp.Server
	oauth *oauthserver.OAuth
	web   http.Handler
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

	diarySvc := diary.New(nil, nil)
	registry := tools.NewRegistry()
	registry.Add(diarytools.All(workspaces, diarySvc)...)
	registry.Add(fstools.All(workspaces)...)

	app := &App{
		cfg:        cfg,
		logger:     logger,
		workspaces: workspaces,
		registry:   registry,
		mcp:        mcp.New(cfg, workspaces, registry, logger),
	}

	if cfg.Auth.Enabled {
		storePath := filepath.Join(resolveDir(cfg.Root, cfg.Auth.StoreDir), "oauth.json")
		oauthSrv, err := oauthserver.New(cfg.Auth, cfg.EndpointPath, storePath, logger)
		if err != nil {
			return nil, fmt.Errorf("configure authorization server: %w", err)
		}
		app.oauth = oauthSrv
	}

	switch {
	case cfg.Web.Enabled && cfg.Auth.Enabled:
		app.web = web.New(web.Config{
			Workspaces: workspaces,
			Logger:     logger,
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
		"tools", registry.Names(),
	)

	return app, nil
}

// Close releases resources owned by the application.
func (a *App) Close() error {
	if a.oauth != nil {
		return a.oauth.Close()
	}
	return nil
}

// MCPServer returns the underlying MCP server.
func (a *App) MCPServer() *mcp.Server { return a.mcp }

// ToolNames returns the registered tool names.
func (a *App) ToolNames() []string { return a.registry.Names() }

// Workspaces returns the workspace manager.
func (a *App) Workspaces() *workspace.Manager { return a.workspaces }

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
