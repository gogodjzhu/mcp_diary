// Package oauthserver wires the OAuth 2.1 authorization-server role for
// mcp-diary. The server acts as its own authorization server to MCP clients and
// to the browser UI, federating identity to Google on the server side. Clients
// obtain a client id automatically through dynamic client registration, so
// nothing but the server URL needs to be distributed.
package oauth

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	oauth "github.com/giantswarm/mcp-oauth"
	oauthhandler "github.com/giantswarm/mcp-oauth/handler"
	"github.com/giantswarm/mcp-oauth/providers/google"
	"github.com/giantswarm/mcp-oauth/security"
	"github.com/giantswarm/mcp-oauth/storage"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
)

// WebClientID is the fixed client id of the first-party browser UI. It is
// pre-registered at startup so the SPA never needs dynamic registration.
const WebClientID = "mcp-diary-web"

// OAuth bundles the authorization server, its HTTP handler and the adapters
// that expose an authenticated identity to the MCP and web access layers.
type OAuth struct {
	cfg     config.AuthConfig
	logger  *slog.Logger
	store   *FileStore
	server  *oauth.Server
	handler *oauthhandler.Handler
}

// New assembles the authorization server from the resolved configuration.
// storePath is the absolute path of the JSON state file.
func New(cfg config.AuthConfig, endpointPath, storePath string, logger *slog.Logger) (*OAuth, error) {
	if logger == nil {
		logger = slog.Default()
	}

	issuer := strings.TrimRight(cfg.PublicURL, "/")
	if issuer == "" {
		return nil, fmt.Errorf("auth public URL is required when auth is enabled")
	}

	encryptor, err := newEncryptor(cfg.EncryptionKey, logger)
	if err != nil {
		return nil, err
	}

	store, err := NewFileStore(storePath, encryptor)
	if err != nil {
		return nil, fmt.Errorf("open oauth store: %w", err)
	}

	provider, err := google.NewProvider(&google.Config{
		ClientID:     cfg.GoogleClientID,
		ClientSecret: cfg.GoogleClientSecret,
		RedirectURL:  issuer + "/oauth/callback",
		Scopes:       []string{"openid", "email", "profile"},
	})
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("configure google provider: %w", err)
	}

	resource := issuer + normalizePath(endpointPath)

	srv, err := oauth.NewServer(
		provider,
		store, // TokenStore
		store, // ClientStore
		store, // FlowStore
		&oauth.ServerConfig{
			Issuer:                        issuer,
			ResourceIdentifier:            resource,
			AllowInsecureHTTP:             isInsecureIssuer(issuer),
			AllowLocalhostRedirectURIs:    true,
			AllowPublicClientRegistration: true,
			MaxClientsPerIP:               cfg.MaxClientsPerIP,
			EnableRevocationEndpoint:      true,
			AccessTokenTTL:                int64(cfg.AccessTokenTTL.Seconds()),
			RefreshTokenTTL:               int64(cfg.RefreshTokenTTL.Seconds()),
			SupportedScopes:               []string{"mcp"},
		},
		logger,
		oauth.WithAuditor(security.NewAuditor(logger, true)),
	)
	if err != nil {
		store.Close()
		return nil, fmt.Errorf("create oauth server: %w", err)
	}

	o := &OAuth{
		cfg:     cfg,
		logger:  logger,
		store:   store,
		server:  srv,
		handler: oauthhandler.New(srv, logger),
	}

	if err := o.ensureWebClient(issuer); err != nil {
		store.Close()
		return nil, err
	}

	return o, nil
}

// RegisterRoutes mounts the OAuth flow endpoints and the discovery documents on
// mux.
func (o *OAuth) RegisterRoutes(mux *http.ServeMux, endpointPath string) {
	o.handler.RegisterOAuthRoutes(mux, oauthhandler.OAuthRoutesOptions{
		MCPPath:         endpointPath,
		IncludeMetadata: true,
	})
}

// Protect validates the bearer token and injects the authenticated identity
// into the request context, in the shape the shared workspace layer expects.
func (o *OAuth) Protect(next http.Handler) http.Handler {
	return o.handler.ValidateToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userInfo, ok := oauthhandler.UserInfoFromContext(r.Context())
		if !ok || userInfo == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		scopes, _ := oauthhandler.ScopesFromContext(r.Context())
		ident := &identity.Identity{
			Subject: userInfo.ID,
			Email:   userInfo.Email,
			Name:    userInfo.Name,
			Scopes:  scopes,
		}
		next.ServeHTTP(w, r.WithContext(identity.WithIdentity(r.Context(), ident)))
	}))
}

// Close releases the store.
func (o *OAuth) Close() error { return o.store.Close() }

// WebRedirectURI is the redirect URI the browser UI must use.
func WebRedirectURI(publicURL string) string {
	return strings.TrimRight(publicURL, "/") + "/auth/callback"
}

func (o *OAuth) ensureWebClient(issuer string) error {
	ctx := context.Background()
	existing, err := o.store.GetClient(ctx, WebClientID)
	if err != nil && !errors.Is(err, storage.ErrClientNotFound) {
		return fmt.Errorf("look up web client: %w", err)
	}
	if existing != nil {
		return nil
	}
	now := time.Now()
	client := &storage.Client{
		ClientID:                WebClientID,
		ClientType:              storage.ClientTypePublic,
		TokenEndpointAuthMethod: "none",
		RedirectURIs:            []string{WebRedirectURI(issuer)},
		GrantTypes:              []string{"authorization_code", "refresh_token"},
		ResponseTypes:           []string{"code"},
		ClientName:              "mcp-diary web UI",
		Scopes:                  []string{"mcp"},
		CreatedAt:               now,
		UpdatedAt:               now,
	}
	if err := o.store.SaveClient(ctx, client); err != nil {
		return fmt.Errorf("register web client: %w", err)
	}
	o.logger.Info("registered first-party web OAuth client", "client_id", WebClientID, "redirect_uri", client.RedirectURIs[0])
	return nil
}

func newEncryptor(key string, logger *slog.Logger) (*security.Encryptor, error) {
	key = strings.TrimSpace(key)
	if key == "" {
		logger.Warn("no auth encryption key configured; OAuth state is stored unencrypted (development only)")
		return nil, nil
	}
	raw, err := decodeKey(key)
	if err != nil {
		return nil, err
	}
	return security.NewEncryptor(raw)
}

func decodeKey(key string) ([]byte, error) {
	if raw, err := base64.StdEncoding.DecodeString(key); err == nil && len(raw) == 32 {
		return raw, nil
	}
	if raw, err := hex.DecodeString(key); err == nil && len(raw) == 32 {
		return raw, nil
	}
	return nil, fmt.Errorf("auth encryption key must be 32 bytes encoded as base64 or hex")
}

func isInsecureIssuer(issuer string) bool {
	return strings.HasPrefix(issuer, "http://")
}

func normalizePath(p string) string {
	if p == "" {
		return "/"
	}
	if !strings.HasPrefix(p, "/") {
		return "/" + p
	}
	return p
}
