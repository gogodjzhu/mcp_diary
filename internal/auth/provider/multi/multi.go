// Package multi dispatches one mcp-oauth Provider interface across several
// upstream providers. The library calls AuthorizationURL, ExchangeCode and
// ValidateToken with no provider argument, so the selection travels in the
// context instead: the login page sets it before starting a flow, and the
// callback shim sets it before handing the request to the library.
package multi

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"
)

// ErrUnknownProvider is returned when a request names no configured provider.
var ErrUnknownProvider = errors.New("unknown auth provider")

type providerKey struct{}

// WithProvider returns a context that selects provider for the dispatch.
func WithProvider(ctx context.Context, provider string) context.Context {
	return context.WithValue(ctx, providerKey{}, provider)
}

// ProviderFrom reads the provider selection from ctx.
func ProviderFrom(ctx context.Context) (string, bool) {
	name, ok := ctx.Value(providerKey{}).(string)
	return name, ok && name != ""
}

// Provider routes provider operations to one of several upstream providers.
// The token indexes remember which upstream issued a token, because the
// library validates tokens without saying where they came from. Only hashes
// are kept. The state index is persisted: the library validates the provider
// token during the callback with a fresh context, so an in-memory table would
// miss and the token would be checked against the wrong provider.
type Provider struct {
	mu           sync.RWMutex
	providers    map[string]providers.Provider
	order        []string
	fallback     string
	issuer       string
	indexPath    string
	accessIndex  map[string]string
	refreshIndex map[string]string
	stateIndex   map[string]string
}

// New builds a dispatcher. fallback is the provider used when a call arrives
// without a selection, which is what happens for the library's own
// authorization redirect. issuer is this server's public URL; the login page
// redirect is built from it.
func New(issuer, fallback string, all ...providers.Provider) (*Provider, error) {
	return NewWithIndex(issuer, fallback, "", all...)
}

// NewWithIndex is New with a file that persists the provider selection across
// the callback, where the library validates the token with a fresh context.
func NewWithIndex(issuer, fallback, indexPath string, all ...providers.Provider) (*Provider, error) {
	p := &Provider{
		providers:    map[string]providers.Provider{},
		fallback:     fallback,
		issuer:       issuer,
		indexPath:    indexPath,
		accessIndex:  map[string]string{},
		refreshIndex: map[string]string{},
		stateIndex:   map[string]string{},
	}
	for _, one := range all {
		if one == nil {
			continue
		}
		name := one.Name()
		if _, dup := p.providers[name]; dup {
			return nil, fmt.Errorf("duplicate provider %q", name)
		}
		p.providers[name] = one
		p.order = append(p.order, name)
	}
	if len(p.providers) == 0 {
		return nil, errors.New("at least one provider is required")
	}
	p.loadIndex()
	if _, ok := p.providers[fallback]; !ok {
		return nil, fmt.Errorf("fallback provider %q is not configured", fallback)
	}
	return p, nil
}

// Names returns the configured provider names in registration order.
func (p *Provider) Names() []string {
	out := make([]string, len(p.order))
	copy(out, p.order)
	return out
}

// Has reports whether name is configured.
func (p *Provider) Has(name string) bool {
	_, ok := p.providers[name]
	return ok
}

// Name returns the fallback provider's name, which is what the library
// advertises.
func (p *Provider) Name() string { return p.fallback }

// DefaultScopes returns the fallback provider's scopes.
func (p *Provider) DefaultScopes() []string {
	return p.providers[p.fallback].DefaultScopes()
}

// AuthorizationURL builds the login page URL. The library generates the
// provider state before it ever knows which provider the user will pick, so
// the choice happens on the page the library redirects to. The page receives
// the state and redirects onward to /login/start once the user decides.
//
// The URL must be absolute: the library rejects anything that is not http or
// https before it redirects.
func (p *Provider) AuthorizationURL(state, codeChallenge, codeChallengeMethod string, scopes []string, opts *providers.AuthorizationURLOptions) string {
	return strings.TrimRight(p.issuer, "/") + "/login?state=" + urlQueryEscape(state)
}

// urlQueryEscape escapes a state for a query string without pulling in net/url
// at every call site.
func urlQueryEscape(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '~':
			b.WriteRune(r)
		default:
			fmt.Fprintf(&b, "%%%02X", r)
		}
	}
	return b.String()
}

