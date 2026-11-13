// Package oauth wires the OAuth 2.1 authorization-server role for mcp-diary.
// The server acts as its own authorization server to MCP clients and to the
// browser UI. Clients obtain a client id automatically through dynamic client
// registration, so nothing but the server URL needs to be distributed.
//
// Identity is federated to one or more upstream providers (Google, Gitee) plus
// a local email-and-password provider. The library drives a single Provider
// interface, so a dispatcher selects the upstream per request and a small login
// page collects the user's choice.
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
	"github.com/giantswarm/mcp-oauth/providers"
	libgoogle "github.com/giantswarm/mcp-oauth/providers/google"
	"github.com/giantswarm/mcp-oauth/security"
	"github.com/giantswarm/mcp-oauth/storage"
	googleprov "github.com/gogodjzhu/mcp-diary/internal/auth/provider/google"

	"github.com/gogodjzhu/mcp-diary/internal/auth/identity"
	giteeprov "github.com/gogodjzhu/mcp-diary/internal/auth/provider/gitee"
	"github.com/gogodjzhu/mcp-diary/internal/auth/provider/multi"
	"github.com/gogodjzhu/mcp-diary/internal/auth/user"
	"github.com/gogodjzhu/mcp-diary/internal/auth/verify"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
)

// WebClientID is the fixed client id of the first-party browser UI. It is
// pre-registered at startup so the SPA never needs dynamic registration.
const WebClientID = "mcp-diary-web"

// emailBindingCode is the error code returned when an authenticated user has
// not confirmed an email address yet.
const emailBindingCode = "email_binding_required"

// OAuth bundles the authorization server, its HTTP handler and the adapters
// that expose an authenticated identity to the MCP and web access layers.
type OAuth struct {
	cfg        config.AuthConfig
	logger     *slog.Logger
	store      *FileStore
	server     *oauth.Server
	handler    *oauthhandler.Handler
	users      *user.Store
	verifier   *verify.Service
	dispatcher *multi.Provider
	linking    map[string]providers.Provider
	login      *Login

	cancelSweep context.CancelFunc
}

// Dependencies are the collaborators the authorization server does not build
// itself. Tests pass fakes; production wiring lives in the composition root.
type Dependencies struct {
	Users    *user.Store
	Verifier *verify.Service
}

// New assembles the authorization server from the resolved configuration.
// storePath is the absolute path of the JSON state file.
func New(cfg config.AuthConfig, endpointPath, storePath string, logger *slog.Logger, deps Dependencies) (*OAuth, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if deps.Users == nil {
		return nil, errors.New("user store is required")
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

	dispatcher, email, linking, err := buildProviders(cfg, issuer, storePath)
	if err != nil {
		store.Close()
		return nil, err
	}

	resource := issuer + normalizePath(endpointPath)
	srv, err := oauth.NewServer(
		dispatcher,
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
		cfg:        cfg,
		logger:     logger,
		store:      store,
		server:     srv,
		handler:    oauthhandler.New(srv, logger),
		users:      deps.Users,
		verifier:   deps.Verifier,
		dispatcher: dispatcher,
		linking:    linking,
	}
	o.login = newLogin(o, dispatcher, deps.Users, deps.Verifier, email)
	if email != nil {
		email.bind(o.login)
	}
	if err := o.ensureWebClient(issuer); err != nil {
		store.Close()
		return nil, err
	}

	sweepCtx, cancel := context.WithCancel(context.Background())
	o.cancelSweep = cancel
	go o.sweepLoop(sweepCtx)

	return o, nil
}

// buildProviders assembles the upstream providers that are configured.
// buildLinkingProviders returns providers aimed at the account-linking
// callback, keyed by provider name.
func buildLinkingProviders(cfg config.AuthConfig, issuer string) (map[string]providers.Provider, error) {
	out := map[string]providers.Provider{}
	if cfg.GoogleClientID != "" {
		g, err := googleprov.New(&libgoogle.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  issuer + "/account/link/callback/google",
			Scopes:       []string{"openid", "email", "profile"},
		})
		if err != nil {
			return nil, fmt.Errorf("configure google linking provider: %w", err)
		}
		out[user.ProviderGoogle] = g
	}
	if cfg.GiteeClientID != "" {
		g, err := giteeprov.New(giteeConfig(cfg, issuer+"/account/link/callback/gitee"))
		if err != nil {
			return nil, fmt.Errorf("configure gitee linking provider: %w", err)
		}
		out[user.ProviderGitee] = g
	}
	return out, nil
}

