// Package gitee implements the mcp-oauth Provider interface for Gitee. Gitee's
// user endpoint does not return an email address, so the address is fetched
// from the emails endpoint and may be empty when the user granted no access to
// it. Callers treat an empty address as "ask the user", not as a failure.
package gitee

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"
)

// Compile-time check that Provider implements the provider interface.
var _ providers.Provider = (*Provider)(nil)

const (
	providerName = "gitee"

	authorizeURL = "https://gitee.com/oauth/authorize"
	tokenURL     = "https://gitee.com/oauth/token"

	userURL   = "https://gitee.com/api/v5/user"
	emailsURL = "https://gitee.com/api/v5/emails"

	// userAgent is required: Gitee rejects token requests without one.
	userAgent = "mcp-diary"

	maxBody = 1 << 20
)

// Config configures the Gitee provider.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
	HTTPClient   *http.Client
	// AuthURL, TokenURL, UserURL and EmailsURL override the Gitee endpoints.
	// Empty uses the real ones. Tests point them at a stand-in server.
	AuthURL   string
	TokenURL  string
	UserURL   string
	EmailsURL string
}

// Provider authenticates users through Gitee.
type Provider struct {
	cfg       *oauth2.Config
	client    *http.Client
	userURL   string
	emailsURL string
}

// New builds a Gitee provider.
func New(cfg Config) (*Provider, error) {
	if cfg.ClientID == "" || cfg.ClientSecret == "" {
		return nil, fmt.Errorf("gitee client id and secret are required")
	}
	if cfg.RedirectURL == "" {
		return nil, fmt.Errorf("gitee redirect url is required")
	}
	scopes := cfg.Scopes
	if len(scopes) == 0 {
		scopes = []string{"user_info", "emails"}
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 15 * time.Second}
	}
	authEndpoint := cfg.AuthURL
	if authEndpoint == "" {
		authEndpoint = authorizeURL
	}
	tokenEndpoint := cfg.TokenURL
	if tokenEndpoint == "" {
		tokenEndpoint = tokenURL
	}
	userEndpoint := cfg.UserURL
	if userEndpoint == "" {
		userEndpoint = userURL
	}
	emailsEndpoint := cfg.EmailsURL
	if emailsEndpoint == "" {
		emailsEndpoint = emailsURL
	}
	return &Provider{
		userURL:   userEndpoint,
		emailsURL: emailsEndpoint,
		cfg: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL:   authEndpoint,
				TokenURL:  tokenEndpoint,
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
		client: client,
	}, nil
}

// Config returns the OAuth2 configuration, so account linking can exchange a
// code without the PKCE verifier the library always sends.
func (p *Provider) Config() *oauth2.Config { return p.cfg }

// WithRedirect returns a copy that sends the user back to redirectURL. Account
// linking needs its own callback, distinct from the login one.
func (p *Provider) WithRedirect(redirectURL string) *Provider {
	clone := *p
	cfg := *p.cfg
	cfg.RedirectURL = redirectURL
	clone.cfg = &cfg
	return &clone
}

// Name returns the provider name.
func (p *Provider) Name() string { return providerName }

// DefaultScopes returns the configured scopes.
func (p *Provider) DefaultScopes() []string { return p.cfg.Scopes }

// AuthorizationURL builds the Gitee authorization URL.
func (p *Provider) AuthorizationURL(state, codeChallenge, codeChallengeMethod string, scopes []string, opts *providers.AuthorizationURLOptions) string {
	if len(scopes) == 0 {
		scopes = p.cfg.Scopes
	}
	var extra []oauth2.AuthCodeOption
	if codeChallenge != "" {
		extra = append(extra,
			oauth2.SetAuthURLParam("code_challenge", codeChallenge),
			oauth2.SetAuthURLParam("code_challenge_method", codeChallengeMethod),
		)
	}
	extra = append(extra, providers.ApplyAuthorizationURLOptions(opts)...)
	return p.cfg.AuthCodeURL(state, extra...)
}

// ExchangeCode trades an authorization code for a token.
func (p *Provider) ExchangeCode(ctx context.Context, code, verifier string) (*oauth2.Token, error) {
	return providers.ExchangeCodeWithPKCE(ctx, p.cfg, p.client, code, verifier)
}

// ValidateToken fetches the Gitee profile for an access token.
func (p *Provider) ValidateToken(ctx context.Context, accessToken string) (*providers.UserInfo, error) {
	var profile struct {
		ID        int64  `json:"id"`
		Login     string `json:"login"`
		Name      string `json:"name"`
		AvatarURL string `json:"avatar_url"`
	}
	if err := p.get(ctx, p.userURL, accessToken, &profile); err != nil {
		return nil, fmt.Errorf("gitee user info: %w", err)
	}
	if profile.ID == 0 {
		return nil, fmt.Errorf("gitee user info: empty id")
	}

	email, verified := p.primaryEmail(ctx, accessToken)
	name := profile.Name
	if name == "" {
		name = profile.Login
	}
	return &providers.UserInfo{
		// Prefix the id so the account lookup can tell a Gitee user from a
		// Google one even when the request carries no provider selection. The
		// library validates tokens with a fresh context, which drops it.
		ID:            providerName + ":" + strconv.FormatInt(profile.ID, 10),
		Email:         email,
		EmailVerified: verified,
		Name:          name,
		Picture:       profile.AvatarURL,
	}, nil
}

// RefreshToken exchanges a refresh token for a new access token.
func (p *Provider) RefreshToken(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	ctx = context.WithValue(ctx, oauth2.HTTPClient, p.client)
	src := p.cfg.TokenSource(ctx, &oauth2.Token{RefreshToken: refreshToken})
	tok, err := src.Token()
	if err != nil {
		return nil, fmt.Errorf("gitee refresh: %w", err)
	}
	return tok, nil
}

// RevokeToken is a no-op: Gitee exposes no token revocation endpoint.
func (p *Provider) RevokeToken(context.Context, string) error { return nil }

// HealthCheck verifies that the Gitee authorization endpoint answers.
func (p *Provider) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, authorizeURL, nil)
	if err != nil {
		return err
	}
	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("gitee health check: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 500 {
		return fmt.Errorf("gitee health check: status %d", resp.StatusCode)
	}
	return nil
}

// primaryEmail returns the best address Gitee will disclose. A failure here is
// not fatal: the account page asks the user to confirm an address instead.
func (p *Provider) primaryEmail(ctx context.Context, accessToken string) (string, bool) {
	var emails []struct {
		Email string `json:"email"`
		State string `json:"state"`
		Scope []string
	}
	if err := p.get(ctx, p.emailsURL, accessToken, &emails); err != nil || len(emails) == 0 {
		return "", false
	}
	for _, e := range emails {
		if strings.EqualFold(e.State, "confirmed") && e.Email != "" {
			return e.Email, true
		}
	}
	if emails[0].Email != "" {
		return emails[0].Email, false
	}
	return "", false
}

func (p *Provider) get(ctx context.Context, endpoint, accessToken string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+accessToken)
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBody))
	if err != nil {
		return err
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d: %s", resp.StatusCode, truncate(body))
	}
	if err := json.Unmarshal(body, out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}

func truncate(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 200 {
		return s[:200]
	}
	return s
}
