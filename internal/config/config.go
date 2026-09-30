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

	// Auth configures the OAuth 2.1 authorization server.
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

// AuthConfig configures authentication. When Enabled is true mcp-diary acts as
// its own OAuth 2.1 authorization server: MCP clients and the browser UI obtain
// tokens from this server (registering themselves dynamically), and the server
// federates user identity to Google behind the scenes. Each authenticated user
// gets an isolated workspace underneath UsersDir.
type AuthConfig struct {
	// Enabled turns on OAuth 2.1 authentication.
	Enabled bool
	// PublicURL is the externally reachable base URL of this server (for
	// example https://mcp.example.com). It is the OAuth issuer identifier.
	PublicURL string
	// GoogleClientID is the Google OAuth client id used on the server side.
	GoogleClientID string
	// GoogleClientSecret is the Google OAuth client secret. It never leaves the
	// server.
	GoogleClientSecret string
	// EncryptionKey encrypts OAuth state at rest. 32 bytes, base64 or hex
	// encoded. Empty stores state unencrypted (development only).
	EncryptionKey string
	// AccessTokenTTL is how long issued access tokens are valid.
	AccessTokenTTL time.Duration
	// RefreshTokenTTL is how long issued refresh tokens are valid.
	RefreshTokenTTL time.Duration
	// MaxClientsPerIP caps dynamic client registrations per IP address.
	MaxClientsPerIP int
	// StoreDir is the directory, relative to Root unless absolute, holding the
	// OAuth state file.
	StoreDir string
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
			AccessTokenTTL:  time.Hour,
			RefreshTokenTTL: 90 * 24 * time.Hour,
			MaxClientsPerIP: 10,
			StoreDir:        "auth",
			UsersDir:        "users",
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
	if a.PublicURL == "" {
		return fmt.Errorf("auth public URL must not be empty when auth is enabled")
	}
	if a.GoogleClientID == "" {
		return fmt.Errorf("google client id must not be empty when auth is enabled")
	}
	if a.GoogleClientSecret == "" {
		return fmt.Errorf("google client secret must not be empty when auth is enabled")
	}
	if a.UsersDir == "" {
		return fmt.Errorf("auth users directory must not be empty")
	}
	if a.StoreDir == "" {
		return fmt.Errorf("auth store directory must not be empty")
	}
	if a.AccessTokenTTL <= 0 {
		return fmt.Errorf("auth access token TTL must be positive")
	}
	if a.RefreshTokenTTL <= 0 {
		return fmt.Errorf("auth refresh token TTL must be positive")
	}
	if a.MaxClientsPerIP < 0 {
		return fmt.Errorf("auth max clients per IP must not be negative")
	}
	return nil
}
