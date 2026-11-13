package multi

import (
	"context"
	"testing"

	"github.com/giantswarm/mcp-oauth/providers"
	"golang.org/x/oauth2"
)

// stub is a provider that records which calls it received.
type stub struct {
	name     string
	token    string
	userInfo *providers.UserInfo
}

func (s *stub) Name() string            { return s.name }
func (s *stub) DefaultScopes() []string { return []string{"s"} }
func (s *stub) AuthorizationURL(state, _, _ string, _ []string, _ *providers.AuthorizationURLOptions) string {
	return "https://" + s.name + ".example/auth?state=" + state
}
func (s *stub) ExchangeCode(context.Context, string, string) (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token, RefreshToken: "refresh-" + s.token}, nil
}
func (s *stub) ValidateToken(context.Context, string) (*providers.UserInfo, error) {
	return s.userInfo, nil
}
func (s *stub) RefreshToken(context.Context, string) (*oauth2.Token, error) {
	return &oauth2.Token{AccessToken: s.token + "-new"}, nil
}
func (s *stub) RevokeToken(context.Context, string) error { return nil }
func (s *stub) HealthCheck(context.Context) error         { return nil }

func TestDispatchByContext(t *testing.T) {
	google := &stub{name: "google", token: "g-token", userInfo: &providers.UserInfo{ID: "g"}}
	gitee := &stub{name: "gitee", token: "e-token", userInfo: &providers.UserInfo{ID: "e"}}
	p, err := New("https://mcp.test", "google", google, gitee)
	if err != nil {
		t.Fatal(err)
	}

	ctx := WithProvider(context.Background(), "gitee")
	tok, err := p.ExchangeCode(ctx, "code", "")
	if err != nil {
		t.Fatal(err)
	}
	if tok.AccessToken != "e-token" {
		t.Fatalf("exchanged with the wrong provider: %s", tok.AccessToken)
	}

	// A later validation carries no context selection; the token index must
	// route it back to the provider that issued it.
	info, err := p.ValidateToken(context.Background(), tok.AccessToken)
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "e" {
		t.Fatalf("validated with the wrong provider: %+v", info)
	}
}

func TestUnknownSelectionFallsBack(t *testing.T) {
	google := &stub{name: "google", token: "g", userInfo: &providers.UserInfo{ID: "g"}}
	p, err := New("https://mcp.test", "google", google)
	if err != nil {
		t.Fatal(err)
	}
	info, err := p.ValidateToken(context.Background(), "never-seen")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "g" {
		t.Fatalf("fallback failed: %+v", info)
	}
}

func TestLocalTokenRoutesToEmailProvider(t *testing.T) {
	google := &stub{name: "google", userInfo: &providers.UserInfo{ID: "g"}}
	email := &stub{name: "email", userInfo: &providers.UserInfo{ID: "local-user"}}
	p, err := New("https://mcp.test", "google", google, email)
	if err != nil {
		t.Fatal(err)
	}
	// No context selection and no remembered index, as happens when the
	// library validates a token on a later request.
	info, err := p.ValidateToken(context.Background(), "local_usr_abcdef0123456789abcdef0123456789")
	if err != nil {
		t.Fatal(err)
	}
	if info.ID != "local-user" {
		t.Fatalf("local token reached the wrong provider: %+v", info)
	}
}

func TestAuthorizationURLPointsAtLoginPage(t *testing.T) {
	p, err := New("https://mcp.test", "google", &stub{name: "google"})
	if err != nil {
		t.Fatal(err)
	}
	if got := p.AuthorizationURL("abc", "", "", nil, nil); got != "https://mcp.test/login?state=abc" {
		t.Fatalf("AuthorizationURL = %q", got)
	}
}
