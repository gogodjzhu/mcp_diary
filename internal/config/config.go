// Package config defines the runtime configuration shared by the CLI, the MCP
// server assembly and the transport layer.
package config

import (
	"fmt"
	"slices"
	"time"
)

// Transport identifies the MCP transport the server exposes.
type Transport string

const (
	// TransportStreamableHTTP serves the MCP protocol over the Streamable HTTP
	// transport (the recommended remote transport).
	TransportStreamableHTTP Transport = "streamable-http"
	// TransportStdio serves the MCP protocol over stdin/stdout.
	TransportStdio Transport = "stdio"
)

// SupportedTransports lists every transport understood by the server.
var SupportedTransports = []Transport{TransportStreamableHTTP, TransportStdio}

// Config is the fully resolved runtime configuration.
type Config struct {
	// Server identity advertised during the MCP initialize handshake.
	Name    string
	Version string

	// Transport selects how the MCP protocol is exposed.
	Transport Transport

	// HTTP-only options.
	Addr                string
	EndpointPath        string
	AllowRemoteClients  bool
	StreamingDisabled   bool
	HeartbeatInterval   time.Duration
	ReadHeaderTimeout   time.Duration
	ReadTimeout         time.Duration
	WriteTimeout        time.Duration
	IdleTimeout         time.Duration
	ShutdownTimeout     time.Duration
	MaxRequestBodyBytes int64

	// Filesystem options.
	Root         string
	ReadOnly     bool
	MaxReadBytes int64

	// Auth configures the OAuth 2.0 / OIDC resource-server behaviour.
	Auth AuthConfig

	// Web configures the browser UI and REST API access layer.
	Web WebConfig

	// Observability options.
	LogLevel  string
	LogFormat string
}

// WebConfig configures the browser UI and REST API access layer. The web routes
// are only registered when authentication is also enabled, so the REST API is
// never exposed anonymously.
type WebConfig struct {
	// Enabled turns on the web UI and REST API routes.
	Enabled bool
	// StaticDir, when set, serves the UI from this directory on disk instead of
	// the embedded build. Intended for development.
	StaticDir string
}

// AuthConfig configures OAuth 2.0 / OIDC resource-server authentication. When
// Enabled is true every MCP request must carry a bearer token issued by the
// configured provider (Google by default) and each authenticated user gets an
// isolated workspace underneath UsersDir.
type AuthConfig struct {
	// Enabled turns on bearer-token authentication.
	Enabled bool
	// Issuer is the canonical authorization-server identifier advertised in the
	// protected resource metadata (Google: https://accounts.google.com).
	Issuer string
	// ClientID is the OAuth client id the tokens must be minted for. It is used
	// as the expected audience.
	ClientID string
	// ClientSecret is only required by providers that need confidential client
	// authentication; Google token validation does not.
	ClientSecret string
	// Audience overrides the expected token audience. Defaults to ClientID.
	Audience string
	// Scopes are the scopes a client must request and a token must contain.
	Scopes []string
	// PublicURL is the externally reachable base URL of this server (for
	// example https://mcp.example.com). When empty it is derived from the
	// request, which is only correct when the server is directly reachable.
	PublicURL string
	// TokenInfoURL is the provider endpoint used to validate opaque access
	// tokens (Google: https://oauth2.googleapis.com/tokeninfo).
	TokenInfoURL string
	// UserInfoURL is an optional fallback endpoint, used to enrich the identity
	// with profile data such as the display name.
	UserInfoURL string
	// RequireVerifiedEmail rejects identities whose email is not verified.
	RequireVerifiedEmail bool
	// CacheTTL is how long a successful verification is cached.
	CacheTTL time.Duration
	// HTTPTimeout bounds provider calls.
	HTTPTimeout time.Duration
	// UsersDir is the directory, relative to Root unless absolute, where
	// per-user workspaces are created.
	UsersDir string
}

// Default returns a Config populated with the built-in defaults.
func Default() Config {
	return Config{
		Name:                "mcp-diary",
		Version:             "dev",
		Transport:           TransportStreamableHTTP,
		Addr:                ":8080",
		EndpointPath:        "/mcp",
		HeartbeatInterval:   30 * time.Second,
		ReadHeaderTimeout:   10 * time.Second,
		ReadTimeout:         60 * time.Second,
		WriteTimeout:        0,
		IdleTimeout:         120 * time.Second,
		ShutdownTimeout:     15 * time.Second,
		MaxRequestBodyBytes: 32 << 20, // 32 MiB
		Root:                ".",
		MaxReadBytes:        1 << 20, // 1 MiB
		Auth: AuthConfig{
			Issuer:               "https://accounts.google.com",
			Scopes:               []string{"openid", "email", "profile"},
			TokenInfoURL:         "https://oauth2.googleapis.com/tokeninfo",
			UserInfoURL:          "https://openidconnect.googleapis.com/v1/userinfo",
			RequireVerifiedEmail: true,
			CacheTTL:             5 * time.Minute,
			HTTPTimeout:          10 * time.Second,
			UsersDir:             "users",
		},
		Web: WebConfig{
			Enabled: true,
		},
		LogLevel:  "info",
		LogFormat: "text",
	}
}

// Validate reports whether the configuration is internally consistent.
func (c Config) Validate() error {
	if c.Name == "" {
		return fmt.Errorf("server name must not be empty")
	}
	if c.Version == "" {
		return fmt.Errorf("server version must not be empty")
	}
	if !slices.Contains(SupportedTransports, c.Transport) {
		return fmt.Errorf("unsupported transport %q (want one of %v)", c.Transport, SupportedTransports)
	}
	if c.Transport == TransportStreamableHTTP {
		if c.Addr == "" {
			return fmt.Errorf("listen address must not be empty")
		}
		if c.EndpointPath == "" || c.EndpointPath[0] != '/' {
			return fmt.Errorf("endpoint path %q must start with '/'", c.EndpointPath)
		}
		if c.MaxRequestBodyBytes <= 0 {
			return fmt.Errorf("max request body bytes must be positive")
		}
	}
	if c.Root == "" {
		return fmt.Errorf("filesystem root must not be empty")
	}
	if c.MaxReadBytes <= 0 {
		return fmt.Errorf("max read bytes must be positive")
	}
	if err := c.Auth.validate(); err != nil {
		return err
	}
	if c.Auth.Enabled && c.Transport != TransportStreamableHTTP {
		return fmt.Errorf("authentication requires the %q transport", TransportStreamableHTTP)
	}
	return nil
}

func (a AuthConfig) validate() error {
	if !a.Enabled {
		return nil
	}
	if a.Issuer == "" {
		return fmt.Errorf("auth issuer must not be empty")
	}
	if a.ClientID == "" {
		return fmt.Errorf("auth client id must not be empty when auth is enabled")
	}
	if a.TokenInfoURL == "" && a.UserInfoURL == "" {
		return fmt.Errorf("auth requires a token info or user info endpoint")
	}
	if len(a.Scopes) == 0 {
		return fmt.Errorf("auth requires at least one scope")
	}
	if a.UsersDir == "" {
		return fmt.Errorf("auth users directory must not be empty")
	}
	if a.CacheTTL < 0 {
		return fmt.Errorf("auth cache TTL must not be negative")
	}
	if a.HTTPTimeout <= 0 {
		return fmt.Errorf("auth HTTP timeout must be positive")
	}
	return nil
}

// ExpectedAudience returns the audience tokens must be minted for.
func (a AuthConfig) ExpectedAudience() string {
	if a.Audience != "" {
		return a.Audience
	}
	return a.ClientID
}
