// Package google wraps the mcp-oauth Google provider to expose its OAuth2
// configuration. Account linking exchanges the authorization code itself,
// because the library's ExchangeCode always sends a PKCE verifier and Google
// rejects one when the authorization request carried no challenge.
package google

import (
	"github.com/giantswarm/mcp-oauth/providers"
	lib "github.com/giantswarm/mcp-oauth/providers/google"
	"golang.org/x/oauth2"
	googleoauth "golang.org/x/oauth2/google"
)

// Provider is a Google provider with its configuration exposed.
type Provider struct {
	*lib.Provider
	cfg *oauth2.Config
}

// New builds a Google provider.
func New(cfg *lib.Config) (*Provider, error) {
	inner, err := lib.NewProvider(cfg)
	if err != nil {
		return nil, err
	}
	return &Provider{
		Provider: inner,
		cfg: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       cfg.Scopes,
			Endpoint:     googleoauth.Endpoint,
		},
	}, nil
}

// Config returns the OAuth2 configuration.
func (p *Provider) Config() *oauth2.Config { return p.cfg }

// compile-time check that the wrapper still satisfies the provider interface.
var _ providers.Provider = (*Provider)(nil)
