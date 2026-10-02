// Package auth describes the authenticated principal carried on a request.
// Token verification lives in the authorization server (internal/auth/oauth);
// this package only holds the identity the workspace layer reads from context.
package identity

import "strings"

// Identity is the authenticated principal extracted from a verified access
// token.
type Identity struct {
	// Subject is the stable provider-specific user id (OIDC "sub").
	Subject string
	// Email is the verified email address, when available.
	Email string
	// Name is the display name, when available.
	Name string
	// Scopes are the scopes granted to the token.
	Scopes []string
}

// Username returns the most human-friendly identifier for the user.
func (i Identity) Username() string {
	if i.Email != "" {
		return i.Email
	}
	if i.Name != "" {
		return i.Name
	}
	return i.Subject
}

// Slug returns a filesystem-safe directory name derived from the identity. The
// result never contains path separators or relative-path components, so it is
// safe to join underneath a workspace root.
func (i Identity) Slug() string {
	raw := i.Email
	if raw == "" {
		raw = i.Subject
	}
	raw = strings.ToLower(strings.TrimSpace(raw))

	var b strings.Builder
	for _, r := range raw {
		switch {
		case r >= 'a' && r <= 'z',
			r >= '0' && r <= '9',
			r == '@', r == '.', r == '_', r == '-':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}

	slug := strings.Trim(b.String(), ".-_")
	if slug == "" || slug == "." || slug == ".." {
		slug = "user"
	}
	if len(slug) > 64 {
		slug = slug[:64]
	}
	return slug
}
