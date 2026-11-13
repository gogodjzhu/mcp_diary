package oauth

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/gogodjzhu/mcp-diary/internal/auth/user"
	"github.com/gogodjzhu/mcp-diary/internal/platform/config"
	"github.com/gogodjzhu/mcp-diary/internal/platform/logging"
	"github.com/gogodjzhu/mcp-diary/internal/transport/httpapi"
)

// fakeGitee is the smallest Gitee that completes an authorization: it redirects
// back with a fixed code and serves one user.
func fakeGitee(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/oauth/authorize", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		http.Redirect(w, r, q.Get("redirect_uri")+"?code=fake-code&state="+q.Get("state"), http.StatusFound)
	})
	mux.HandleFunc("/oauth/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"access_token":"fake-gitee-token","token_type":"bearer","expires_in":86400}`)
	})
	mux.HandleFunc("/api/v5/user", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `{"id":768136,"login":"delzhu","name":"del"}`) // bare id; the provider prefixes it
	})
	mux.HandleFunc("/api/v5/emails", func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, `[{"email":"delzhu@outlook.com","state":"confirmed"}]`)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

// TestGiteeLoginResolvesToBoundUser reproduces a Gitee login where the Gitee
// user id collides with another provider's subject. The login must reach the
// account the Gitee identity is bound to, not whatever the fallback provider
// happens to return.
func TestGiteeLoginResolvesToBoundUser(t *testing.T) {
	gitee := fakeGitee(t)

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	issuer := "http://" + listener.Addr().String()

	o := newGiteeTestOAuth(t, issuer, gitee.URL)
	owner, err := o.users.CreateEmailUser("mr_djzhu@163.com", "secret123", "DJ", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.users.LinkIdentity(owner.ID, user.ProviderGitee, "768136", user.Profile{Email: "delzhu@outlook.com"}, time.Now()); err != nil {
		t.Fatal(err)
	}

	mux := http.NewServeMux()
	o.RegisterRoutes(mux, "/mcp")
	mux.HandleFunc("/auth/callback", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/done?"+r.URL.RawQuery, http.StatusFound)
	})
	mux.Handle("/api/", o.Protect(httpapi.New(httpapi.Config{Users: o.Users()})))

	ts := httptest.NewUnstartedServer(mux)
	ts.Listener = listener
	ts.Start()
	t.Cleanup(ts.Close)

	verifier := "verifier-0123456789abcdef0123456789abcdefXYZ"
	sum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(sum[:])

	noRedirect := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}

	start := issuer + "/oauth/authorize?" + url.Values{
		"response_type": {"code"}, "client_id": {WebClientID},
		"redirect_uri": {issuer + "/auth/callback"}, "scope": {"mcp"},
		"state": {"clientstate1234567890abcdef"}, "code_challenge": {challenge},
		"code_challenge_method": {"S256"}, "resource": {issuer + "/mcp"},
	}.Encode()
	resp, err := noRedirect.Get(start)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	providerState := mustQuery(t, resp.Header.Get("Location"), "state")

	// The user picks Gitee. The fake Gitee redirects straight back.
	resp, err = noRedirect.Get(issuer + "/login/start?provider=gitee&state=" + providerState)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	giteeURL := resp.Header.Get("Location")
	if !strings.Contains(giteeURL, "code_challenge=") {
		t.Fatalf("gitee authorize URL missing PKCE challenge: %s", giteeURL)
	}

	resp, err = noRedirect.Get(giteeURL)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	callback := resp.Header.Get("Location")

	resp, err = noRedirect.Get(callback)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("callback -> %d %s", resp.StatusCode, raw)
	}
	authCode := mustQuery(t, resp.Header.Get("Location"), "code")

	exchange := url.Values{
		"grant_type": {"authorization_code"}, "code": {authCode},
		"redirect_uri": {issuer + "/auth/callback"}, "client_id": {WebClientID},
		"code_verifier": {verifier}, "resource": {issuer + "/mcp"},
	}
	resp, err = ts.Client().PostForm(issuer+"/oauth/token", exchange)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var token map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&token)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("token -> %d %v", resp.StatusCode, token)
	}

	req, _ := http.NewRequest(http.MethodGet, issuer+"/api/me", nil)
	req.Header.Set("Authorization", "Bearer "+token["access_token"].(string))
	resp, err = ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var me map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&me)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/api/me -> %d %v", resp.StatusCode, me)
	}
	if me["user_id"] != string(owner.ID) {
		t.Fatalf("logged in as %v, want the bound account %s", me["user_id"], owner.ID)
	}
	if me["email_required"] == true {
		t.Fatalf("verified account was gated: %v", me)
	}
}

func newGiteeTestOAuth(t *testing.T, issuer, giteeURL string) *OAuth {
	t.Helper()
	cfg := config.Default().Auth
	cfg.Enabled = true
	cfg.PublicURL = issuer
	cfg.EmailEnabled = true
	cfg.GoogleClientID = ""
	cfg.GoogleClientSecret = ""
	cfg.GiteeClientID = "gitee-id"
	cfg.GiteeClientSecret = "gitee-secret"
	cfg.GiteeBaseURL = giteeURL

	dir := t.TempDir()
	users, err := user.Open(dir+"/users.json", []byte("test-secret"))
	if err != nil {
		t.Fatal(err)
	}
	logger, _ := logging.New("error", "text")
	o, err := New(cfg, "/mcp", dir+"/oauth.json", logger, Dependencies{Users: users})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}
