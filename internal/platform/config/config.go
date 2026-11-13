// Package config defines the runtime configuration shared by the CLI, the MCP
// server assembly and the transport layer.
package config

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"
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

	// Workspace (storage) options.
	Root         string
	ReadOnly     bool
	MaxReadBytes int64

	// Auth configures the OAuth 2.1 authorization server.
	Auth AuthConfig

	// Web configures the browser UI and REST API access layer.
	Web WebConfig

	// Sync encrypts storage-medium credentials at rest.
	Sync SyncConfig

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

// SMTPConfig describes the SMTP submission endpoint used to deliver
// verification codes. It mirrors the mail sender's configuration without
// importing it: platform must not depend on auth.
type SMTPConfig struct {
	Host     string
	Port     int
	TLSMode  string
	Username string
	Password string
	From     string
	FromName string
	Timeout  time.Duration
}

// AuthConfig configures authentication. When Enabled is true mcp-diary acts as
// its own OAuth 2.1 authorization server: MCP clients and the browser UI obtain
// tokens from this server (registering themselves dynamically), and the server
// federates user identity to the configured providers. Each authenticated user
// gets an isolated workspace underneath UsersDir, named after their user id.
type AuthConfig struct {
	// Enabled turns on OAuth 2.1 authentication.
	Enabled bool
	// PublicURL is the externally reachable base URL of this server (for
	// example https://mcp.example.com). It is the OAuth issuer identifier.
	PublicURL string
	// GoogleClientID is the Google OAuth client id used on the server side.
	// Empty disables Google login.
	GoogleClientID string
	// GoogleClientSecret is the Google OAuth client secret. It never leaves the
	// server.
	GoogleClientSecret string
	// GiteeClientID is the Gitee OAuth client id. Empty hides Gitee login.
	GiteeClientID string
	// GiteeClientSecret is the Gitee OAuth client secret.
	GiteeClientSecret string
	// GiteeBaseURL overrides the Gitee endpoints. Empty uses gitee.com. Tests
	// point it at a stand-in server.
	GiteeBaseURL string
	// EmailEnabled offers email-and-password registration and login.
	EmailEnabled bool
	// RequireEmailBinding blocks business endpoints until the user confirms an
	// email address. Every login path, including Google, goes through it.
	RequireEmailBinding bool
	// SMTP delivers verification codes. Required when EmailEnabled is set.
	SMTP SMTPConfig
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
	// UsersFile is the user store file, relative to StoreDir unless absolute.
	UsersFile string
	// MaxUserBytes caps a single user's workspace. Zero disables the cap.
	MaxUserBytes int64
}

// SyncConfig holds the master key used to encrypt storage-medium credentials
// (GitHub tokens, etc.) before they are written under each user sandbox.
type SyncConfig struct {
	// EncryptionKey is a 32-byte key, base64 or hex encoded, injected at
	// deploy time. Empty refuses to persist credentials.
	EncryptionKey string
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
			EmailEnabled:        true,
			RequireEmailBinding: true,
			AccessTokenTTL:      time.Hour,
			RefreshTokenTTL:     90 * 24 * time.Hour,
			MaxClientsPerIP:     10,
			StoreDir:            "auth",
			UsersDir:            "users",
			UsersFile:           "users.json",
			MaxUserBytes:        50 << 20, // 50 MiB
			SMTP: SMTPConfig{
				Port:     465,
				TLSMode:  "ssl",
				FromName: "mcp-diary",
				Timeout:  10 * time.Second,
			},
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
	if err := c.Sync.validate(); err != nil {
		return err
	}
	if c.Auth.Enabled && c.Transport != TransportStreamableHTTP {
		return fmt.Errorf("authentication requires the %q transport", TransportStreamableHTTP)
	}
	return nil
}

func (s SyncConfig) validate() error {
	key := strings.TrimSpace(s.EncryptionKey)
	if key == "" {
		return nil
	}
	if raw, err := base64.StdEncoding.DecodeString(key); err == nil && len(raw) == 32 {
		return nil
	}
	if raw, err := hex.DecodeString(key); err == nil && len(raw) == 32 {
		return nil
	}
	return fmt.Errorf("sync encryption key must be 32 bytes encoded as base64 or hex")
}

func (a AuthConfig) validate() error {
	if !a.Enabled {
		return nil
	}
	if a.PublicURL == "" {
		return fmt.Errorf("auth public URL must not be empty when auth is enabled")
	}
	if a.GoogleClientID == "" && a.GiteeClientID == "" && !a.EmailEnabled {
		return fmt.Errorf("at least one auth provider must be configured")
	}
	if (a.GoogleClientID == "") != (a.GoogleClientSecret == "") {
		return fmt.Errorf("google client id and secret must both be set or both be empty")
	}
	if (a.GiteeClientID == "") != (a.GiteeClientSecret == "") {
		return fmt.Errorf("gitee client id and secret must both be set or both be empty")
	}
	if a.EmailEnabled {
		if err := a.validateSMTP(); err != nil {
			return err
		}
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
	if a.MaxUserBytes < 0 {
		return fmt.Errorf("max user bytes must not be negative")
	}
	return nil
}

// validateSMTP checks the mail configuration. Verification codes are how every
// account proves its address, so a half-configured sender must not boot.
func (a AuthConfig) validateSMTP() error {
	s := a.SMTP
	if s.Host == "" || s.Username == "" || s.Password == "" {
		return fmt.Errorf("smtp host, username and password are required when email auth is enabled")
	}
	if s.Port <= 0 {
		return fmt.Errorf("smtp port must be positive")
	}
	switch s.TLSMode {
	case "ssl", "starttls", "plain":
	default:
		return fmt.Errorf("smtp tls mode must be %q, %q or %q", "ssl", "starttls", "plain")
	}
	return nil
}
