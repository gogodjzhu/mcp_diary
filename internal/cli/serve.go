package cli

import (
	"os"
	"os/signal"
	"syscall"

	"github.com/gogodjzhu/mcp-diary/internal/app"
	"github.com/gogodjzhu/mcp-diary/internal/config"
	"github.com/gogodjzhu/mcp-diary/internal/logging"
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

	flags.BoolVar(&cfg.Auth.Enabled, "auth-enabled", cfg.Auth.Enabled, "require OAuth 2.0 / OIDC bearer tokens (Google by default)")
	flags.StringVar(&cfg.Auth.Issuer, "auth-issuer", cfg.Auth.Issuer, "authorization server issuer advertised in resource metadata")
	flags.StringVar(&cfg.Auth.ClientID, "auth-client-id", cfg.Auth.ClientID, "OAuth client id tokens must be minted for (audience)")
	flags.StringVar(&cfg.Auth.ClientSecret, "auth-client-secret", cfg.Auth.ClientSecret, "OAuth client secret (only for providers that require it)")
	flags.StringVar(&cfg.Auth.Audience, "auth-audience", cfg.Auth.Audience, "override the expected token audience (defaults to client id)")
	flags.StringSliceVar(&cfg.Auth.Scopes, "auth-scopes", cfg.Auth.Scopes, "required scopes (comma separated)")
	flags.StringVar(&cfg.Auth.PublicURL, "auth-public-url", cfg.Auth.PublicURL, "externally reachable base URL, e.g. https://mcp.example.com")
	flags.StringVar(&cfg.Auth.TokenInfoURL, "auth-tokeninfo-url", cfg.Auth.TokenInfoURL, "token validation endpoint")
	flags.StringVar(&cfg.Auth.UserInfoURL, "auth-userinfo-url", cfg.Auth.UserInfoURL, "optional userinfo endpoint for profile enrichment")
	flags.BoolVar(&cfg.Auth.RequireVerifiedEmail, "auth-require-verified-email", cfg.Auth.RequireVerifiedEmail, "reject tokens whose email is not verified")
	flags.DurationVar(&cfg.Auth.CacheTTL, "auth-cache-ttl", cfg.Auth.CacheTTL, "how long successful token verifications are cached")
	flags.DurationVar(&cfg.Auth.HTTPTimeout, "auth-http-timeout", cfg.Auth.HTTPTimeout, "timeout for authorization-server calls")
	flags.StringVar(&cfg.Auth.UsersDir, "auth-users-dir", cfg.Auth.UsersDir, "directory (relative to root unless absolute) holding per-user workspaces")

	flags.BoolVar(&cfg.Web.Enabled, "web-enabled", cfg.Web.Enabled, "serve the web UI and REST API (requires --auth-enabled)")
	flags.StringVar(&cfg.Web.StaticDir, "web-static-dir", cfg.Web.StaticDir, "serve web assets from this directory instead of the embedded build (development)")

	return cmd
}
