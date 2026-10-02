package identity

import "context"

type identityKey struct{}

// WithIdentity returns a copy of ctx carrying the authenticated identity.
func WithIdentity(ctx context.Context, identity *Identity) context.Context {
	if identity == nil {
		return ctx
	}
	return context.WithValue(ctx, identityKey{}, identity)
}

// IdentityFrom extracts the authenticated identity from ctx.
func IdentityFrom(ctx context.Context) (*Identity, bool) {
	identity, ok := ctx.Value(identityKey{}).(*Identity)
	return identity, ok && identity != nil
}