// ExchangeCode exchanges a code with the provider selected in ctx.
func (p *Provider) ExchangeCode(ctx context.Context, code, verifier string) (*oauth2.Token, error) {
	upstream, name, err := p.resolve(ctx)
	if err != nil {
		return nil, err
	}
	p.noteState(ctx, name)
	tok, err := upstream.ExchangeCode(ctx, code, verifier)
	if err != nil {
		return nil, err
	}
	p.remember(name, tok)
	return tok, nil
}

// ValidateToken validates a token with the provider that issued it.
func (p *Provider) ValidateToken(ctx context.Context, accessToken string) (*providers.UserInfo, error) {
	upstream, _, err := p.resolveToken(ctx, accessToken)
	if err != nil {
		return nil, err
	}
	return upstream.ValidateToken(ctx, accessToken)
}

// RefreshToken refreshes a token with the provider that issued it.
func (p *Provider) RefreshToken(ctx context.Context, refreshToken string) (*oauth2.Token, error) {
	p.mu.RLock()
	name := p.refreshIndex[hash(refreshToken)]
	p.mu.RUnlock()
	if name == "" {
		name = p.fallback
	}
	upstream, ok := p.providers[name]
	if !ok {
		return nil, ErrUnknownProvider
	}
	tok, err := upstream.RefreshToken(ctx, refreshToken)
	if err != nil {
		return nil, err
	}
	p.remember(name, tok)
	return tok, nil
}

// RevokeToken revokes a token with every provider, since the caller does not
// say which one issued it. The first success wins.
func (p *Provider) RevokeToken(ctx context.Context, token string) error {
	var first error
	for _, name := range p.order {
		if err := p.providers[name].RevokeToken(ctx, token); err == nil {
			return nil
		} else if first == nil {
			first = err
		}
	}
	return first
}

// HealthCheck checks every configured provider.
func (p *Provider) HealthCheck(ctx context.Context) error {
	var problems []string
	for _, name := range p.order {
		if err := p.providers[name].HealthCheck(ctx); err != nil {
			problems = append(problems, name+": "+err.Error())
		}
	}
	if len(problems) > 0 {
		return errors.New(strings.Join(problems, "; "))
	}
	return nil
}

// StartURL is the in-app URL the login page redirects to once the user has
// chosen a provider. The handler behind it performs the real upstream redirect.
func StartURL(provider, state string) string {
	return "/login/start?provider=" + provider + "&state=" + state
}

// Resolve returns the provider a context selects, or the fallback when the
// context carries no selection.
func (p *Provider) Resolve(ctx context.Context) (providers.Provider, string, error) {
	return p.resolve(ctx)
}

// Get returns the named provider.
func (p *Provider) Get(name string) (providers.Provider, bool) {
	upstream, ok := p.providers[name]
	return upstream, ok
}

func (p *Provider) resolve(ctx context.Context) (providers.Provider, string, error) {
	name, _ := ProviderFrom(ctx)
	if name == "" {
		name = p.fallback
	}
	upstream, ok := p.providers[name]
	if !ok {
		return nil, "", fmt.Errorf("%w: %s", ErrUnknownProvider, name)
	}
	return upstream, name, nil
}

// remember records which provider issued a token. Hashes only, so the table
// is useless to anyone who reads the process memory.
func (p *Provider) remember(name string, tok *oauth2.Token) {
	if tok == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok.AccessToken != "" {
		p.accessIndex[hash(tok.AccessToken)] = name
	}
	if tok.RefreshToken != "" {
		p.refreshIndex[hash(tok.RefreshToken)] = name
	}
	defer p.persistIndex()
}

func hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func (p *Provider) resolveToken(ctx context.Context, accessToken string) (providers.Provider, string, error) {
	if name, ok := ProviderFrom(ctx); ok {
		if upstream, exists := p.providers[name]; exists {
			return upstream, name, nil
		}
		return nil, "", fmt.Errorf("%w: %s", ErrUnknownProvider, name)
	}
	p.mu.RLock()
	name := p.accessIndex[hash(accessToken)]
	if name == "" {
		name = p.stateIndex[stateFrom(ctx)]
	}
	p.mu.RUnlock()
	if name == "" && strings.HasPrefix(accessToken, "local_") {
		// A local email login has no upstream provider. The library validates
		// tokens without the context the callback shim set, so the prefix is
		// the only signal left. Without it this falls through to the upstream
		// provider, which rejects the token and the request 401s.
		name = "email"
	}
	if name == "" {
		name = p.fallback
	}
	return p.providers[name], name, nil
}
