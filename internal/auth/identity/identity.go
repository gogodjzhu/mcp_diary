// Package identity describes the authenticated principal carried on a request.
// Token verification lives in the authorization server (internal/auth/oauth);
// this package only holds the identity the workspace and web layers read from
// context, so neither of them has to import the authorization server.
package identity

import "strings"

// Identity is the authenticated principal extracted from a verified access
// token. UserID is the primary key; everything else is descriptive.
type Identity struct {
	// UserID is the stable account id ("usr_" + 32 hex). It is the workspace
	// directory name and the token subject.
	UserID string
	// Provider is the login method that produced this session.
	Provider string
	// Email is the account's primary email address, when one is set.
	Email string
	// Name is the display name, when available.
	Name string
	// EmailVerified reports whether the primary email was confirmed.
	EmailVerified bool
	// EmailRequired reports whether business endpoints must refuse this session
	// until an email address is confirmed.
	EmailRequired bool
	// Scopes are the scopes granted to the token.
	Scopes []string

	// Subject is the legacy provider subject. Tokens issued before the user
	// store existed carry one and no UserID; new tokens leave it empty.
	Subject string
}

// Username returns the most human-friendly identifier for the user. The email
// wins because it is what existing clients display.
func (i Identity) Username() string {
	if i.Email != "" {
		return i.Email
	}
	if i.Name != "" {
		return i.Name
	}
	if i.UserID != "" {
		return i.UserID
	}
	return i.Subject
}

// WorkspaceKey returns the directory name for this user's workspace. It prefers
// the user id and falls back to the legacy slug so sessions issued before the
// user store existed keep their files.
func (i Identity) WorkspaceKey() string {
	if strings.HasPrefix(i.UserID, "usr_") {
		return i.UserID
	}
	return i.Slug()
}

// Slug returns a filesystem-safe directory name derived from the legacy
// identity. Kept for workspaces created before user ids existed.
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
