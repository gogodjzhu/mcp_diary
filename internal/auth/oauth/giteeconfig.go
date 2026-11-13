package oauth

import (
	"strings"

	giteeprov "github.com/gogodjzhu/mcp-diary/internal/auth/provider/gitee"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
)

// giteeConfig builds the Gitee provider configuration, pointing every endpoint
// at GiteeBaseURL when one is set.
func giteeConfig(cfg config.AuthConfig, redirectURL string) giteeprov.Config {
	out := giteeprov.Config{
		ClientID:     cfg.GiteeClientID,
		ClientSecret: cfg.GiteeClientSecret,
		RedirectURL:  redirectURL,
	}
	if base := strings.TrimRight(cfg.GiteeBaseURL, "/"); base != "" {
		out.AuthURL = base + "/oauth/authorize"
		out.TokenURL = base + "/oauth/token"
		out.UserURL = base + "/api/v5/user"
		out.EmailsURL = base + "/api/v5/emails"
	}
	return out
}