func buildProviders(cfg config.AuthConfig, issuer, storePath string) (*multi.Provider, *emailProvider, map[string]providers.Provider, error) {
	var all []providers.Provider

	if cfg.GoogleClientID != "" {
		g, err := googleprov.New(&libgoogle.Config{
			ClientID:     cfg.GoogleClientID,
			ClientSecret: cfg.GoogleClientSecret,
			RedirectURL:  issuer + "/oauth/callback",
			Scopes:       []string{"openid", "email", "profile"},
		})
		if err != nil {
			return nil, nil, nil, fmt.Errorf("configure google provider: %w", err)
		}
		all = append(all, g)
	}
	if cfg.GiteeClientID != "" {
		g, err := giteeprov.New(giteeConfig(cfg, issuer+"/oauth/callback/gitee"))
		if err != nil {
			return nil, nil, nil, fmt.Errorf("configure gitee provider: %w", err)
		}
		all = append(all, g)
	}

	var email *emailProvider
	if cfg.EmailEnabled {
		email = &emailProvider{}
		all = append(all, email)
	}
	if len(all) == 0 {
		return nil, nil, nil, errors.New("no auth provider configured")
	}
	dispatcher, err := multi.NewWithIndex(issuer, all[0].Name(), storePath+".providers", all...)
	if err != nil {
		return nil, nil, nil, err
	}
	// Linking needs providers whose callback is the account-linking path, not
	// the login path. Built separately because a provider's redirect URL is
	// fixed when it is constructed.
	linking, err := buildLinkingProviders(cfg, issuer)
	if err != nil {
		return nil, nil, nil, err
	}
	return dispatcher, email, linking, nil
}

// RegisterRoutes mounts the OAuth flow endpoints, the discovery documents and
// the login surface on mux.
func (o *OAuth) RegisterRoutes(mux *http.ServeMux, endpointPath string) {
	o.handler.RegisterOAuthRoutes(mux, oauthhandler.OAuthRoutesOptions{
		MCPPath:         endpointPath,
		IncludeMetadata: true,
	})
	// The library registers exactly one callback. Every other provider gets a
	// path of its own; the shim stamps the provider onto the context before
	// delegating to the library's exported callback handler.
	mux.Handle("/oauth/callback/", o.callbackShim())
	o.login.RegisterRoutes(mux)
}

// callbackShim reads the provider name from the last path segment and delegates
// to the library. /oauth/callback/google and /oauth/callback/gitee both land
// here; /oauth/callback itself is the library's own route and never does.
func (o *OAuth) callbackShim() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		provider := strings.Trim(strings.TrimPrefix(r.URL.Path, "/oauth/callback/"), "/")
		if provider == "" || strings.Contains(provider, "/") {
			http.NotFound(w, r)
			return
		}
		ctx := multi.WithProvider(r.Context(), provider)
		ctx = multi.WithState(ctx, r.URL.Query().Get("state"))
		o.handler.ServeCallback(w, r.WithContext(ctx))
	})
}

// Protect validates the bearer token, resolves it to a user and injects the
// authenticated identity into the request context.
func (o *OAuth) Protect(next http.Handler) http.Handler {
	return o.handler.ValidateToken(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		info, ok := oauthhandler.UserInfoFromContext(r.Context())
		if !ok || info == nil {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ident, err := o.resolveIdentity(r.Context(), info)
		if err != nil {
			o.logger.Warn("authenticated token has no user", "subject", info.ID, "error", err)
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		ctx := o.withIdentity(r.Context(), ident)
		// The web and workspace layers read a different context key, owned by
		// the identity package so they do not import this one.
		ctx = identity.WithIdentity(ctx, &identity.Identity{
			UserID:        string(ident.UserID),
			Provider:      ident.Provider,
			Email:         ident.Email,
			Name:          ident.Name,
			EmailVerified: ident.EmailVerified,
			EmailRequired: ident.EmailRequired,
		})
		next.ServeHTTP(w, r.WithContext(ctx))
	}))
}

