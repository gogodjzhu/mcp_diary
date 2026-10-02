package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/gogodjzhu/mcp-diary/internal/app"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/gogodjzhu/mcp-diary/internal/platform/logging"
	"github.com/spf13/cobra"
)

func newServeCommand(version string, logLevel, logFormat *string) *cobra.Command {
	cfg := config.Default()
	cfg.Version = version
	transport := string(cfg.Transport)

	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Start the MCP server",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg.Transport = config.Transport(transport)
			cfg.LogLevel = *logLevel
			cfg.LogFormat = *logFormat

			logger, err := logging.New(cfg.LogLevel, cfg.LogFormat)
			if err != nil {
				return err
			}

			application, err := app.New(cfg, logger)
			if err != nil {
				return err
			}
			defer func() { _ = application.Close() }()

			ctx, stop := signal.NotifyContext(cmd.Context(), os.Interrupt, syscall.SIGTERM)
			defer stop()

			return application.Run(ctx)
		},
	}

	flags := cmd.Flags()
	flags.StringVar(&transport, "transport", transport, "transport to serve: streamable-http or stdio")
	flags.StringVar(&cfg.Addr, "addr", cfg.Addr, "HTTP listen address (streamable-http only)")
	flags.StringVar(&cfg.EndpointPath, "endpoint", cfg.EndpointPath, "HTTP endpoint path (streamable-http only)")
	flags.StringVar(&cfg.Root, "root", cfg.Root, "workspace root directory exposed to clients")
	flags.BoolVar(&cfg.ReadOnly, "read-only", cfg.ReadOnly, "disable all mutating tools")
	flags.BoolVar(&cfg.AllowRemoteClients, "allow-remote", cfg.AllowRemoteClients, "disable DNS-rebinding protection when accessed via a non-localhost Host header")
	flags.BoolVar(&cfg.StreamingDisabled, "disable-streaming", cfg.StreamingDisabled, "disable server-sent streaming and answer GET with 405")
	flags.Int64Var(&cfg.MaxReadBytes, "max-read-bytes", cfg.MaxReadBytes, "maximum bytes returned by a single read")
	flags.Int64Var(&cfg.MaxRequestBodyBytes, "max-request-bytes", cfg.MaxRequestBodyBytes, "maximum HTTP request body size in bytes")

	flags.BoolVar(&cfg.Auth.Enabled, "auth-enabled", cfg.Auth.Enabled, "require OAuth 2.1 authentication (mcp-diary acts as its own authorization server)")
	flags.StringVar(&cfg.Auth.PublicURL, "auth-public-url", cfg.Auth.PublicURL, "externally reachable base URL, e.g. https://mcp.example.com (OAuth issuer)")
	flags.StringVar(&cfg.Auth.GoogleClientID, "google-client-id", cfg.Auth.GoogleClientID, "Google OAuth client id used server-side")
	flags.StringVar(&cfg.Auth.GoogleClientSecret, "google-client-secret", cfg.Auth.GoogleClientSecret, "Google OAuth client secret (kept on the server)")
	flags.StringVar(&cfg.Auth.EncryptionKey, "auth-encryption-key", cfg.Auth.EncryptionKey, "32-byte key (base64 or hex) encrypting OAuth state at rest")
	flags.DurationVar(&cfg.Auth.AccessTokenTTL, "auth-access-token-ttl", cfg.Auth.AccessTokenTTL, "issued access token lifetime")
	flags.DurationVar(&cfg.Auth.RefreshTokenTTL, "auth-refresh-token-ttl", cfg.Auth.RefreshTokenTTL, "issued refresh token lifetime")
	flags.IntVar(&cfg.Auth.MaxClientsPerIP, "auth-max-clients-per-ip", cfg.Auth.MaxClientsPerIP, "maximum dynamic client registrations per IP")
	flags.StringVar(&cfg.Auth.StoreDir, "auth-store-dir", cfg.Auth.StoreDir, "directory (relative to root unless absolute) holding OAuth state")
	flags.StringVar(&cfg.Auth.UsersDir, "auth-users-dir", cfg.Auth.UsersDir, "directory (relative to root unless absolute) holding per-user workspaces")

	flags.BoolVar(&cfg.Web.Enabled, "web-enabled", cfg.Web.Enabled, "serve the web UI and REST API (requires --auth-enabled)")
	flags.StringVar(&cfg.Web.StaticDir, "web-static-dir", cfg.Web.StaticDir, "serve web assets from this directory instead of the embedded build (development)")

	return cmd
}
