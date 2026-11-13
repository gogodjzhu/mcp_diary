package oauth

import (
	"context"
	"strings"

	"github.com/giantswarm/mcp-oauth/providers"

	"github.com/gogodjzhu/mcp-diary/internal/auth/provider/multi"
	"github.com/gogodjzhu/mcp-diary/internal/auth/user"
)

// providerAndSubject recovers which provider authenticated the user and the
// subject to store. A Gitee id carries a "gitee:" prefix because the library
// re-validates tokens with a context that has lost the provider selection, and
// a bare numeric id collides with other providers' subjects.
func providerAndSubject(ctx context.Context, info *providers.UserInfo) (string, string) {
	if provider, rest, ok := strings.Cut(info.ID, ":"); ok && provider == user.ProviderGitee {
		return provider, rest
	}
	if provider, ok := multi.ProviderFrom(ctx); ok && provider != "" {
		return provider, info.ID
	}
	if strings.HasPrefix(info.ID, "local_") || strings.HasPrefix(info.ID, user.IDPrefix) {
		return user.ProviderEmail, info.ID
	}
	return user.ProviderGoogle, info.ID
}