// Gate blocks business endpoints until the user has confirmed an email
// address. Account endpoints must not be wrapped with it, or the user could
// never complete the confirmation.
func (o *OAuth) Gate(next http.Handler) http.Handler {
	if !o.cfg.RequireEmailBinding {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ident, ok := identityFrom(r.Context())
		if ok && ident.EmailRequired {
			w.Header().Set("X-Profile-URL", strings.TrimRight(o.cfg.PublicURL, "/")+"/account?bind=email")
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusForbidden)
			_, _ = fmt.Fprintf(w, `{"error":{"code":%q,"message":"请先验证邮箱"}}`, emailBindingCode)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// Users returns the user store.
func (o *OAuth) Users() *user.Store { return o.users }

// Verifier returns the email verification service, which may be nil when
// email is not configured.
func (o *OAuth) Verifier() *verify.Service { return o.verifier }

// Login returns the login surface.
func (o *OAuth) Login() *Login { return o.login }

// PublicURL returns the configured issuer.
func (o *OAuth) PublicURL() string { return o.cfg.PublicURL }

// Close releases the store and stops background work.
func (o *OAuth) Close() error {
	if o.cancelSweep != nil {
		o.cancelSweep()
	}
	return o.store.Close()
}

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

// resolveIdentity maps a validated token to a user, creating the account on
// the first login through an external provider.
func (o *OAuth) resolveIdentity(ctx context.Context, info *providers.UserInfo) (*resolvedIdentity, error) {
	provider, subject := providerAndSubject(ctx, info)

	if u, _, ok := o.users.FindByIdentity(provider, subject); ok {
		if _, err := o.users.TouchLogin(provider, subject, time.Now()); err != nil {
			return nil, err
		}
		return o.identityFor(u.ID, provider)
	}

	// A local email login already created the user; the token subject is the
	// user id itself.
	if provider == user.ProviderEmail {
		return o.identityFor(user.ID(info.ID), provider)
	}

	u, err := o.users.CreateOAuthUser(provider, subject, user.Profile{
		Email:         info.Email,
		EmailVerified: info.EmailVerified,
		Name:          info.Name,
		AvatarURL:     info.Picture,
	}, time.Now())
	if errors.Is(err, user.ErrEmailTaken) {
		// The address belongs to an existing account. Bind the new identity to
		// it instead of failing the login: the user proved control of the
		// address by authenticating with the provider.
		existing, _ := o.users.FindByEmail(info.Email)
		if _, linkErr := o.users.LinkIdentity(existing.ID, provider, subject, user.Profile{
			Email: info.Email, EmailVerified: info.EmailVerified, Name: info.Name, AvatarURL: info.Picture,
		}, time.Now()); linkErr != nil {
			return nil, linkErr
		}
		return o.identityFor(existing.ID, provider)
	}
	if err != nil {
		return nil, err
	}
	o.logger.Info("created user on first login", "user_id", u.ID, "provider", provider)
	return o.identityFor(u.ID, provider)
}

func (o *OAuth) identityFor(id user.ID, provider string) (*resolvedIdentity, error) {
	u, err := o.users.Get(id)
	if err != nil {
		return nil, err
	}
	return &resolvedIdentity{
		UserID:        u.ID,
		Provider:      provider,
		Email:         u.PrimaryEmail,
		Name:          u.DisplayName,
		EmailVerified: u.EmailVerified,
		// Gate on the verified address, not the stored flag. The flag is set when
		// the account is created and nothing clears it, so trusting it locks out
		// users who have already confirmed an address.
		EmailRequired: o.cfg.RequireEmailBinding && !u.EmailVerified,
	}, nil
}

// inferProvider guesses where a token came from when the request context
// carries no selection. Opaque library validations hit this path.
func (o *OAuth) inferProvider(info *providers.UserInfo) string {
	if strings.HasPrefix(info.ID, "local_") || strings.HasPrefix(info.ID, user.IDPrefix) {
		return user.ProviderEmail
	}
	if o.dispatcher.Has(user.ProviderGoogle) {
		return user.ProviderGoogle
	}
	return o.dispatcher.Name()
}

func (o *OAuth) issuer() string { return strings.TrimRight(o.cfg.PublicURL, "/") }

// revokeUserTokens drops every token issued to a user. A failure is logged by
// the caller; there is nothing better to do with it.
func (o *OAuth) revokeUserTokens(id user.ID) error {
	return o.server.RevokeAllTokensForUserClient(context.Background(), string(id), "")
}

func (o *OAuth) sweepLoop(ctx context.Context) {
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			removed, err := o.users.Sweep(time.Now(), 7*24*time.Hour)
			if err != nil {
				o.logger.Warn("user sweep failed", "error", err)
				continue
			}
			if len(removed) > 0 {
				o.logger.Info("removed abandoned unverified users", "count", len(removed))
			}
		}
	}
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
