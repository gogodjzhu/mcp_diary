package oauth

import (
	"context"

	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"
)

// redirectExchanger is a provider that can trade a code using its own redirect
// URL. The library's ExchangeCode always sends a PKCE verifier, and Google
// rejects a verifier when the authorization request carried no challenge.
// Account linking makes no challenge, so it exchanges the code itself.
type redirectExchanger interface {
	Config() *oauth2.Config
}

// exchangeForLink trades an authorization code for a token without PKCE.
func exchangeForLink(ctx context.Context, upstream providers.Provider, code string) (*oauth2.Token, error) {
	if ex, ok := upstream.(redirectExchanger); ok {
		return ex.Config().Exchange(ctx, code)
	}
	return upstream.ExchangeCode(ctx, code, "")
}
