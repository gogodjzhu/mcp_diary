// Package app is the composition root. It resolves the shared dependencies
// (workspaces and auth), wires the MCP and web access layers together and runs
// the resulting HTTP server.
package app

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/gogodjzhu/mcp-diary/internal/access/mcp"
	"github.com/gogodjzhu/mcp-diary/internal/access/web"
	"github.com/gogodjzhu/mcp-diary/internal/auth"
	"github.com/gogodjzhu/mcp-diary/internal/config"
	"github.com/gogodjzhu/mcp-diary/internal/tools"
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

	mcp *mcp.Server

	verifier  auth.Verifier
	authGuard *auth.Middleware
	web       http.Handler
}

// New builds the application: it opens the workspace root, optionally wires
// OAuth 2.0 / OIDC authentication and the web access layer, and assembles the
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

	registry := tools.NewRegistry(fstools.All(workspaces)...)

	app := &App{
		cfg:        cfg,
		logger:     logger,
		workspaces: workspaces,
		registry:   registry,
		mcp:        mcp.New(cfg, workspaces, registry, logger),
	}

	if cfg.Auth.Enabled {
		app.verifier = auth.NewOpaqueVerifier(auth.VerifierOptions{
			TokenInfoURL:         cfg.Auth.TokenInfoURL,
			UserInfoURL:          cfg.Auth.UserInfoURL,
			Audience:             cfg.Auth.ExpectedAudience(),
			Scopes:               cfg.Auth.Scopes,
			RequireVerifiedEmail: cfg.Auth.RequireVerifiedEmail,
			CacheTTL:             cfg.Auth.CacheTTL,
			HTTPClient:           &http.Client{Timeout: cfg.Auth.HTTPTimeout},
		})
		app.authGuard = auth.NewMiddleware(auth.MiddlewareConfig{
			Verifier:     app.verifier,
			Scopes:       cfg.Auth.Scopes,
			PublicURL:    cfg.Auth.PublicURL,
			EndpointPath: cfg.EndpointPath,
			Logger:       logger,
		})
	}

	switch {
	case cfg.Web.Enabled && cfg.Auth.Enabled:
		app.web = web.New(web.Config{
			Verifier:   app.verifier,
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

// MCPServer returns the underlying MCP server.
func (a *App) MCPServer() *mcp.Server { return a.mcp }

// ToolNames returns the registered tool names.
func (a *App) ToolNames() []string { return a.registry.Names() }

// Workspaces returns the workspace manager.
func (a *App) Workspaces() *workspace.Manager { return a.workspaces }

// AuthEnabled reports whether bearer-token authentication is active.
func (a *App) AuthEnabled() bool { return a.cfg.Auth.Enabled }

// AuthMiddleware returns the MCP authentication middleware, or nil when auth is
// disabled.
func (a *App) AuthMiddleware() *auth.Middleware { return a.authGuard }
