package oauth

import (
	"context"

	"github.com/gogodjzhu/mcp-diary/internal/auth/user"
)

// resolvedIdentity is the principal the authorization server injects into a
// request. It lives in this package so the web layer can read it without
// importing the authorization server.
type resolvedIdentity struct {
	UserID        user.ID
	Provider      string
	Email         string
	Name          string
	EmailVerified bool
	EmailRequired bool
	Scopes        []string
}

type resolvedKey struct{}

func (o *OAuth) withIdentity(ctx context.Context, ident *resolvedIdentity) context.Context {
	return context.WithValue(ctx, resolvedKey{}, ident)
}

func identityFrom(ctx context.Context) (*resolvedIdentity, bool) {
	ident, ok := ctx.Value(resolvedKey{}).(*resolvedIdentity)
	return ident, ok && ident != nil
}

// Identity is the authenticated principal as the rest of the process sees it.
// It is deliberately a flat struct: callers must not need the user store to
// read who a request belongs to.
type Identity struct {
	UserID        string
	Provider      string
	Email         string
	Name          string
	EmailVerified bool
	EmailRequired bool
	Scopes        []string
}

// IdentityFrom extracts the authenticated identity from ctx.
func IdentityFrom(ctx context.Context) (Identity, bool) {
	ident, ok := identityFrom(ctx)
	if !ok {
		return Identity{}, false
	}
	return Identity{
		UserID:        string(ident.UserID),
		Provider:      ident.Provider,
		Email:         ident.Email,
		Name:          ident.Name,
		EmailVerified: ident.EmailVerified,
		EmailRequired: ident.EmailRequired,
		Scopes:        ident.Scopes,
	}, true
}
